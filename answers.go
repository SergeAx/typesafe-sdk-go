package typesafe

import (
	"encoding/json"
	"net/http"
)

// Answer is one of [*NoulAnswer], [*ChoiceAnswer], or [*ScoreAnswer], matching
// the type of the question it answers.
type Answer interface {
	// Type reports the answer's discriminator: "noul", "choice", or "score".
	Type() string
}

// NoulAnswer answers a [Noul].
type NoulAnswer struct {
	// Noul is the probability of a yes answer, from 0 to 1. Values near 0.5
	// mean the model is unsure.
	Noul float64
}

func (*NoulAnswer) Type() string { return "noul" }

// ChoiceAnswer answers a [Choice].
type ChoiceAnswer struct {
	// Choice is the label with the highest probability.
	Choice string
	// Confidence in the selected label, from 0 to 1.
	Confidence float64
	// Probabilities of every label, keyed by label, summing to about 1.
	Probabilities map[string]float64
}

func (*ChoiceAnswer) Type() string { return "choice" }

// ScoreAnswer answers a [Score].
type ScoreAnswer struct {
	// Score is the probability-weighted average of the rubric levels, so it may
	// fall between them.
	Score float64
	// Confidence in the score, from 0 to 1.
	Confidence float64
	// Legend is the requested rubric, keyed by score level.
	Legend map[int]Content
	// Probabilities of every score level, summing to about 1.
	Probabilities map[int]float64
}

func (*ScoreAnswer) Type() string { return "score" }

// Usage reports the tokens a request consumed. Counts are zero when the API
// does not report them.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneResponse holds the answers to one request, keyed by the question
// names it was sent with.
type SystemOneResponse struct {
	// Model that answered the questions, which may differ from the alias asked for.
	Model string
	// Answers keyed by question name. Answers of a type this SDK version does
	// not model are dropped, with a warning.
	Answers map[string]Answer
	Usage   Usage
	// RequestID is the x-typesafe-request-id response header, empty when absent.
	RequestID string
	// HTTPResponse is the response the answers were read from, with its body
	// already consumed.
	HTTPResponse *http.Response
}

// Nouls returns the yes/no answers, keyed by question name.
func (r *SystemOneResponse) Nouls() map[string]*NoulAnswer {
	nouls := map[string]*NoulAnswer{}
	for name, answer := range r.Answers {
		if noul, ok := answer.(*NoulAnswer); ok {
			nouls[name] = noul
		}
	}
	return nouls
}

// Choices returns the choice answers, keyed by question name.
func (r *SystemOneResponse) Choices() map[string]*ChoiceAnswer {
	choices := map[string]*ChoiceAnswer{}
	for name, answer := range r.Answers {
		if choice, ok := answer.(*ChoiceAnswer); ok {
			choices[name] = choice
		}
	}
	return choices
}

// Scores returns the score answers, keyed by question name.
func (r *SystemOneResponse) Scores() map[string]*ScoreAnswer {
	scores := map[string]*ScoreAnswer{}
	for name, answer := range r.Answers {
		if score, ok := answer.(*ScoreAnswer); ok {
			scores[name] = score
		}
	}
	return scores
}

// ModelMetadata describes a model or alias available to the account.
type ModelMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// ReleaseDate is formatted as YYYY-MM-DD.
	ReleaseDate string `json:"release_date"`
}

// Required fields are pointers or maps, which stay nil when the response omits
// a field or sets it to null, so a missing value is reported rather than
// silently defaulted.
type systemOneWire struct {
	Model   *string                    `json:"model"`
	Usage   *Usage                     `json:"usage"`
	Answers map[string]json.RawMessage `json:"answers"`
}

// answerWire is the wire form of one answer type. answer returns the decoded
// answer, or the name of the first required field the response left out.
type answerWire interface {
	answer() (Answer, string)
}

type noulWire struct {
	Noul *float64 `json:"noul"`
}

func (w *noulWire) answer() (Answer, string) {
	if w.Noul == nil {
		return nil, "noul"
	}
	return &NoulAnswer{Noul: *w.Noul}, ""
}

type choiceWire struct {
	Choice        *string            `json:"choice"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

func (w *choiceWire) answer() (Answer, string) {
	switch {
	case w.Choice == nil:
		return nil, "choice"
	case w.Confidence == nil:
		return nil, "confidence"
	case w.Probabilities == nil:
		return nil, "probabilities"
	}
	return &ChoiceAnswer{Choice: *w.Choice, Confidence: *w.Confidence, Probabilities: w.Probabilities}, ""
}

type scoreWire struct {
	Score         *float64        `json:"score"`
	Confidence    *float64        `json:"confidence"`
	Legend        map[int]Content `json:"legend"`
	Probabilities map[int]float64 `json:"probabilities"`
}

func (w *scoreWire) answer() (Answer, string) {
	switch {
	case w.Score == nil:
		return nil, "score"
	case w.Confidence == nil:
		return nil, "confidence"
	case w.Legend == nil:
		return nil, "legend"
	case w.Probabilities == nil:
		return nil, "probabilities"
	}
	return &ScoreAnswer{Score: *w.Score, Confidence: *w.Confidence, Legend: w.Legend, Probabilities: w.Probabilities}, ""
}

func (r *response) decodeSystemOne() (*SystemOneResponse, error) {
	var wire systemOneWire
	if err := r.unmarshal("", r.body, &wire); err != nil {
		return nil, err
	}
	if wire.Model == nil {
		return nil, r.invalid("model", nil)
	}
	if wire.Answers == nil {
		return nil, r.invalid("answers", nil)
	}

	answers := make(map[string]Answer, len(wire.Answers))
	for name, raw := range wire.Answers {
		answer, err := r.decodeAnswer(name, raw)
		if err != nil {
			return nil, err
		}
		if answer != nil {
			answers[name] = answer
		}
	}

	result := &SystemOneResponse{
		Model:        *wire.Model,
		Answers:      answers,
		RequestID:    r.requestID,
		HTTPResponse: r.http,
	}
	if wire.Usage != nil {
		result.Usage = *wire.Usage
	}
	return result, nil
}

func (r *response) decodeAnswer(name string, raw json.RawMessage) (Answer, error) {
	path := "answers." + name
	var head struct {
		Type *string `json:"type"`
	}
	if err := r.unmarshal(path, raw, &head); err != nil {
		return nil, err
	}
	if head.Type == nil {
		return nil, r.invalid(path+".type", nil)
	}

	var wire answerWire
	switch *head.Type {
	case "noul":
		wire = &noulWire{}
	case "choice":
		wire = &choiceWire{}
	case "score":
		wire = &scoreWire{}
	default:
		// Forward compatibility: a newer API may answer with a type this
		// version does not know. The raw payload stays reachable through
		// HTTPResponse.
		r.logger.Warn("ignoring answer of unrecognized type",
			"answer", name, "type", *head.Type, "request_id", r.requestID)
		return nil, nil
	}
	if err := r.unmarshal(path, raw, wire); err != nil {
		return nil, err
	}
	answer, missing := wire.answer()
	if missing != "" {
		return nil, r.invalid(path+"."+missing, nil)
	}
	return answer, nil
}
