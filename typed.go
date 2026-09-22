package jev

import (
	"errors"
	"strconv"
)

// Failures reading a typed answer that are only visible against the question
// that was asked. Like the other decoding failures they arrive wrapped in a
// [*ResponseError] naming the field.
var (
	errNotOffered    = errors.New("not among the choices the question offered")
	errOutsideRubric = errors.New("outside the levels the question's rubric defines")
)

// TypedQuestion is a question whose answer reads back as A. [Noul], [ChoiceOf]
// and [ScoreOf] implement it, reading back as [NoulAnswer], [ChoiceAnswerOf]
// and [ScoreAnswerOf] respectively. Pass one to [Ask].
type TypedQuestion[A any] interface {
	Question

	// decodeAnswer converts the answer the API returned into A, checking it
	// against what this question offered. It reports the offending field,
	// relative to the answer, when the two disagree.
	decodeAnswer(Answer) (A, string, error)
}

// Key is a typed handle on the answer to a question registered with [Ask]. Its
// zero value reads nothing: [Key.Answer] fails.
type Key[A any] struct {
	name   string
	decode func(Answer) (A, string, error)
}

// Ask registers question under name in req and returns a handle typed by the
// answer the question reads back as. Reading it needs no type assertion, and a
// [ChoiceOf] or [ScoreOf] answer comes back in the caller's own label or level
// type, so the compiler checks what is done with it:
//
//	req := jev.SystemOneRequest{State: ticket}
//	urgent := jev.Ask(&req, "urgent", jev.Noul{Instructions: "Is this urgent?"})
//	team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{Criteria: jev.LabelsOf(Billing, Technical)})
//
//	resp, err := client.SystemOne(ctx, req)
//	// ...
//	t, err := team.Answer(resp) // t.Choice is a Team
//
// A name already registered is replaced. A is inferred from question, which
// must therefore be a concrete question, not a [Question] interface value.
func Ask[A any](req *SystemOneRequest, name string, question TypedQuestion[A]) Key[A] {
	if req.Questions == nil {
		req.Questions = make(map[string]Question)
	}
	req.Questions[name] = question
	return Key[A]{name: name, decode: question.decodeAnswer}
}

// Name is the question's name: the key its answer comes back under.
func (k Key[A]) Name() string { return k.name }

// Answer reads this question's answer out of resp.
//
// It fails with a [*ResponseError] naming the field when the answer is absent,
// of another kind, or disagrees with the question: a choice the question never
// offered, or a level outside its rubric. [Client.SystemOne] already rejects a
// response that leaves a question unanswered or answered in the wrong kind, so
// against its result only the disagreements can surface here.
func (k Key[A]) Answer(resp *SystemOneResponse) (A, error) {
	var zero A
	if k.decode == nil {
		return zero, errors.New("jev: Answer called on a zero Key; use the Key returned by Ask")
	}
	if resp == nil {
		return zero, errors.New("jev: Answer called with a nil response")
	}

	answer, ok := resp.Answers[k.name]
	if !ok {
		return zero, resp.invalid("", "answers."+k.name, errMissingAnswer)
	}

	decoded, field, err := k.decode(answer)
	if err != nil {
		return zero, resp.invalid("", joinPath("answers."+k.name, field), err)
	}
	return decoded, nil
}

func (Noul) decodeAnswer(answer Answer) (NoulAnswer, string, error) {
	noul, ok := answer.(NoulAnswer)
	if !ok {
		return NoulAnswer{}, "type", errAnswerKindMismatch
	}
	return noul, "", nil
}

// decodeAnswer re-keys the answer by T, refusing a label the question did not
// offer: converting it anyway would yield a T that is none of the caller's
// constants, which is exactly what the typed form exists to rule out.
func (c ChoiceOf[T]) decodeAnswer(answer Answer) (ChoiceAnswerOf[T], string, error) {
	plain, ok := answer.(ChoiceAnswer)
	if !ok {
		return ChoiceAnswerOf[T]{}, "type", errAnswerKindMismatch
	}

	choice := T(plain.Choice)
	if _, offered := c.Criteria[choice]; !offered {
		return ChoiceAnswerOf[T]{}, "choice", errNotOffered
	}

	probabilities := make(map[T]float64, len(plain.Probabilities))

	var (
		failed bool
		bad    string
	)
	for label, probability := range plain.Probabilities {
		if _, offered := c.Criteria[T(label)]; !offered {
			if !failed || label < bad {
				failed, bad = true, label
			}
			continue
		}
		probabilities[T(label)] = probability
	}
	if failed {
		return ChoiceAnswerOf[T]{}, "probabilities." + bad, errNotOffered
	}

	return ChoiceAnswerOf[T]{Choice: choice, Confidence: plain.Confidence, Probabilities: probabilities}, "", nil
}

// decodeAnswer re-keys the answer by L, refusing a level outside the rubric
// the question sent.
func (s ScoreOf[L]) decodeAnswer(answer Answer) (ScoreAnswerOf[L], string, error) {
	plain, ok := answer.(ScoreAnswer)
	if !ok {
		return ScoreAnswerOf[L]{}, "type", errAnswerKindMismatch
	}

	legend, field, err := rekeyLevels[L](plain.Legend, len(s.Criteria), "legend")
	if err != nil {
		return ScoreAnswerOf[L]{}, field, err
	}
	probabilities, field, err := rekeyLevels[L](plain.Probabilities, len(s.Criteria), "probabilities")
	if err != nil {
		return ScoreAnswerOf[L]{}, field, err
	}

	return ScoreAnswerOf[L]{Score: plain.Score, Confidence: plain.Confidence, Legend: legend, Probabilities: probabilities}, "", nil
}

// rekeyLevels converts a map keyed by int level to one keyed by L, reporting
// the lowest level outside 0 through count-1. Every level in range fits any
// ScoreLevel type, since a rubric has at most ten.
func rekeyLevels[L ScoreLevel, V any](levels map[int]V, count int, field string) (map[L]V, string, error) {
	rekeyed := make(map[L]V, len(levels))

	var (
		failed bool
		bad    int
	)
	for level, value := range levels {
		if level < 0 || level >= count {
			if !failed || level < bad {
				failed, bad = true, level
			}
			continue
		}
		rekeyed[L(level)] = value
	}

	if failed {
		return nil, field + "." + strconv.Itoa(bad), errOutsideRubric
	}
	return rekeyed, "", nil
}
