package jev

import (
	"encoding/json"
	"errors"
	"log/slog"
)

// Answer is a single answer to a named question. It is a sealed union: only
// [NoulAnswer], [ChoiceAnswer] and [ScoreAnswer] implement it. Recover the
// concrete answer with a type switch, or reach for one of the typed maps on
// [SystemOneResponse].
type Answer interface {
	answer()
}

// NoulAnswer is a yes/no answer.
//
// See https://docs.typesafe.ai/primitives/noul.
type NoulAnswer struct {
	// Noul is the probability of a yes answer, from 0 to 1. Values near 0.5
	// mean the model is unsure.
	Noul float64 `json:"noul"`
}

func (NoulAnswer) answer() {}

// ChoiceAnswer is a selected label and the probabilities of the alternatives.
//
// See https://docs.typesafe.ai/primitives/choice.
type ChoiceAnswer struct {
	// Choice is the label with the highest probability.
	Choice string `json:"choice"`

	// Confidence in the selection, from 0 to 1.
	Confidence float64 `json:"confidence"`

	// Probabilities holds every label's probability. They sum to about 1.
	Probabilities map[string]float64 `json:"probabilities"`
}

func (ChoiceAnswer) answer() {}

// ScoreAnswer is an expected score with the rubric it was scored against.
//
// See https://docs.typesafe.ai/primitives/score.
type ScoreAnswer struct {
	// Score is the probability-weighted average of the rubric levels, so it
	// may fall between them.
	Score float64 `json:"score"`

	// Confidence in the score, from 0 to 1.
	Confidence float64 `json:"confidence"`

	// Legend maps each score level to the criterion it was given for.
	Legend map[int]any `json:"legend"`

	// Probabilities holds each score level's probability. They sum to about 1.
	Probabilities map[int]float64 `json:"probabilities"`
}

func (ScoreAnswer) answer() {}

// Usage reports the tokens a request consumed. A field is nil when the API did
// not report it.
type Usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

// Wire shapes for decoding. Required scalars are pointers so an absent field is
// distinguishable from a zero one: encoding/json zero-fills, which would
// silently turn a missing confidence into 0.0.

type noulWire struct {
	Noul *float64 `json:"noul"`
}

type choiceWire struct {
	Choice        *string             `json:"choice"`
	Confidence    *float64            `json:"confidence"`
	Probabilities *map[string]float64 `json:"probabilities"`
}

type scoreWire struct {
	Score         *float64         `json:"score"`
	Confidence    *float64         `json:"confidence"`
	Legend        *map[int]any     `json:"legend"`
	Probabilities *map[int]float64 `json:"probabilities"`
}

// decodeAnswer converts one raw answer into its concrete type. It reports the
// dotted path of the first field that is missing or of the wrong shape,
// relative to the answer itself.
func decodeAnswer(kind string, raw []byte) (Answer, string, error) {
	switch kind {
	case kindNoul:
		var wire noulWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, jsonFieldPath(err), err
		}
		if wire.Noul == nil {
			return nil, "noul", errMissingField
		}
		return NoulAnswer{Noul: *wire.Noul}, "", nil

	case kindChoice:
		var wire choiceWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, jsonFieldPath(err), err
		}
		switch {
		case wire.Choice == nil:
			return nil, "choice", errMissingField
		case wire.Confidence == nil:
			return nil, "confidence", errMissingField
		case wire.Probabilities == nil:
			return nil, "probabilities", errMissingField
		}
		return ChoiceAnswer{
			Choice:        *wire.Choice,
			Confidence:    *wire.Confidence,
			Probabilities: *wire.Probabilities,
		}, "", nil

	case kindScore:
		var wire scoreWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, jsonFieldPath(err), err
		}
		switch {
		case wire.Score == nil:
			return nil, "score", errMissingField
		case wire.Confidence == nil:
			return nil, "confidence", errMissingField
		case wire.Legend == nil:
			return nil, "legend", errMissingField
		case wire.Probabilities == nil:
			return nil, "probabilities", errMissingField
		}
		return ScoreAnswer{
			Score:         *wire.Score,
			Confidence:    *wire.Confidence,
			Legend:        *wire.Legend,
			Probabilities: *wire.Probabilities,
		}, "", nil
	}

	return nil, "", errUnknownAnswerKind
}

// answerKind reads the discriminator off a raw answer.
func answerKind(raw []byte) (string, bool) {
	var tagged struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &tagged); err != nil || tagged.Type == nil {
		return "", false
	}
	return *tagged.Type, true
}

// answerKinds reads every answer's discriminator up front, so a malformed
// answer is reported before any other answer's fields are decoded. Names are
// visited in sorted order, keeping the reported path independent of map
// iteration.
func answerKinds(raw map[string]json.RawMessage) (map[string]string, string, error) {
	kinds := make(map[string]string, len(raw))
	for _, name := range sortedKeys(raw) {
		kind, ok := answerKind(raw[name])
		if !ok {
			return nil, "answers." + name + ".type", errMissingField
		}
		kinds[name] = kind
	}
	return kinds, "", nil
}

// decodeAnswers converts the answers object, dropping kinds this SDK version
// does not model so an answer type added later does not fail the whole
// response.
func decodeAnswers(raw map[string]json.RawMessage, kinds map[string]string, logger *slog.Logger) (map[string]Answer, string, error) {
	answers := make(map[string]Answer, len(raw))
	for _, name := range sortedKeys(raw) {
		decoded, field, err := decodeAnswer(kinds[name], raw[name])
		switch {
		case errors.Is(err, errUnknownAnswerKind):
			// Forward compatibility: the raw payload is still on the response.
			logger.Warn("jev: ignoring answer of unrecognized type", "answer", name, "type", kinds[name])
			continue
		case err != nil:
			return nil, joinPath("answers."+name, field), err
		}
		answers[name] = decoded
	}
	return answers, "", nil
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
