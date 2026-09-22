package jev

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
)

func TestRanked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		probabilities map[string]float64
		want          []string
	}{
		{"by probability", map[string]float64{"calm": 0.1, "angry": 0.7, "sad": 0.2}, []string{"angry", "sad", "calm"}},
		// Ties keep a lexical order, so the result never depends on map order.
		{"ties are lexical", map[string]float64{"b": 0.4, "a": 0.4, "c": 0.2}, []string{"a", "b", "c"}},
		{"one label", map[string]float64{"only": 1}, []string{"only"}},
		{"none", nil, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for range 10 {
				got := ChoiceAnswer{Probabilities: tt.probabilities}.Ranked()
				if !slices.Equal(got, tt.want) {
					t.Fatalf("Ranked() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestRankedTyped(t *testing.T) {
	t.Parallel()

	answer := ChoiceAnswerOf[Team]{Probabilities: map[Team]float64{Billing: 0.2, Technical: 0.8}}
	if got, want := answer.Ranked(), []Team{Technical, Billing}; !reflect.DeepEqual(got, want) {
		t.Errorf("Ranked() = %v, want %v", got, want)
	}
}

func TestLevel(t *testing.T) {
	t.Parallel()

	threeLevels := map[int]any{0: "low", 1: "mid", 2: "high"}

	tests := []struct {
		name   string
		answer ScoreAnswer
		want   int
	}{
		{"exact", ScoreAnswer{Score: 1, Legend: threeLevels}, 1},
		{"rounds down", ScoreAnswer{Score: 1.4, Legend: threeLevels}, 1},
		{"rounds half up", ScoreAnswer{Score: 1.5, Legend: threeLevels}, 2},
		{"rounds up", ScoreAnswer{Score: 1.7, Legend: threeLevels}, 2},
		{"clamped above", ScoreAnswer{Score: 9, Legend: threeLevels}, 2},
		{"clamped below", ScoreAnswer{Score: -3, Legend: threeLevels}, 0},
		{"nan takes the lowest", ScoreAnswer{Score: math.NaN(), Legend: threeLevels}, 0},
		{"falls back to probabilities", ScoreAnswer{Score: 7, Probabilities: map[int]float64{0: 0.5, 1: 0.5}}, 1},
		{"no rubric known", ScoreAnswer{Score: 2.6}, 3},
		{"no rubric known, negative", ScoreAnswer{Score: -1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.answer.Level(); got != tt.want {
				t.Errorf("Level() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLevelAndDescriptionTyped(t *testing.T) {
	t.Parallel()

	answer := ScoreAnswerOf[Urgency]{
		Score:  1.8,
		Legend: map[Urgency]any{CanWait: "can wait", ThisWeek: "this week", Today: "today"},
	}

	if got := answer.Level(); got != Today {
		t.Errorf("Level() = %v, want Today", got)
	}
	if got := answer.Description(); got != "today" {
		t.Errorf("Description() = %v, want today", got)
	}
}

func TestDecodeAnswerTolerance(t *testing.T) {
	t.Parallel()

	// A mistyped field the kind does not use is ignored, as an unknown one is:
	// a later API may reuse a name with a different shape on another kind.
	body := `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":0.4,"score":"not a number","legend":[1]}}}`

	got, err := decodeSystemOne(meta(body), nil, "")
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v, want the irrelevant fields tolerated", err)
	}
	if got.Nouls["n"].Noul != 0.4 {
		t.Errorf("Nouls[n] = %+v, want 0.4", got.Nouls["n"])
	}
}

func TestDecodeAnswerMaskedFieldError(t *testing.T) {
	t.Parallel()

	// encoding/json reports only the first mistyped field and stores a zero in
	// its place. Here an irrelevant field fails first; the relevant one behind
	// it must still be reported rather than accepted as a zero probability.
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"choice probability masked by a score field",
			`{"model":"m","usage":{},"answers":{"c":{"type":"choice","score":"x","choice":"a","confidence":0.5,"probabilities":{"a":"bad"}}}}`,
			"answers.c.probabilities.a",
		},
		{
			"noul value masked by a legend field",
			`{"model":"m","usage":{},"answers":{"n":{"type":"noul","legend":[],"noul":"0.5"}}}`,
			"answers.n.noul",
		},
		{
			"score confidence masked by a choice field",
			`{"model":"m","usage":{},"answers":{"s":{"type":"score","choice":1,"score":1,"confidence":"high","legend":{"0":"a"},"probabilities":{"0":1}}}}`,
			"answers.s.confidence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeSystemOne(meta(tt.body), nil, "")

			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("decodeSystemOne() error = %v, want *ResponseError", err)
			}
			if responseErr.Field != tt.want {
				t.Errorf("Field = %q, want %q", responseErr.Field, tt.want)
			}
		})
	}
}

func TestDecodeAnswerMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		// A mistyped value is stored as a zero by encoding/json, so these must be
		// caught from the recorded error, not from a nil pointer.
		{"mistyped noul", `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":"0.5"}}}`, "answers.n.noul"},
		{"mistyped probability", `{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":"x"}}}}`, "answers.c.probabilities.a"},
		{"empty type", `{"model":"m","usage":{},"answers":{"c":{"type":""}}}`, "answers.c.type"},
		{"null type", `{"model":"m","usage":{},"answers":{"c":{"type":null}}}`, "answers.c.type"},
		{"object type", `{"model":"m","usage":{},"answers":{"c":{"type":{}}}}`, "answers.c.type"},
		{"score legend key not a level", `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":{"":"a"},"probabilities":{"0":1}}}}`, "answers.s.legend."},
		{"score probability key not a level", `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":{"0":"a"},"probabilities":{"top":1}}}}`, "answers.s.probabilities.top"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeSystemOne(meta(tt.body), nil, "")

			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("decodeSystemOne() error = %v, want *ResponseError", err)
			}
			if responseErr.Field != tt.want {
				t.Errorf("Field = %q, want %q", responseErr.Field, tt.want)
			}
		})
	}
}

func TestUnknownAnswerKeepsOnlyWhatItNeeds(t *testing.T) {
	t.Parallel()

	body := `{"model":"m","usage":{},"answers":{"x":{"type":"aurora","value":3},"n":{"type":"noul","noul":0.5}}}`

	got, err := decodeSystemOne(meta(body), nil, "")
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	unknown, ok := got.Answers["x"].(UnknownAnswer)
	if !ok || unknown.Type != "aurora" || string(unknown.Raw) != `{"type":"aurora","value":3}` {
		t.Errorf("Answers[x] = %#v, want the unmodeled answer as sent", got.Answers["x"])
	}
	// A modeled answer is not also kept as bytes.
	if _, ok := got.Answers["n"].(NoulAnswer); !ok {
		t.Errorf("Answers[n] = %#v, want a NoulAnswer", got.Answers["n"])
	}
	// An unknown answer belongs to no typed view.
	if len(got.Nouls) != 1 || len(got.Choices) != 0 || len(got.Scores) != 0 {
		t.Errorf("typed views = %d/%d/%d, want 1/0/0", len(got.Nouls), len(got.Choices), len(got.Scores))
	}
}
