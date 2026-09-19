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

// Validate reports the problems the API would reject before sending a request.
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
	// Instructions is the required question or statement to evaluate.
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

func (q Noul) validate(name string) error {
	if q.Instructions == nil {
		return newError("noul question %q needs instructions", name)
	}
	return nil
}

// Choice is a question that selects one of the labels in its criteria.
// See https://docs.typesafe.ai/primitives/choice.
type Choice struct {
	// Instructions is the required decision the model should make.
	Instructions Content
	// Criteria maps each label to a description of when it applies, or to nil
	// to let the label speak for itself. At least one label is required.
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
	if q.Instructions == nil {
		return newError("choice question %q needs instructions", name)
	}
	if len(q.Criteria) == 0 {
		return newError("choice question %q needs at least one criterion", name)
	}
	return nil
}

// Score is a question that rates the state against an ordered rubric, scoring
// each criterion by its position from zero.
// See https://docs.typesafe.ai/primitives/score.
type Score struct {
	// Instructions is the required dimension the model should rate.
	Instructions Content
	// Criteria describes the score levels in order, starting at zero.
	// It must contain at least two levels.
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
	if q.Instructions == nil {
		return newError("score question %q needs instructions", name)
	}
	if len(q.Criteria) < 2 {
		return newError("score question %q needs at least two score levels", name)
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
	if kind == "choice" || kind == "score" {
		if _, ok := q["criteria"]; !ok {
			return newError("raw question %q of type %q requires %q", name, kind, "criteria")
		}
	}
	return nil
}
