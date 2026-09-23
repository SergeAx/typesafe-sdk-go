package typesafe

import "encoding/json"

// Content is the JSON a question, a criterion, or the request state carries:
// a string, a map[string]any, a []any, or nil for an undescribed value.
type Content = any

// Question is one of [Noul], [Choice], [Score], or [RawQuestion].
type Question interface {
	json.Marshaler
	validate(name string) error
}

// Questions are the questions of a request, keyed by the names their answers
// come back under.
type Questions map[string]Question

// Validate reports the problems the API would reject: no questions at all, a
// choice question without labels or a score question without a rubric, typed
// or raw, or a raw question without a type.
func (q Questions) Validate() error {
	if len(q) == 0 {
		return newError("at least one question is required")
	}
	for name, question := range q {
		if question == nil {
			return newError("question %q is nil", name)
		}
		if err := question.validate(name); err != nil {
			return err
		}
	}
	return nil
}

// Noul is a yes/no question or statement, answered with the probability that
// the answer is yes. See https://docs.typesafe.ai/primitives/noul.
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

func (q Noul) validate(string) error { return nil }

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

func (q Choice) validate(name string) error {
	if len(q.Criteria) == 0 {
		return newError("choice question %q has no criteria; at least one label is required", name)
	}
	return nil
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

func (q Score) validate(name string) error {
	if len(q.Criteria) == 0 {
		return newError("score question %q has no criteria; at least one score level is required", name)
	}
	return nil
}

// RawQuestion sends a question payload verbatim, for question types this SDK
// version does not model. It must carry a nonempty "type".
type RawQuestion map[string]any

func (q RawQuestion) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(q))
}

func (q RawQuestion) validate(name string) error {
	kind, ok := q["type"].(string)
	if !ok || kind == "" {
		return newError("raw question %q needs a nonempty string %q field", name, "type")
	}
	var empty, container string
	switch kind {
	case "choice":
		empty, container = "{}", "object"
	case "score":
		empty, container = "[]", "array"
	default:
		return nil
	}
	// Judged by the JSON the API receives, so a json.RawMessage or a custom
	// marshaler counts like a map or a slice. An encoding failure is left to
	// the request, which reports its cause.
	encoded, err := encodeJSON(q["criteria"])
	if err == nil && (encoded[0] != empty[0] || string(encoded) == empty) {
		return newError("raw question %q of type %q needs %q as a nonempty JSON %s, got %s",
			name, kind, "criteria", container, truncate(string(encoded)))
	}
	return nil
}
