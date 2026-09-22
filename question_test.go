package jev

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestQuestionMarshalJSON(t *testing.T) {
	t.Parallel()

	rich := map[string]any{"summary": "duplicated"}

	tests := []struct {
		name     string
		question Question
		want     string
	}{
		{"noul empty", Noul{}, `{"type":"noul"}`},
		{"noul instructions", Noul{Instructions: "Spam?"}, `{"type":"noul","instructions":"Spam?"}`},
		// An explicitly empty value is meaningful and must survive encoding; only
		// a nil (unset) one is omitted.
		{"noul empty string kept", Noul{Instructions: ""}, `{"type":"noul","instructions":""}`},
		{"noul empty slice kept", Noul{Instructions: []any{}}, `{"type":"noul","instructions":[]}`},
		{"noul empty criteria kept", Noul{Criteria: &NoulCriteria{}}, `{"type":"noul","criteria":{}}`},
		{"noul criteria true", Noul{Criteria: &NoulCriteria{True: "Yes"}}, `{"type":"noul","criteria":{"true":"Yes"}}`},
		{"noul criteria both", Noul{Criteria: &NoulCriteria{True: "Yes", False: "No"}}, `{"type":"noul","criteria":{"true":"Yes","false":"No"}}`},
		{"noul rich instructions", Noul{Instructions: rich}, `{"type":"noul","instructions":{"summary":"duplicated"}}`},

		{"choice", Choice{Criteria: map[string]any{"a": nil}}, `{"type":"choice","criteria":{"a":null}}`},
		{
			"choice described",
			Choice{Instructions: "Tone?", Criteria: map[string]any{"calm": "polite", "angry": nil}},
			`{"type":"choice","instructions":"Tone?","criteria":{"angry":null,"calm":"polite"}}`,
		},

		{"score", Score{Criteria: []any{"good"}}, `{"type":"score","criteria":["good"]}`},
		{
			"score described",
			Score{Instructions: "Quality?", Criteria: []any{"bad", "ok", "great"}},
			`{"type":"score","instructions":"Quality?","criteria":["bad","ok","great"]}`,
		},

		{
			"raw passthrough",
			RawQuestion{"type": "noul", "instructions": "Spam?", "weight": 3},
			`{"instructions":"Spam?","type":"noul","weight":3}`,
		},
		{"raw future type", RawQuestion{"type": "aurora"}, `{"type":"aurora"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.question)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Marshal() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestQuestionMarshalDoesNotMutateRaw(t *testing.T) {
	t.Parallel()

	raw := RawQuestion{"type": "score", "criteria": []any{"good"}, "weight": 1}
	if _, err := json.Marshal(raw); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if len(raw) != 3 || raw["type"] != "score" || raw["weight"] != 1 {
		t.Errorf("raw question mutated by encoding: %v", raw)
	}
}

func TestValidateQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions map[string]Question
		want      error
	}{
		{"empty", map[string]Question{}, ErrNoQuestions},
		{"nil map", nil, ErrNoQuestions},

		{"noul", map[string]Question{"q": Noul{}}, nil},
		{"noul pointer", map[string]Question{"q": &Noul{}}, nil},
		{"choice", map[string]Question{"q": Choice{Criteria: map[string]any{"a": nil}}}, nil},
		{"score", map[string]Question{"q": Score{Criteria: []any{"good"}}}, nil},

		{"choice no criteria", map[string]Question{"q": Choice{}}, ErrInvalidQuestion},
		{"choice empty criteria", map[string]Question{"q": Choice{Criteria: map[string]any{}}}, ErrInvalidQuestion},
		{"choice pointer no criteria", map[string]Question{"q": &Choice{}}, ErrInvalidQuestion},
		{"score no criteria", map[string]Question{"q": Score{}}, ErrInvalidQuestion},
		{"score empty criteria", map[string]Question{"q": Score{Criteria: []any{}}}, ErrInvalidQuestion},

		{"nil question", map[string]Question{"q": nil}, ErrInvalidQuestion},
		{"nil pointer", map[string]Question{"q": (*Noul)(nil)}, ErrInvalidQuestion},

		{"raw ok", map[string]Question{"q": RawQuestion{"type": "noul"}}, nil},
		{"raw future type", map[string]Question{"q": RawQuestion{"type": "aurora", "n": nil}}, nil},
		{"raw no type", map[string]Question{"q": RawQuestion{"instructions": "x"}}, ErrInvalidQuestion},
		{"raw empty type", map[string]Question{"q": RawQuestion{"type": ""}}, ErrInvalidQuestion},
		{"raw non-string type", map[string]Question{"q": RawQuestion{"type": 1}}, ErrInvalidQuestion},
		{"raw choice no criteria", map[string]Question{"q": RawQuestion{"type": "choice"}}, ErrInvalidQuestion},
		{"raw score no criteria", map[string]Question{"q": RawQuestion{"type": "score"}}, ErrInvalidQuestion},
		{"raw score empty criteria", map[string]Question{"q": RawQuestion{"type": "score", "criteria": []any{}}}, ErrInvalidQuestion},
		{"raw score criteria present", map[string]Question{"q": RawQuestion{"type": "score", "criteria": []any{"good"}}}, nil},
		// The API, not the SDK, judges a raw question's criteria shape.
		{"raw choice odd criteria", map[string]Question{"q": RawQuestion{"type": "choice", "criteria": []any{"odd"}}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateQuestions(tt.questions)
			if !errors.Is(err, tt.want) {
				t.Errorf("validateQuestions() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateQuestionsIsDeterministic(t *testing.T) {
	t.Parallel()

	// Both questions are invalid; the sorted name order decides which is reported.
	questions := map[string]Question{"b": Score{}, "a": Choice{}}
	for range 20 {
		err := validateQuestions(questions)
		if err == nil || !errors.Is(err, ErrInvalidQuestion) {
			t.Fatalf("validateQuestions() error = %v", err)
		}
		if want := `choice question "a"`; !strings.Contains(err.Error(), want) {
			t.Fatalf("validateQuestions() = %q, want it to name %s", err, want)
		}
	}
}

func TestDerefNilPointers(t *testing.T) {
	t.Parallel()

	// Every question kind may be handed over as a pointer, including a nil one.
	for name, question := range map[string]Question{
		"noul":   (*Noul)(nil),
		"choice": (*Choice)(nil),
		"score":  (*Score)(nil),
		"raw":    (*RawQuestion)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := validateQuestions(map[string]Question{"q": question}); !errors.Is(err, ErrInvalidQuestion) {
				t.Errorf("validateQuestions(%T) error = %v, want ErrInvalidQuestion", question, err)
			}
		})
	}

	valid := map[string]Question{
		"choice": &Choice{Criteria: map[string]any{"a": nil}},
		"score":  &Score{Criteria: []any{"good"}},
		"raw":    &RawQuestion{"type": "noul"},
	}
	if err := validateQuestions(valid); err != nil {
		t.Errorf("validateQuestions() error = %v, want nil for valid pointer questions", err)
	}
}

func TestPointerQuestionsMarshal(t *testing.T) {
	t.Parallel()

	// A question handed over as a pointer must encode exactly like its value.
	tests := []struct {
		question Question
		want     string
	}{
		{&Noul{Instructions: "Spam?"}, `{"type":"noul","instructions":"Spam?"}`},
		{&Choice{Criteria: map[string]any{"a": nil}}, `{"type":"choice","criteria":{"a":null}}`},
		{&Score{Criteria: []any{"good"}}, `{"type":"score","criteria":["good"]}`},
		{&RawQuestion{"type": "aurora"}, `{"type":"aurora"}`},
	}

	for _, tt := range tests {
		got, err := json.Marshal(map[string]Question{"q": tt.question})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if want := `{"q":` + tt.want + `}`; string(got) != want {
			t.Errorf("Marshal(%T) = %s, want %s", tt.question, got, want)
		}
	}
}

func TestLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []string
		want  map[string]any
	}{
		{"several", []string{"billing", "technical", "other"}, map[string]any{"billing": nil, "technical": nil, "other": nil}},
		{"one", []string{"spam"}, map[string]any{"spam": nil}},
		// A repeated label collapses, matching what a map literal would do.
		{"duplicates collapse", []string{"a", "b", "a"}, map[string]any{"a": nil, "b": nil}},
		{"none", nil, map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Labels(tt.names...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Labels(%q) = %v, want %v", tt.names, got, tt.want)
			}
			for label, description := range got {
				if description != nil {
					t.Errorf("Labels() described %q as %v, want an undescribed label", label, description)
				}
			}
		})
	}
}

func TestLabelsMarshalsLikeAMapLiteral(t *testing.T) {
	t.Parallel()

	helper, err := json.Marshal(Choice{Instructions: "Tone?", Criteria: Labels("calm", "angry")})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	literal, err := json.Marshal(Choice{Instructions: "Tone?", Criteria: map[string]any{"calm": nil, "angry": nil}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if string(helper) != string(literal) {
		t.Errorf("Labels() encoded as %s, want it identical to the map literal %s", helper, literal)
	}
	if want := `{"type":"choice","instructions":"Tone?","criteria":{"angry":null,"calm":null}}`; string(helper) != want {
		t.Errorf("Marshal() = %s, want %s", helper, want)
	}
}

func TestLabelsResultIsIndependent(t *testing.T) {
	t.Parallel()

	// The returned map is the caller's to describe further.
	criteria := Labels("billing", "other")
	criteria["billing"] = "Payments or invoices"

	got, err := json.Marshal(Choice{Criteria: criteria})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := `{"type":"choice","criteria":{"billing":"Payments or invoices","other":null}}`; string(got) != want {
		t.Errorf("Marshal() = %s, want %s", got, want)
	}
}

func TestLabelsEmptyIsRejected(t *testing.T) {
	t.Parallel()

	// Labels() with no names is an empty criteria map, which validation
	// refuses just as it refuses a bare Choice{}.
	err := validateQuestions(map[string]Question{"q": Choice{Criteria: Labels()}})
	if !errors.Is(err, ErrInvalidQuestion) {
		t.Errorf("validateQuestions() error = %v, want ErrInvalidQuestion", err)
	}
}

func TestLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		descriptions []string
		want         []any
	}{
		{"several", []string{"can wait", "this week", "today"}, []any{"can wait", "this week", "today"}},
		{"one", []string{"only"}, []any{"only"}},
		// Unlike Labels, position is the meaning, so repeats are distinct levels.
		{"repeats are distinct levels", []string{"same", "same"}, []any{"same", "same"}},
		{"none", nil, []any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Levels(tt.descriptions...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Levels(%q) = %v, want %v", tt.descriptions, got, tt.want)
			}
		})
	}
}

func TestLevelsPreservesOrder(t *testing.T) {
	t.Parallel()

	// A level's index is its score, so the encoded order must be the author's.
	got, err := json.Marshal(Score{Instructions: "How urgent?", Criteria: Levels("can wait", "this week", "today")})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	want := `{"type":"score","instructions":"How urgent?","criteria":["can wait","this week","today"]}`
	if string(got) != want {
		t.Errorf("Marshal() = %s, want %s", got, want)
	}
}

func TestLevelsMarshalsLikeASliceLiteral(t *testing.T) {
	t.Parallel()

	helper, err := json.Marshal(Score{Criteria: Levels("bad", "good")})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	literal, err := json.Marshal(Score{Criteria: []any{"bad", "good"}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if string(helper) != string(literal) {
		t.Errorf("Levels() encoded as %s, want it identical to the slice literal %s", helper, literal)
	}
}

func TestLevelsEmptyIsRejected(t *testing.T) {
	t.Parallel()

	err := validateQuestions(map[string]Question{"q": Score{Criteria: Levels()}})
	if !errors.Is(err, ErrInvalidQuestion) {
		t.Errorf("validateQuestions() error = %v, want ErrInvalidQuestion", err)
	}
}
