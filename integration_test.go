//go:build integration

// These tests call the real TypeSafe API and spend the account's credit, so
// they build only with the integration tag:
//
//	go test -tags integration ./...
//
// TYPESAFE_API_KEY, and optionally TYPESAFE_BASE_URL, come from the
// environment or else from a .env file in this directory. Without a key, or
// when the account cannot pay, the tests skip instead of failing.
package typesafe_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	typesafe "serge.ax/go/typesafe-sdk-go"
)

func TestMain(m *testing.M) {
	loadDotenv(".env")
	os.Exit(m.Run())
}

// loadDotenv fills in TypeSafe settings the environment leaves blank.
func loadDotenv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for line := range strings.Lines(string(data)) {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		name = strings.TrimSpace(strings.TrimPrefix(name, "export "))
		if !ok || !strings.HasPrefix(name, "TYPESAFE_") || strings.TrimSpace(os.Getenv(name)) != "" {
			continue
		}
		os.Setenv(name, strings.Trim(strings.TrimSpace(value), `"'`))
	}
}

func liveClient(t *testing.T, options ...typesafe.ClientOption) *typesafe.Client {
	t.Helper()
	if strings.TrimSpace(os.Getenv(typesafe.APIKeyEnv)) == "" {
		skip(t, typesafe.APIKeyEnv+" is set neither in the environment nor in .env")
	}
	client, err := typesafe.New(options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

// unpaid reports whether a response refuses a request the account cannot pay
// for. The API does not document that refusal, so the standard 402 counts, as
// does a 403 or 429 that speaks of credit.
func unpaid(status int, message string) bool {
	message = strings.ToLower(message)
	aboutCredit := slices.ContainsFunc([]string{"credit", "balance", "billing", "payment", "quota", "funds"},
		func(word string) bool { return strings.Contains(message, word) })
	return status == http.StatusPaymentRequired ||
		(status == http.StatusForbidden || status == http.StatusTooManyRequests) && aboutCredit
}

func skipIfUnpaid(t *testing.T, err error) {
	t.Helper()
	if apiErr, ok := errors.AsType[*typesafe.APIError](err); ok && unpaid(apiErr.Status, apiErr.Message) {
		skip(t, fmt.Sprintf("the account cannot pay for requests: %d %s", apiErr.Status, apiErr.Message))
	}
}

var warned = map[string]bool{}

// skip skips the test and, on GitHub Actions, raises each distinct reason as a
// warning once, so a run that never reached the API does not pass unnoticed.
func skip(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" && !warned[reason] {
		warned[reason] = true
		fmt.Printf("::warning title=Live API tests skipped::%s\n", reason)
	}
	t.Skip(reason)
}

func TestLiveModelsList(t *testing.T) {
	models, err := liveClient(t).Models.List(t.Context())
	skipIfUnpaid(t, err)
	if err != nil {
		t.Fatalf("Models.List() error = %v", err)
	}

	if !slices.ContainsFunc(models, func(m typesafe.ModelMetadata) bool { return m.Name == typesafe.DefaultModel }) {
		t.Errorf("Models.List() = %v, want it to include the default model %q", models, typesafe.DefaultModel)
	}
	for _, model := range models {
		if _, err := time.Parse(time.RFC3339, model.ReleaseDate); err != nil {
			t.Errorf("model %q ReleaseDate = %q, want an RFC 3339 timestamp", model.Name, model.ReleaseDate)
		}
	}
}

func TestLiveSystemOneAnswersEveryQuestionType(t *testing.T) {
	labels := map[string]typesafe.Content{"billing": "About payments or charges", "technical": nil, "other": nil}
	levels := []typesafe.Content{"Can wait", "Needs attention this week", "Needs attention today"}

	result, err := liveClient(t).SystemOne(t.Context(),
		map[string]any{"subject": "Duplicate charge", "message": "I was charged twice. Please fix this ASAP."},
		typesafe.Questions{
			"billing":  typesafe.Noul{Instructions: "Is this about billing?"},
			"category": typesafe.Choice{Instructions: "What is this ticket about?", Criteria: labels},
			"urgency":  typesafe.Score{Instructions: "How urgent is this ticket?", Criteria: levels},
		})
	skipIfUnpaid(t, err)
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if result.Model == "" || result.RequestID == "" || result.Usage.InputTokens <= 0 {
		t.Errorf("Model = %q, RequestID = %q, Usage = %+v, want all reported", result.Model, result.RequestID, result.Usage)
	}
	billing, category, urgency := result.Nouls()["billing"], result.Choices()["category"], result.Scores()["urgency"]
	if len(result.Answers) != 3 || billing == nil || category == nil || urgency == nil {
		t.Fatalf("Answers = %v, want a noul, a choice and a score", result.Answers)
	}

	if !isProbability(billing.Noul) {
		t.Errorf("Nouls()[billing].Noul = %v, want a probability", billing.Noul)
	}

	if _, isLabel := labels[category.Choice]; !isLabel || !isProbability(category.Confidence) {
		t.Errorf("Choices()[category] = %+v, want one of the labels with a confidence", category)
	}
	if !sameKeys(category.Probabilities, labels) || !sumsToOne(category.Probabilities) {
		t.Errorf("Choices()[category].Probabilities = %v, want every label, summing to 1", category.Probabilities)
	}

	rubric := maps.Collect(slices.All(levels))
	if urgency.Score < 0 || urgency.Score > float64(len(levels)-1) || !isProbability(urgency.Confidence) {
		t.Errorf("Scores()[urgency] = %+v, want a score within the rubric with a confidence", urgency)
	}
	if !maps.Equal(urgency.Legend, rubric) {
		t.Errorf("Scores()[urgency].Legend = %v, want the rubric %v", urgency.Legend, rubric)
	}
	if !sameKeys(urgency.Probabilities, rubric) || !sumsToOne(urgency.Probabilities) {
		t.Errorf("Scores()[urgency].Probabilities = %v, want every level, summing to 1", urgency.Probabilities)
	}
}

func TestLiveErrorsMatchTheirCategories(t *testing.T) {
	question := typesafe.Questions{"billing": typesafe.Noul{Instructions: "Is this about billing?"}}

	_, err := liveClient(t, typesafe.WithAPIKey("not-a-real-key")).SystemOne(t.Context(), "state", question)
	if !errors.Is(err, typesafe.ErrAuthentication) {
		t.Errorf("SystemOne() with a bad key error = %v, want ErrAuthentication", err)
	}

	_, err = liveClient(t).SystemOne(t.Context(), "state", question, typesafe.WithModel("no-such-model"))
	skipIfUnpaid(t, err)
	apiErr, ok := errors.AsType[*typesafe.APIError](err)
	if !ok || !errors.Is(err, typesafe.ErrBadRequest) || apiErr.Message == "" || apiErr.RequestID == "" {
		t.Errorf("SystemOne() with an unknown model error = %v, want ErrBadRequest with a message and request ID", err)
	}
}

func TestLiveValidateAgreesWithTheAPI(t *testing.T) {
	liveClient(t)
	questions := map[string]typesafe.Question{
		"noul with instructions":        typesafe.Noul{Instructions: "Is this about billing?"},
		"noul with one criterion":       typesafe.Noul{Criteria: &typesafe.NoulCriteria{True: "About billing"}},
		"noul with nothing":             typesafe.Noul{},
		"noul with empty instructions":  typesafe.Noul{Instructions: ""},
		"choice with one label":         typesafe.Choice{Criteria: map[string]typesafe.Content{"billing": nil}},
		"choice without labels":         typesafe.Choice{},
		"score with one level":          typesafe.Score{Criteria: []typesafe.Content{"Can wait"}},
		"score without levels":          typesafe.Score{},
		"raw choice with null criteria": typesafe.RawQuestion{"type": "choice", "criteria": nil},
		"raw choice with list criteria": typesafe.RawQuestion{"type": "choice", "criteria": []string{"billing"}},
		"raw score with empty criteria": typesafe.RawQuestion{"type": "score", "criteria": []any{}},
	}

	for name, question := range questions {
		t.Run(name, func(t *testing.T) {
			sdkAccepts := typesafe.Questions{"q": question}.Validate() == nil
			status, body := postQuestion(t, question)
			if apiAccepts := status == http.StatusOK; sdkAccepts != apiAccepts {
				t.Errorf("Validate() accepts = %v, but the API answered %d %s", sdkAccepts, status, body)
			}
		})
	}
}

func postQuestion(t *testing.T, question typesafe.Question) (int, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"state":     "I was charged twice.",
		"model":     typesafe.DefaultModel,
		"questions": map[string]typesafe.Question{"q": question},
	})
	if err != nil {
		t.Fatalf("encoding the question: %v", err)
	}
	baseURL := cmp.Or(strings.TrimSpace(os.Getenv(typesafe.BaseURLEnv)), typesafe.DefaultBaseURL)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/systemone", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(os.Getenv(typesafe.APIKeyEnv)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending the question: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if unpaid(resp.StatusCode, string(body)) {
		skip(t, fmt.Sprintf("the account cannot pay for requests: %d %s", resp.StatusCode, body))
	}
	return resp.StatusCode, string(body)
}

func isProbability(p float64) bool { return p >= 0 && p <= 1 }

func sumsToOne[K comparable](probabilities map[K]float64) bool {
	sum := 0.0
	for _, p := range probabilities {
		sum += p
	}
	return math.Abs(sum-1) < 0.02
}

func sameKeys[K comparable, V, W any](a map[K]V, b map[K]W) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
