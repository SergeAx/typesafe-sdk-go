package typesafe

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestQuestionMarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		question Question
		want     string
	}{
		{
			name:     "noul omits unset instructions and criteria",
			question: Noul{},
			want:     `{"type":"noul"}`,
		},
		{
			name:     "noul with instructions",
			question: Noul{Instructions: "Is this message spam?"},
			want:     `{"instructions":"Is this message spam?","type":"noul"}`,
		},
		{
			name: "noul with both criteria",
			question: Noul{
				Instructions: "Is this message spam?",
				Criteria:     &NoulCriteria{True: "Unsolicited advertising", False: "A legitimate conversation"},
			},
			want: `{"criteria":{"false":"A legitimate conversation","true":"Unsolicited advertising"},"instructions":"Is this message spam?","type":"noul"}`,
		},
		{
			name:     "noul with one criterion omits the other",
			question: Noul{Criteria: &NoulCriteria{True: "Unsolicited advertising"}},
			want:     `{"criteria":{"true":"Unsolicited advertising"},"type":"noul"}`,
		},
		{
			name: "choice keeps undescribed labels as null",
			question: Choice{
				Instructions: "What is the tone?",
				Criteria:     map[string]Content{"angry": nil, "calm": "A neutral or polite message"},
			},
			want: `{"criteria":{"angry":null,"calm":"A neutral or polite message"},"instructions":"What is the tone?","type":"choice"}`,
		},
		{
			name:     "score keeps rubric order",
			question: Score{Instructions: "How urgent is this?", Criteria: []Content{"Can wait", "This week", "Today"}},
			want:     `{"criteria":["Can wait","This week","Today"],"instructions":"How urgent is this?","type":"score"}`,
		},
		{
			name:     "structured instructions pass through",
			question: Noul{Instructions: map[string]any{"task": "Identify advertising."}},
			want:     `{"instructions":{"task":"Identify advertising."},"type":"noul"}`,
		},
		{
			name:     "raw question passes through verbatim",
			question: RawQuestion{"type": "future", "knob": 1.5},
			want:     `{"knob":1.5,"type":"future"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.question)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Errorf("Marshal() = %s, want %s", encoded, test.want)
			}
		})
	}
}

func TestQuestionsMarshalJSON(t *testing.T) {
	questions := Questions{"billing": Noul{Instructions: "Is this about billing?"}}

	encoded, err := json.Marshal(questions)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"billing":{"instructions":"Is this about billing?","type":"noul"}}`
	if string(encoded) != want {
		t.Errorf("Marshal() = %s, want %s", encoded, want)
	}
}

func TestQuestionsValidate(t *testing.T) {
	tests := []struct {
		name      string
		questions Questions
		wantErr   bool
	}{
		{name: "nil is rejected", questions: nil, wantErr: true},
		{name: "empty is rejected", questions: Questions{}, wantErr: true},
		{name: "nil question is rejected", questions: Questions{"a": nil}, wantErr: true},
		{name: "noul without instructions is rejected", questions: Questions{"a": Noul{}}, wantErr: true},
		{name: "choice without instructions is rejected", questions: Questions{"a": Choice{}}, wantErr: true},
		{name: "choice without criteria is rejected", questions: Questions{"a": Choice{Instructions: "Choose"}}, wantErr: true},
		{name: "score without criteria is rejected", questions: Questions{"a": Score{Instructions: "How urgent?"}}, wantErr: true},
		{name: "score without instructions is rejected", questions: Questions{"a": Score{Criteria: []Content{"Can wait", "Today"}}}, wantErr: true},
		{name: "score with one level is rejected", questions: Questions{"a": Score{Instructions: "How urgent?", Criteria: []Content{"Can wait"}}}, wantErr: true},
		{name: "raw question without a type is rejected", questions: Questions{"a": RawQuestion{}}, wantErr: true},
		{name: "raw question with an empty type is rejected", questions: Questions{"a": RawQuestion{"type": ""}}, wantErr: true},
		{name: "raw score without criteria is rejected", questions: Questions{"a": RawQuestion{"type": "score"}}, wantErr: true},
		{name: "raw noul is accepted", questions: Questions{"a": RawQuestion{"type": "noul"}}, wantErr: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.questions.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, test.wantErr)
			}
			var sdkErr *Error
			if err != nil && !errors.As(err, &sdkErr) {
				t.Errorf("Validate() error = %T, want *typesafe.Error", err)
			}
		})
	}
}
