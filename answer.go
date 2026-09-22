package jev

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Answer is a single answer to a named question. It is a sealed union: only
// [NoulAnswer], [ChoiceAnswerOf], [ScoreAnswerOf] and [UnknownAnswer]
// implement it. Recover the concrete answer with a type switch, read it through
// a [Key], or reach for one of the typed maps on [SystemOneResponse].
type Answer interface {
	answer()
}

// NoulAnswer is a yes/no answer.
//
// See https://docs.typesafe.ai/primitives/noul.
type NoulAnswer struct {
	// Noul is the probability of a yes answer, from 0 to 1. Values near 0.5
	// mean the model is unsure. The threshold to act on is yours: it depends on
	// what a wrong yes and a wrong no each cost.
	Noul float64 `json:"noul"`
}

func (NoulAnswer) answer() {}

// ChoiceAnswerOf is a selected label and the probabilities of the
// alternatives, with labels of the caller's own string type.
//
// See https://docs.typesafe.ai/primitives/choice.
type ChoiceAnswerOf[T ~string] struct {
	// Choice is the label with the highest probability.
	Choice T `json:"choice"`

	// Confidence in the selection, from 0 to 1. It is the API's own measure,
	// not the probability of Choice.
	Confidence float64 `json:"confidence"`

	// Probabilities holds every label's probability. They sum to about 1.
	Probabilities map[T]float64 `json:"probabilities"`
}

// ChoiceAnswer is a [ChoiceAnswerOf] with plain string labels.
type ChoiceAnswer = ChoiceAnswerOf[string]

func (ChoiceAnswerOf[T]) answer() {}

// Ranked returns the labels from most to least probable, so the runner-up is
// at index 1: worth a look before acting on a low-confidence choice. Labels of
// equal probability keep a stable, lexical order.
func (a ChoiceAnswerOf[T]) Ranked() []T {
	labels := slices.Collect(maps.Keys(a.Probabilities))
	slices.SortFunc(labels, func(x, y T) int {
		if byProbability := cmp.Compare(a.Probabilities[y], a.Probabilities[x]); byProbability != 0 {
			return byProbability
		}
		return cmp.Compare(x, y)
	})
	return labels
}

// ScoreAnswerOf is an expected score with the rubric it was scored against,
// with levels of the caller's own integer type.
//
// See https://docs.typesafe.ai/primitives/score.
type ScoreAnswerOf[L ScoreLevel] struct {
	// Score is the probability-weighted average of the rubric levels, so it
	// may fall between them. See [ScoreAnswerOf.Level] for the level it rounds
	// to.
	Score float64 `json:"score"`

	// Confidence in the score, from 0 to 1.
	Confidence float64 `json:"confidence"`

	// Legend maps each level to the criterion it was given for.
	Legend map[L]any `json:"legend"`

	// Probabilities holds each level's probability. They sum to about 1.
	Probabilities map[L]float64 `json:"probabilities"`
}

// ScoreAnswer is a [ScoreAnswerOf] with plain int levels.
type ScoreAnswer = ScoreAnswerOf[int]

func (ScoreAnswerOf[L]) answer() {}

// Level returns the rubric level Score rounds to, clamped to the levels the
// answer reports, so it is always one the question offered.
func (a ScoreAnswerOf[L]) Level() L {
	levels := a.Legend
	if len(levels) == 0 {
		// Without a legend the probabilities are the only record of the rubric.
		return clampLevel(a.Score, slices.Collect(maps.Keys(a.Probabilities)))
	}
	return clampLevel(a.Score, slices.Collect(maps.Keys(levels)))
}

// Description returns what the rubric says about [ScoreAnswerOf.Level].
func (a ScoreAnswerOf[L]) Description() any {
	return a.Legend[a.Level()]
}

// clampLevel rounds score to the nearest level, kept within the known levels.
// With none known it is kept from going below zero only.
func clampLevel[L ScoreLevel](score float64, levels []L) L {
	rounded := math.Round(score)
	if len(levels) == 0 {
		if !(rounded > 0) { // Also catches NaN.
			return 0
		}
		return L(rounded)
	}

	lowest, highest := slices.Min(levels), slices.Max(levels)
	switch {
	case !(rounded > float64(lowest)): // Also catches NaN.
		return lowest
	case rounded > float64(highest):
		return highest
	}
	return L(rounded)
}

// UnknownAnswer is an answer of a kind this SDK version does not model, kept
// rather than dropped so a [RawQuestion] works end to end: decode Raw yourself.
type UnknownAnswer struct {
	// Type is the answer's wire discriminator.
	Type string

	// Raw is the answer exactly as the API sent it.
	Raw json.RawMessage
}

func (UnknownAnswer) answer() {}

// Usage reports the tokens a request consumed. A field is nil when the API did
// not report it.
type Usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

// answerWire is one answer as sent, decoded in a single pass whatever its kind.
// Required scalars are pointers so an absent field is distinguishable from a
// zero one: encoding/json zero-fills, which would silently turn a missing
// confidence into 0.0.
type answerWire struct {
	Type          *string            `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Legend        map[string]any     `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`

	raw []byte // kept for a kind this SDK does not model, or a malformed answer
	err error  // the first field of the wrong JSON type, if any
}

// UnmarshalJSON records a field of the wrong type instead of returning it, so
// one malformed answer neither stops its siblings from decoding nor masks
// their errors, and so the error can be judged against the fields the answer's
// kind actually uses.
func (w *answerWire) UnmarshalJSON(data []byte) error {
	type fields answerWire // Sheds this method, so decoding does not recurse.
	w.err = json.Unmarshal(data, (*fields)(w))

	// The bytes are kept only where they may be needed again: to hand an
	// unmodeled answer back, or to re-read a malformed one field by field.
	if w.err != nil || (w.typed() && !isModeledKind(*w.Type)) {
		w.raw = bytes.Clone(data)
	}
	return nil
}

// typed reports whether the answer carries a usable discriminator. A pointer
// alone does not prove it: encoding/json allocates the field before finding
// the value is not a string, so a mistyped "type" arrives as a pointer to "".
func (w *answerWire) typed() bool {
	return w.Type != nil && *w.Type != ""
}

func isModeledKind(kind string) bool {
	return kind == kindNoul || kind == kindChoice || kind == kindScore
}

// decode converts the answer into its concrete type. It reports the dotted
// path of the first field that is missing or of the wrong shape, relative to
// the answer itself.
func (w *answerWire) decode() (Answer, string, error) {
	switch *w.Type {
	case kindNoul:
		if field, err := w.fieldError("noul"); err != nil {
			return nil, field, err
		}
		if w.Noul == nil {
			return nil, "noul", errMissingField
		}
		return NoulAnswer{Noul: *w.Noul}, "", nil

	case kindChoice:
		if field, err := w.fieldError("choice", "confidence", "probabilities"); err != nil {
			return nil, field, err
		}
		switch {
		case w.Choice == nil:
			return nil, "choice", errMissingField
		case w.Confidence == nil:
			return nil, "confidence", errMissingField
		case w.Probabilities == nil:
			return nil, "probabilities", errMissingField
		}
		return ChoiceAnswer{Choice: *w.Choice, Confidence: *w.Confidence, Probabilities: w.Probabilities}, "", nil

	case kindScore:
		if field, err := w.fieldError("score", "confidence", "legend", "probabilities"); err != nil {
			return nil, field, err
		}
		switch {
		case w.Score == nil:
			return nil, "score", errMissingField
		case w.Confidence == nil:
			return nil, "confidence", errMissingField
		case w.Legend == nil:
			return nil, "legend", errMissingField
		case w.Probabilities == nil:
			return nil, "probabilities", errMissingField
		}

		legend, field, err := levelKeys(w.Legend, "legend")
		if err != nil {
			return nil, field, err
		}
		probabilities, field, err := levelKeys(w.Probabilities, "probabilities")
		if err != nil {
			return nil, field, err
		}
		return ScoreAnswer{Score: *w.Score, Confidence: *w.Confidence, Legend: legend, Probabilities: probabilities}, "", nil
	}

	return UnknownAnswer{Type: *w.Type, Raw: w.raw}, "", nil
}

// fieldError reports the path of a field of the wrong JSON type among fields,
// the ones this answer's kind uses. A mistyped field the kind does not use is
// tolerated, as any unknown field is.
//
// encoding/json reports only the first mistyped field and stores a zero in its
// place. So when that first field is one the kind ignores, the answer is
// re-read field by field: a relevant field it masked would otherwise pass as a
// zero, the very failure the pointer fields exist to prevent.
func (w *answerWire) fieldError(fields ...string) (string, error) {
	if w.err == nil {
		return "", nil
	}

	var typeErr *json.UnmarshalTypeError
	if !errors.As(w.err, &typeErr) {
		return "", w.err
	}

	head, _, _ := strings.Cut(typeErr.Field, ".")
	if slices.Contains(fields, head) {
		return typeErr.Field, w.err
	}
	return w.recheck(fields)
}

// recheck decodes each of fields on its own, in order, and reports the first
// that fails. It runs only for an answer already known to be malformed.
func (w *answerWire) recheck(fields []string) (string, error) {
	var byField map[string]json.RawMessage
	if err := json.Unmarshal(w.raw, &byField); err != nil {
		return "", err
	}

	for _, field := range fields {
		raw, ok := byField[field]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, fieldTarget(field)); err != nil {
			return joinPath(field, jsonFieldPath(err)), err
		}
	}
	return "", nil
}

// fieldTarget is a value of the Go type answerWire decodes field into.
func fieldTarget(field string) any {
	switch field {
	case "choice":
		return new(string)
	case "legend":
		return new(map[string]any)
	case "probabilities":
		return new(map[string]float64)
	}
	return new(float64) // noul, score and confidence
}

// levelKeys converts a map keyed by level numbers written as strings, reporting
// the lowest key that is not an integer as field.key.
func levelKeys[V any](m map[string]V, field string) (map[int]V, string, error) {
	levels := make(map[int]V, len(m))

	var (
		failed bool
		bad    string
	)
	for key, value := range m {
		level, err := strconv.Atoi(key)
		if err != nil {
			if !failed || key < bad {
				failed, bad = true, key
			}
			continue
		}
		levels[level] = value
	}

	if failed {
		return nil, field + "." + bad, errMissingField
	}
	return levels, "", nil
}

// partition splits the answers into the per-kind views on [SystemOneResponse].
func partition(answers map[string]Answer) (nouls map[string]NoulAnswer, choices map[string]ChoiceAnswer, scores map[string]ScoreAnswer) {
	nouls = map[string]NoulAnswer{}
	choices = map[string]ChoiceAnswer{}
	scores = map[string]ScoreAnswer{}

	for name, answer := range answers {
		switch answer := answer.(type) {
		case NoulAnswer:
			nouls[name] = answer
		case ChoiceAnswer:
			choices[name] = answer
		case ScoreAnswer:
			scores[name] = answer
		}
	}
	return nouls, choices, scores
}
