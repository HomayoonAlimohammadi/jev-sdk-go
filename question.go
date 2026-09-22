package jev

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Wire discriminators for the question and answer kinds.
const (
	kindNoul   = "noul"
	kindChoice = "choice"
	kindScore  = "score"
)

// Question is a question to ask about the state. It is a sealed union: only
// [Noul], [Choice], [Score] and [RawQuestion] implement it.
type Question interface {
	question()
}

// Noul is a yes/no question.
//
// See https://docs.typesafe.ai/primitives/noul.
type Noul struct {
	// Instructions is the question or statement to evaluate, as a string, a
	// JSON object, an array, or any other JSON-marshalable value. A nil value
	// is omitted from the request; an explicitly empty one is sent as-is.
	Instructions any `json:"instructions,omitempty"`

	// Criteria optionally describes what counts as a yes and as a no.
	Criteria *NoulCriteria `json:"criteria,omitempty"`
}

func (Noul) question() {}

// MarshalJSON encodes the question with its "noul" type discriminator.
func (n Noul) MarshalJSON() ([]byte, error) {
	type noul Noul // Sheds the method set so json does not recurse into MarshalJSON.
	return json.Marshal(struct {
		Type string `json:"type"`
		noul
	}{kindNoul, noul(n)})
}

// NoulCriteria describes the yes and no outcomes of a [Noul]. A nil field is
// left undescribed and omitted from the request.
type NoulCriteria struct {
	True  any `json:"true,omitempty"`
	False any `json:"false,omitempty"`
}

// Choice is a question that selects one of several named alternatives.
//
// See https://docs.typesafe.ai/primitives/choice.
type Choice struct {
	// Instructions is what the model should decide. See [Noul.Instructions].
	Instructions any `json:"instructions,omitempty"`

	// Criteria maps each label to a description of when it applies. A nil
	// description leaves the label to be interpreted by its name alone.
	// At least one label is required.
	Criteria map[string]any `json:"criteria"`
}

func (Choice) question() {}

// MarshalJSON encodes the question with its "choice" type discriminator.
func (c Choice) MarshalJSON() ([]byte, error) {
	type choice Choice
	return json.Marshal(struct {
		Type string `json:"type"`
		choice
	}{kindChoice, choice(c)})
}

// Labels builds [Choice] criteria for choices that need no description, so a
// call site is not padded out with nil values:
//
//	jev.Choice{
//	    Instructions: "What is this ticket about?",
//	    Criteria:     jev.Labels("billing", "technical", "other"),
//	}
//
// Describe a choice by writing the map out instead; a description sharpens the
// boundary between labels and is worth the extra line when they overlap. A
// label repeated here appears once in the request.
func Labels(names ...string) map[string]any {
	criteria := make(map[string]any, len(names))
	for _, name := range names {
		criteria[name] = nil
	}
	return criteria
}

// Score is a question that rates the state against an ordered rubric.
//
// See https://docs.typesafe.ai/primitives/score.
type Score struct {
	// Instructions is what the model should rate. See [Noul.Instructions].
	Instructions any `json:"instructions,omitempty"`

	// Criteria describes each score level. A description's index is its score,
	// counting from zero. At least one level is required.
	Criteria []any `json:"criteria"`
}

func (Score) question() {}

// MarshalJSON encodes the question with its "score" type discriminator.
func (s Score) MarshalJSON() ([]byte, error) {
	type score Score
	return json.Marshal(struct {
		Type string `json:"type"`
		score
	}{kindScore, score(s)})
}

// RawQuestion is sent verbatim, including its "type" key. It is the escape
// hatch for question kinds or fields the API accepts before this SDK models
// them; the API, not the SDK, validates its contents.
type RawQuestion map[string]any

func (RawQuestion) question() {}

// validateQuestions rejects question maps the API is certain to refuse, so the
// caller gets a local error instead of a round trip. Names are checked in
// sorted order so the reported error does not depend on map iteration.
func validateQuestions(questions map[string]Question) error {
	if len(questions) == 0 {
		return ErrNoQuestions
	}
	for _, name := range slices.Sorted(maps.Keys(questions)) {
		if err := validateQuestion(name, questions[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateQuestion(name string, question Question) error {
	switch q := deref(question).(type) {
	case Noul:
		return nil
	case Choice:
		if len(q.Criteria) == 0 {
			return fmt.Errorf("%w: choice question %q has no criteria; at least one choice is required", ErrInvalidQuestion, name)
		}
	case Score:
		if len(q.Criteria) == 0 {
			return fmt.Errorf("%w: score question %q has no criteria; at least one score is required", ErrInvalidQuestion, name)
		}
	case RawQuestion:
		return validateRawQuestion(name, q)
	default:
		return fmt.Errorf("%w: question %q is nil", ErrInvalidQuestion, name)
	}
	return nil
}

func validateRawQuestion(name string, question RawQuestion) error {
	kind, _ := question["type"].(string)
	if kind == "" {
		return fmt.Errorf("%w: question %q needs a nonempty string %q", ErrInvalidQuestion, name, "type")
	}

	if kind != kindChoice && kind != kindScore {
		return nil
	}

	criteria, ok := question["criteria"]
	if !ok {
		return fmt.Errorf("%w: %s question %q requires %q", ErrInvalidQuestion, kind, name, "criteria")
	}
	if kind == kindScore {
		if levels, ok := criteria.([]any); ok && len(levels) == 0 {
			return fmt.Errorf("%w: score question %q has no criteria; at least one score is required", ErrInvalidQuestion, name)
		}
	}
	return nil
}

// deref unwraps a question passed as a pointer so validation treats
// &Noul{...} exactly like Noul{...}. A nil pointer becomes an untyped nil.
func deref(question Question) Question {
	switch q := question.(type) {
	case *Noul:
		if q == nil {
			return nil
		}
		return *q
	case *Choice:
		if q == nil {
			return nil
		}
		return *q
	case *Score:
		if q == nil {
			return nil
		}
		return *q
	case *RawQuestion:
		if q == nil {
			return nil
		}
		return *q
	}
	return question
}
