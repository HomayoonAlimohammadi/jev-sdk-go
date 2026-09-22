package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Wire discriminators for the question and answer kinds.
const (
	kindNoul   = "noul"
	kindChoice = "choice"
	kindScore  = "score"
)

// Limits the API enforces, checked locally so a request it would refuse fails
// before it is sent. The documentation states the caps. The floors follow the
// service's reported behavior as of 2026-09-20: it accepts a single score level
// although the documentation describes two, and it refuses a noul that asks
// nothing.
const (
	maxChoiceOptions = 255
	maxScoreLevels   = 10
)

// Question is a question to ask about the state. It is a sealed union: only
// [Noul], [ChoiceOf], [ScoreOf] and [RawQuestion] implement it. [Choice] and
// [Score] are the forms with plain string labels and plain int levels.
type Question interface {
	// kind is the wire discriminator the matching answer must carry.
	kind() string

	// validate reports why the API would refuse the question, if it would.
	validate() error
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

func (Noul) kind() string { return kindNoul }

// validate applies the service's rule that a noul must ask something: it needs
// non-empty instructions or at least one described outcome.
func (n Noul) validate() error {
	if n.Criteria != nil && (!isJSONNull(n.Criteria.True) || !isJSONNull(n.Criteria.False)) {
		return nil
	}
	if isJSONEmpty(n.Instructions) {
		return errors.New("has neither instructions nor a described outcome; it needs one")
	}
	return nil
}

// MarshalJSON encodes the question with its "noul" type discriminator.
func (n Noul) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string        `json:"type"`
		Instructions any           `json:"instructions,omitempty"`
		Criteria     *NoulCriteria `json:"criteria,omitempty"`
	}{kindNoul, n.Instructions, n.Criteria})
}

// NoulCriteria describes the yes and no outcomes of a [Noul]. A nil field is
// left undescribed and omitted from the request.
type NoulCriteria struct {
	True  any `json:"true,omitempty"`
	False any `json:"false,omitempty"`
}

// ChoiceOf is a question that selects one of several named alternatives, with
// labels of the caller's own string type. Its answer, read through [Ask], comes
// back as that same type, so a switch over the choice is checked by the
// compiler:
//
//	type Team string
//
//	const (
//	    Billing   Team = "billing"
//	    Technical Team = "technical"
//	)
//
//	team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{Criteria: map[Team]any{Billing: nil, Technical: nil}})
//
// See https://docs.typesafe.ai/primitives/choice.
type ChoiceOf[T ~string] struct {
	// Instructions is what the model should decide. See [Noul.Instructions].
	Instructions any `json:"instructions,omitempty"`

	// Criteria maps each label to a description of when it applies. A nil
	// description leaves the label to be interpreted by its name alone.
	// Between 1 and 255 labels are required.
	Criteria map[T]any `json:"criteria"`
}

// Choice is a [ChoiceOf] with plain string labels.
type Choice = ChoiceOf[string]

func (ChoiceOf[T]) kind() string { return kindChoice }

func (c ChoiceOf[T]) validate() error {
	switch {
	case len(c.Criteria) == 0:
		return errors.New("has no criteria; at least one choice is required")
	case len(c.Criteria) > maxChoiceOptions:
		return fmt.Errorf("has %d choices; the API accepts at most %d", len(c.Criteria), maxChoiceOptions)
	}
	return nil
}

// MarshalJSON encodes the question with its "choice" type discriminator.
func (c ChoiceOf[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string    `json:"type"`
		Instructions any       `json:"instructions,omitempty"`
		Criteria     map[T]any `json:"criteria"`
	}{kindChoice, c.Instructions, c.Criteria})
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
// label repeated here appears once in the request. For labels of your own type,
// see [LabelsOf].
func Labels(names ...string) map[string]any {
	return LabelsOf(names...)
}

// LabelsOf is [Labels] for a [ChoiceOf] with labels of the caller's own type.
func LabelsOf[T ~string](names ...T) map[T]any {
	criteria := make(map[T]any, len(names))
	for _, name := range names {
		criteria[name] = nil
	}
	return criteria
}

// ScoreLevel is satisfied by the integer types a score level can be read back
// as. Level i of a rubric is the value i.
type ScoreLevel interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// ScoreOf is a question that rates the state against an ordered rubric, with
// levels read back as the caller's own integer type. A description's position
// is its level, so an iota-numbered enum lines up with the rubric directly:
//
//	type Urgency int
//
//	const (
//	    CanWait Urgency = iota
//	    ThisWeek
//	    Today
//	)
//
//	urgency := jev.Ask(&req, "urgency", jev.ScoreOf[Urgency]{Criteria: jev.Levels("can wait", "this week", "today")})
//
// See https://docs.typesafe.ai/primitives/score.
type ScoreOf[L ScoreLevel] struct {
	// Instructions is what the model should rate. See [Noul.Instructions].
	Instructions any `json:"instructions,omitempty"`

	// Criteria describes each score level. A description's index is its level,
	// counting from zero. Between 1 and 10 levels are required, none of them
	// null.
	Criteria []any `json:"criteria"`
}

// Score is a [ScoreOf] with plain int levels.
type Score = ScoreOf[int]

func (ScoreOf[L]) kind() string { return kindScore }

func (s ScoreOf[L]) validate() error {
	switch {
	case len(s.Criteria) == 0:
		return errors.New("has no criteria; at least one score is required")
	case len(s.Criteria) > maxScoreLevels:
		return fmt.Errorf("has %d levels; the API accepts at most %d", len(s.Criteria), maxScoreLevels)
	}
	for level, description := range s.Criteria {
		if isJSONNull(description) {
			return fmt.Errorf("leaves level %d null; every level needs a description", level)
		}
	}
	return nil
}

// MarshalJSON encodes the question with its "score" type discriminator.
func (s ScoreOf[L]) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		Instructions any    `json:"instructions,omitempty"`
		Criteria     []any  `json:"criteria"`
	}{kindScore, s.Instructions, s.Criteria})
}

// Levels builds [Score] criteria from one plain description per level, so a
// rubric of short phrases reads as one:
//
//	jev.Score{
//	    Instructions: "How urgent is this ticket?",
//	    Criteria:     jev.Levels("can wait", "this week", "today"),
//	}
//
// Order is the meaning here, unlike [Labels]: a description's position is its
// score, counting from zero. Write the slice out to describe a level with
// something richer than a string.
func Levels(descriptions ...string) []any {
	criteria := make([]any, len(descriptions))
	for i, description := range descriptions {
		criteria[i] = description
	}
	return criteria
}

// RawQuestion is sent verbatim, including its "type" key. It is the escape
// hatch for question kinds or fields the API accepts before this SDK models
// them; the API, not the SDK, validates its contents.
type RawQuestion map[string]any

// kind is whatever the caller put in "type", including one this SDK version
// does not model.
func (q RawQuestion) kind() string {
	kind, _ := q["type"].(string)
	return kind
}

// validate checks only the structure every question needs, leaving the rest to
// the API, which is the point of a raw question.
func (q RawQuestion) validate() error {
	kind := q.kind()
	if kind == "" {
		return fmt.Errorf("needs a nonempty string %q", "type")
	}
	if kind != kindChoice && kind != kindScore {
		return nil
	}

	criteria, ok := q["criteria"]
	if !ok {
		return fmt.Errorf("requires %q", "criteria")
	}
	if kind == kindScore {
		if levels, ok := criteria.([]any); ok && len(levels) == 0 {
			return errors.New("has no criteria; at least one score is required")
		}
	}
	return nil
}

// questionKind reports the wire "type" a question asks for, or "" for a nil
// question.
func questionKind(question Question) string {
	if isNilQuestion(question) {
		return ""
	}
	return question.kind()
}

// validateQuestions rejects question maps the API is certain to refuse, so the
// caller gets a local error instead of a round trip.
//
// Every question is checked and the failure with the lowest name is reported,
// which keeps the error independent of map iteration order without paying for
// a sort on the path where nothing fails.
func validateQuestions(questions map[string]Question) error {
	if len(questions) == 0 {
		return ErrNoQuestions
	}

	var (
		failed    bool
		firstName string
		firstErr  error
	)
	for name, question := range questions {
		if failed && name >= firstName {
			continue
		}
		if err := validateQuestion(name, question); err != nil {
			failed, firstName, firstErr = true, name, err
		}
	}
	return firstErr
}

func validateQuestion(name string, question Question) error {
	if name == "" {
		return fmt.Errorf("%w: a question name must not be empty; it is the key its answer comes back under", ErrInvalidQuestion)
	}
	if isNilQuestion(question) {
		return fmt.Errorf("%w: question %q is nil", ErrInvalidQuestion, name)
	}
	if err := question.validate(); err != nil {
		return fmt.Errorf("%w: question %q %w", ErrInvalidQuestion, name, err)
	}
	return nil
}

// isNilQuestion also catches a nil pointer to a question, which would
// otherwise panic the moment one of its methods read a field.
func isNilQuestion(question Question) bool {
	if question == nil {
		return true
	}
	value := reflect.ValueOf(question)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

// isJSONNull reports whether v encodes to JSON null. The common cases are
// answered without encoding anything.
func isJSONNull(v any) bool {
	switch v.(type) {
	case nil:
		return true
	case string:
		return false
	}
	encoded, err := json.Marshal(v)
	// An unencodable value is not null; encoding the request reports it.
	return err == nil && string(encoded) == "null"
}

// isJSONEmpty reports whether v encodes to null, "", {} or []: a value that
// asks nothing.
func isJSONEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return false
	}
	switch string(encoded) {
	case "null", `""`, "{}", "[]":
		return true
	}
	return false
}
