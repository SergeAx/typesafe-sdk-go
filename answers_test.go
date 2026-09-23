package typesafe

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"
)

func testResponse(body string) *response {
	return &response{
		http:      &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		body:      []byte(body),
		endpoint:  "POST https://api.typesafe.ai/v1/systemone",
		requestID: "req_123",
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

const fullSystemOneBody = `{
	"model": "jev-1",
	"usage": {"input_tokens": 120, "output_tokens": 12},
	"answers": {
		"billing": {"type": "noul", "noul": 0.98},
		"tone": {
			"type": "choice",
			"choice": "angry",
			"confidence": 0.9,
			"probabilities": {"angry": 0.8, "calm": 0.1, "excited": 0.1}
		},
		"urgency": {
			"type": "score",
			"score": 1.7,
			"confidence": 0.9,
			"legend": {"0": "Can wait", "1": "This week", "2": "Today"},
			"probabilities": {"0": 0.1, "1": 0.1, "2": 0.8}
		}
	}
}`

func TestDecodeSystemOne(t *testing.T) {
	result, err := testResponse(fullSystemOneBody).decodeSystemOne()
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	if result.Model != "jev-1" {
		t.Errorf("Model = %q, want %q", result.Model, "jev-1")
	}
	if result.RequestID != "req_123" {
		t.Errorf("RequestID = %q, want %q", result.RequestID, "req_123")
	}
	if want := (Usage{InputTokens: 120, OutputTokens: 12}); result.Usage != want {
		t.Errorf("Usage = %+v, want %+v", result.Usage, want)
	}

	if got := result.Nouls()["billing"].Noul; got != 0.98 {
		t.Errorf("Nouls()[billing].Noul = %v, want 0.98", got)
	}

	tone := result.Choices()["tone"]
	if tone.Choice != "angry" || tone.Confidence != 0.9 {
		t.Errorf("Choices()[tone] = %+v, want angry at 0.9", tone)
	}
	wantProbabilities := map[string]float64{"angry": 0.8, "calm": 0.1, "excited": 0.1}
	if !reflect.DeepEqual(tone.Probabilities, wantProbabilities) {
		t.Errorf("Choices()[tone].Probabilities = %v, want %v", tone.Probabilities, wantProbabilities)
	}

	urgency := result.Scores()["urgency"]
	if urgency.Score != 1.7 {
		t.Errorf("Scores()[urgency].Score = %v, want 1.7", urgency.Score)
	}
	wantLegend := map[int]Content{0: "Can wait", 1: "This week", 2: "Today"}
	if !reflect.DeepEqual(urgency.Legend, wantLegend) {
		t.Errorf("Scores()[urgency].Legend = %v, want %v", urgency.Legend, wantLegend)
	}
	wantScores := map[int]float64{0: 0.1, 1: 0.1, 2: 0.8}
	if !reflect.DeepEqual(urgency.Probabilities, wantScores) {
		t.Errorf("Scores()[urgency].Probabilities = %v, want %v", urgency.Probabilities, wantScores)
	}
}

func TestDecodeSystemOneGroupsByType(t *testing.T) {
	result, err := testResponse(fullSystemOneBody).decodeSystemOne()
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	if len(result.Answers) != 3 {
		t.Fatalf("len(Answers) = %d, want 3", len(result.Answers))
	}
	for name, group := range map[string]int{"Nouls": len(result.Nouls()), "Choices": len(result.Choices()), "Scores": len(result.Scores())} {
		if group != 1 {
			t.Errorf("len(%s()) = %d, want 1", name, group)
		}
	}
	if got := result.Answers["urgency"].Type(); got != "score" {
		t.Errorf("Answers[urgency].Type() = %q, want %q", got, "score")
	}
}

func TestDecodeSystemOneSkipsUnknownAnswerType(t *testing.T) {
	body := `{"model":"jev-1","usage":{},"answers":{
		"billing":{"type":"noul","noul":0.5},
		"mystery":{"type":"telepathy","vibes":"good"}
	}}`

	result, err := testResponse(body).decodeSystemOne()
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}
	if _, ok := result.Answers["mystery"]; ok {
		t.Error("Answers kept an answer of an unrecognized type")
	}
	if _, ok := result.Answers["billing"]; !ok {
		t.Error("Answers dropped a known answer alongside an unrecognized one")
	}
}

func TestDecodeSystemOneMissingUsage(t *testing.T) {
	result, err := testResponse(`{"model":"jev-1","answers":{"a":{"type":"noul","noul":0.1}}}`).decodeSystemOne()
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}
	if result.Usage != (Usage{}) {
		t.Errorf("Usage = %+v, want the zero value", result.Usage)
	}
}

func TestDecodeSystemOneRejectsMissingFields(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "invalid JSON", body: `not json`, wantField: ""},
		{name: "missing model", body: `{"answers":{}}`, wantField: "model"},
		{name: "missing answers", body: `{"model":"jev-1"}`, wantField: "answers"},
		{
			name:      "missing answer type",
			body:      `{"model":"jev-1","answers":{"a":{"noul":0.5}}}`,
			wantField: "answers.a.type",
		},
		{
			name:      "missing noul probability",
			body:      `{"model":"jev-1","answers":{"a":{"type":"noul"}}}`,
			wantField: "answers.a.noul",
		},
		{
			name:      "missing choice confidence",
			body:      `{"model":"jev-1","answers":{"tone":{"type":"choice","choice":"calm","probabilities":{}}}}`,
			wantField: "answers.tone.confidence",
		},
		{
			name:      "missing choice probabilities",
			body:      `{"model":"jev-1","answers":{"tone":{"type":"choice","choice":"calm","confidence":0.9}}}`,
			wantField: "answers.tone.probabilities",
		},
		{
			name:      "null choice probabilities",
			body:      `{"model":"jev-1","answers":{"tone":{"type":"choice","choice":"calm","confidence":0.9,"probabilities":null}}}`,
			wantField: "answers.tone.probabilities",
		},
		{
			name:      "null score probabilities",
			body:      `{"model":"jev-1","answers":{"u":{"type":"score","score":1,"confidence":0.9,"legend":{},"probabilities":null}}}`,
			wantField: "answers.u.probabilities",
		},
		{
			name:      "missing score legend",
			body:      `{"model":"jev-1","answers":{"u":{"type":"score","score":1,"confidence":0.9,"probabilities":{}}}}`,
			wantField: "answers.u.legend",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := testResponse(test.body).decodeSystemOne()

			invalid, ok := errors.AsType[*ResponseValidationError](err)
			if !ok {
				t.Fatalf("decodeSystemOne() error = %v (%T), want *ResponseValidationError", err, err)
			}
			if invalid.Field != test.wantField {
				t.Errorf("Field = %q, want %q", invalid.Field, test.wantField)
			}
			if invalid.RequestID != "req_123" {
				t.Errorf("RequestID = %q, want %q", invalid.RequestID, "req_123")
			}
		})
	}
}

func TestDecodeSystemOneIgnoresUnknownFields(t *testing.T) {
	body := `{"model":"jev-1","answers":{"a":{"type":"noul","noul":0.5,"rationale":"because"}},"cost":3}`

	if _, err := testResponse(body).decodeSystemOne(); err != nil {
		t.Fatalf("decodeSystemOne() error = %v, want fields from a newer API to be ignored", err)
	}
}
