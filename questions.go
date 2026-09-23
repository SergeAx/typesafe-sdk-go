package typesafe

import (
	"cmp"
	"encoding/json"
)

// Content is the JSON a question, a criterion, or the request state carries:
// a string, a map[string]any, a []any, or nil for an undescribed value.
type Content = any

// Question is one of [Noul], [Choice], [Score], or [RawQuestion].
type Question interface {
	json.Marshaler
	question()
}

func (Noul) question()        {}
func (Choice) question()      {}
func (Score) question()       {}
func (RawQuestion) question() {}

// Questions are the questions of a request, keyed by the names their answers
// come back under.
type Questions map[string]Question

// Validate reports the problems the API would reject: no questions at all, a
// question without a type, a noul with neither instructions nor criteria, a
// choice without labels, or a score without a rubric.
func (q Questions) Validate() error {
	if len(q) == 0 {
		return newError("at least one question is required")
	}
	for name, question := range q {
		if question == nil {
			return newError("question %q is nil", name)
		}
		if err := validateQuestion(name, question); err != nil {
			return err
		}
	}
	return nil
}

// validateQuestion judges a question by the JSON the API receives, so typed and
// raw questions meet the same rules, and a json.RawMessage or custom marshaler
// in a raw one counts like a map or a slice. An encoding failure is left to the
// request, which reports its cause.
func validateQuestion(name string, question Question) error {
	encoded, err := encodeJSON(question)
	if err != nil {
		return nil
	}
	var wire struct {
		Type         string          `json:"type"`
		Instructions json.RawMessage `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	}
	if json.Unmarshal(encoded, &wire) != nil || wire.Type == "" {
		return newError("question %q needs a nonempty string %q field", name, "type")
	}

	var open byte
	var want string
	switch wire.Type {
	case "noul":
		if blank(wire.Instructions) && blank(wire.Criteria) {
			return newError("noul question %q needs instructions or criteria", name)
		}
		return nil
	case "choice":
		open, want = '{', "an object of at least one label"
	case "score":
		open, want = '[', "a list of at least one level"
	default:
		return nil
	}
	if blank(wire.Criteria) || wire.Criteria[0] != open {
		return newError("%s question %q needs criteria as %s, got %s",
			wire.Type, name, want, truncate(cmp.Or(string(wire.Criteria), "nothing")))
	}
	return nil
}

// blank reports whether a JSON value carries nothing: it is absent, null, or an
// empty string, object, or list.
func blank(raw json.RawMessage) bool {
	switch string(raw) {
	case "", "null", `""`, "{}", "[]":
		return true
	}
	return false
}

// Noul is a yes/no question or statement, answered with the probability that
// the answer is yes. It needs instructions, criteria, or both.
// See https://docs.typesafe.ai/primitives/noul.
type Noul struct {
	// Instructions is the question or statement to evaluate.
	Instructions Content
	// Criteria optionally says what counts as a yes and as a no.
	Criteria *NoulCriteria
}

// NoulCriteria describes the two outcomes of a [Noul].
type NoulCriteria struct {
	True  Content
	False Content
}

func (q Noul) MarshalJSON() ([]byte, error) {
	payload := map[string]any{"type": "noul"}
	if q.Instructions != nil {
		payload["instructions"] = q.Instructions
	}
	if q.Criteria != nil {
		payload["criteria"] = q.Criteria
	}
	return json.Marshal(payload)
}

func (c NoulCriteria) MarshalJSON() ([]byte, error) {
	payload := map[string]any{}
	if c.True != nil {
		payload["true"] = c.True
	}
	if c.False != nil {
		payload["false"] = c.False
	}
	return json.Marshal(payload)
}

// Choice is a question that selects one of the labels in its criteria.
// See https://docs.typesafe.ai/primitives/choice.
type Choice struct {
	// Instructions is what the model should decide.
	Instructions Content
	// Criteria maps each label to a description of when it applies, or to nil
	// to let the label speak for itself.
	Criteria map[string]Content
}

func (q Choice) MarshalJSON() ([]byte, error) {
	criteria := q.Criteria
	if criteria == nil {
		criteria = map[string]Content{}
	}
	payload := map[string]any{"type": "choice", "criteria": criteria}
	if q.Instructions != nil {
		payload["instructions"] = q.Instructions
	}
	return json.Marshal(payload)
}

// Score is a question that rates the state against an ordered rubric, scoring
// each criterion by its position from zero.
// See https://docs.typesafe.ai/primitives/score.
type Score struct {
	// Instructions is what the model should rate.
	Instructions Content
	// Criteria describes the score levels in order, starting at zero.
	Criteria []Content
}

func (q Score) MarshalJSON() ([]byte, error) {
	criteria := q.Criteria
	if criteria == nil {
		criteria = []Content{}
	}
	payload := map[string]any{"type": "score", "criteria": criteria}
	if q.Instructions != nil {
		payload["instructions"] = q.Instructions
	}
	return json.Marshal(payload)
}

// RawQuestion sends a question payload verbatim, for question types this SDK
// version does not model. It must carry a nonempty "type".
type RawQuestion map[string]any

func (q RawQuestion) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(q))
}
