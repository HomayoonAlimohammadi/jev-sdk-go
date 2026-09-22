package jev

import (
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func meta(body string) ResponseMeta {
	return ResponseMeta{
		RequestID:  "req-123",
		StatusCode: http.StatusOK,
		Header:     header("X-Typesafe-Request-Id", "req-123"),
		Raw:        []byte(body),
	}
}

const systemOneBody = `{
  "model": "jev-latest",
  "usage": {"input_tokens": 12, "output_tokens": 3},
  "answers": {
    "spam": {"type": "noul", "noul": 0.98},
    "tone": {"type": "choice", "choice": "friendly", "confidence": 0.9, "probabilities": {"friendly": 0.9, "hostile": 0.1}},
    "quality": {"type": "score", "score": 1.7, "confidence": 0.8,
                "legend": {"0": "bad", "1": "ok", "2": "great"},
                "probabilities": {"0": 0.1, "1": 0.1, "2": 0.8}}
  }
}`

func TestDecodeSystemOne(t *testing.T) {
	t.Parallel()

	got, err := decodeSystemOne(meta(systemOneBody), nil, "POST https://x/v1/systemone", discard())
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	if got.Model != "jev-latest" {
		t.Errorf("Model = %q, want jev-latest", got.Model)
	}
	if got.Usage.InputTokens == nil || *got.Usage.InputTokens != 12 {
		t.Errorf("Usage.InputTokens = %v, want 12", got.Usage.InputTokens)
	}
	if got.RequestID != "req-123" || got.StatusCode != http.StatusOK {
		t.Errorf("ResponseMeta = %+v, want it populated", got.ResponseMeta)
	}

	if want := (NoulAnswer{Noul: 0.98}); got.Nouls["spam"] != want {
		t.Errorf("Nouls[spam] = %+v, want %+v", got.Nouls["spam"], want)
	}
	if got.Choices["tone"].Choice != "friendly" || got.Choices["tone"].Confidence != 0.9 {
		t.Errorf("Choices[tone] = %+v", got.Choices["tone"])
	}
	if want := map[string]float64{"friendly": 0.9, "hostile": 0.1}; !reflect.DeepEqual(got.Choices["tone"].Probabilities, want) {
		t.Errorf("Choices[tone].Probabilities = %v, want %v", got.Choices["tone"].Probabilities, want)
	}

	// Integer-keyed rubric maps: the wire sends string keys, the SDK hands back ints.
	score := got.Scores["quality"]
	if score.Score != 1.7 || score.Confidence != 0.8 {
		t.Errorf("Scores[quality] = %+v", score)
	}
	if want := (map[int]any{0: "bad", 1: "ok", 2: "great"}); !reflect.DeepEqual(score.Legend, want) {
		t.Errorf("Legend = %v, want %v", score.Legend, want)
	}
	if want := (map[int]float64{0: 0.1, 1: 0.1, 2: 0.8}); !reflect.DeepEqual(score.Probabilities, want) {
		t.Errorf("Probabilities = %v, want %v", score.Probabilities, want)
	}

	if len(got.Answers) != 3 {
		t.Errorf("Answers has %d entries, want 3", len(got.Answers))
	}
	if got.Answers["spam"] != Answer(NoulAnswer{Noul: 0.98}) {
		t.Errorf("Answers[spam] = %+v, want it to match Nouls[spam]", got.Answers["spam"])
	}
}

func TestDecodeSystemOneInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty body", ``, ""},
		{"not an object", `[1,2]`, ""},
		{"null", `null`, "model"},
		{"missing model", `{"usage":{},"answers":{}}`, "model"},
		{"missing usage", `{"model":"m","answers":{}}`, "usage"},

		{"answer not an object", `{"model":"m","usage":{},"answers":{"c":"not-a-mapping"}}`, "answers.c.type"},
		{"answer without type", `{"model":"m","usage":{},"answers":{"c":{"noul":0.5}}}`, "answers.c.type"},
		{"answer type not a string", `{"model":"m","usage":{},"answers":{"c":{"type":1}}}`, "answers.c.type"},

		{"noul missing", `{"model":"m","usage":{},"answers":{"n":{"type":"noul"}}}`, "answers.n.noul"},
		{"noul wrong type", `{"model":"m","usage":{},"answers":{"n":{"type":"noul","noul":"0.5"}}}`, "answers.n.noul"},

		{"choice missing confidence", `{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"a","probabilities":{}}}}`, "answers.c.confidence"},
		{"choice missing choice", `{"model":"m","usage":{},"answers":{"c":{"type":"choice","confidence":0.5,"probabilities":{}}}}`, "answers.c.choice"},
		{"choice missing probabilities", `{"model":"m","usage":{},"answers":{"c":{"type":"choice","choice":"a","confidence":0.5}}}`, "answers.c.probabilities"},

		{"score legend wrong shape", `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":[],"probabilities":{}}}}`, "answers.s.legend"},
		{"score legend bad key", `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"legend":{"x":"bad"},"probabilities":{}}}}`, "answers.s.legend.x"},
		{"score missing legend", `{"model":"m","usage":{},"answers":{"s":{"type":"score","score":1,"confidence":1,"probabilities":{}}}}`, "answers.s.legend"},

		// A malformed answer is reported ahead of a field problem in another one.
		{
			"structural check wins",
			`{"model":"m","usage":{},"answers":{"a":{"type":"noul"},"z":"not-a-mapping"}}`,
			"answers.z.type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeSystemOne(meta(tt.body), nil, "POST https://x/v1/systemone", discard())

			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("decodeSystemOne() error = %v, want *ResponseError", err)
			}
			if responseErr.Field != tt.want {
				t.Errorf("Field = %q, want %q", responseErr.Field, tt.want)
			}
			if responseErr.RequestID != "req-123" || responseErr.StatusCode != http.StatusOK {
				t.Errorf("error lost its response metadata: %+v", responseErr)
			}
			if string(responseErr.Body) != tt.body {
				t.Errorf("error lost the raw body")
			}
		})
	}
}

func TestDecodeSystemOneTolerance(t *testing.T) {
	t.Parallel()

	body := `{
	  "model": "m",
	  "usage": {"input_tokens": 1, "output_tokens": 1, "reasoning_tokens": 9},
	  "answers": {
	    "spam": {"type": "noul", "noul": 0.9, "explanation": "spammy"},
	    "mystery": {"type": "aurora", "value": 3}
	  }
	}`

	got, err := decodeSystemOne(meta(body), nil, "", discard())
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	// Unknown extra fields are ignored; an answer kind this version does not
	// model is dropped rather than failing the response.
	if len(got.Answers) != 1 || got.Nouls["spam"].Noul != 0.9 {
		t.Errorf("Answers = %v, want only the noul", got.Answers)
	}
	if _, ok := got.Answers["mystery"]; ok {
		t.Error("Answers kept an unrecognized answer type")
	}
	// It is still reachable through the raw body.
	if !strings.Contains(string(got.Raw), "aurora") {
		t.Error("Raw lost the unrecognized answer")
	}
}

func TestDecodeSystemOneUsageOptional(t *testing.T) {
	t.Parallel()

	got, err := decodeSystemOne(meta(`{"model":"m","usage":{},"answers":{}}`), nil, "", discard())
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}
	if got.Usage.InputTokens != nil || got.Usage.OutputTokens != nil {
		t.Errorf("Usage = %+v, want nil token counts", got.Usage)
	}
	if got.Answers == nil || len(got.Answers) != 0 {
		t.Errorf("Answers = %v, want an empty map", got.Answers)
	}
}

func TestDecodeListModels(t *testing.T) {
	t.Parallel()

	body := `{"models":[{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01","context_window":128000}]}`
	got, err := decodeListModels(meta(body), "")
	if err != nil {
		t.Fatalf("decodeListModels() error = %v", err)
	}

	want := ModelMetadata{Name: "jev-latest", Description: "Fast model", ReleaseDate: "2026-08-01"}
	if len(got.Models) != 1 || got.Models[0] != want {
		t.Errorf("Models = %+v, want [%+v]", got.Models, want)
	}
	if got.RequestID != "req-123" {
		t.Errorf("ResponseMeta = %+v, want it populated", got.ResponseMeta)
	}
}

func TestDecodeListModelsInvalid(t *testing.T) {
	t.Parallel()

	model := `{"name":"n","description":"d","release_date":"r"}`

	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty body", ``, ""},
		{"null", `null`, "models"},
		{"missing models", `{}`, "models"},
		{"models wrong type", `{"models":"bad"}`, "models"},
		{"missing name", `{"models":[` + model + `,{"description":"d","release_date":"r"}]}`, "models[1].name"},
		{"missing description", `{"models":[` + model + `,{"name":"n","release_date":"r"}]}`, "models[1].description"},
		{"missing release_date", `{"models":[` + model + `,{"name":"n","description":"d"}]}`, "models[1].release_date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeListModels(meta(tt.body), "")

			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("decodeListModels() error = %v, want *ResponseError", err)
			}
			if responseErr.Field != tt.want {
				t.Errorf("Field = %q, want %q", responseErr.Field, tt.want)
			}
		})
	}
}

func TestDecodeListModelsEmpty(t *testing.T) {
	t.Parallel()

	got, err := decodeListModels(meta(`{"models":[]}`), "")
	if err != nil {
		t.Fatalf("decodeListModels() error = %v", err)
	}
	if len(got.Models) != 0 {
		t.Errorf("Models = %v, want empty", got.Models)
	}
}

func TestResponseMetaSetter(t *testing.T) {
	t.Parallel()

	type custom struct {
		ResponseMeta
		Model string `json:"model"`
	}

	var out any = &custom{}
	setter, ok := out.(metaSetter)
	if !ok {
		t.Fatal("an embedded ResponseMeta does not satisfy metaSetter")
	}

	setter.setMeta(ResponseMeta{RequestID: "req-9"})
	if got := out.(*custom).RequestID; got != "req-9" {
		t.Errorf("RequestID = %q, want req-9", got)
	}
}

func TestCheckAnswered(t *testing.T) {
	t.Parallel()

	const body = `{"model":"m","usage":{},"answers":{
		"spam":{"type":"noul","noul":0.9},
		"tone":{"type":"choice","choice":"calm","confidence":0.8,"probabilities":{"calm":1}}
	}}`

	tests := []struct {
		name      string
		questions map[string]Question
		want      string // the reported field path, empty when the response is accepted
	}{
		{
			"every question answered",
			map[string]Question{"spam": Noul{}, "tone": Choice{Criteria: map[string]any{"calm": nil}}},
			"",
		},
		{"a subset is fine", map[string]Question{"spam": Noul{}}, ""},
		{"no questions to check", nil, ""},

		{
			"unanswered question",
			map[string]Question{"spam": Noul{}, "urgency": Score{Criteria: []any{"low"}}},
			"answers.urgency",
		},
		{"answered by the wrong kind", map[string]Question{"spam": Choice{Criteria: map[string]any{"a": nil}}}, "answers.spam.type"},
		{"score asked, noul returned", map[string]Question{"spam": Score{Criteria: []any{"low"}}}, "answers.spam.type"},

		// A raw question is held to the type it names.
		{"raw question matched", map[string]Question{"spam": RawQuestion{"type": "noul"}}, ""},
		{"raw question mismatched", map[string]Question{"spam": RawQuestion{"type": "score"}}, "answers.spam.type"},

		// Pointers behave like values.
		{"pointer question", map[string]Question{"spam": &Noul{}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeSystemOne(meta(body), tt.questions, "POST https://x/v1/systemone", discard())

			if tt.want == "" {
				if err != nil {
					t.Fatalf("decodeSystemOne() error = %v, want nil", err)
				}
				return
			}

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

func TestCheckAnsweredAllowsUnmodeledKinds(t *testing.T) {
	t.Parallel()

	// A raw question answered by a kind this SDK version does not model counts
	// as answered: the escape hatch must keep working, with the payload on Raw.
	const body = `{"model":"m","usage":{},"answers":{"mystery":{"type":"aurora","value":3}}}`

	got, err := decodeSystemOne(meta(body), map[string]Question{"mystery": RawQuestion{"type": "aurora"}}, "", discard())
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v, want the unmodeled answer tolerated", err)
	}

	if len(got.Answers) != 0 {
		t.Errorf("Answers = %v, want the unmodeled answer dropped", got.Answers)
	}
	if !strings.Contains(string(got.Raw), "aurora") {
		t.Error("Raw lost the unmodeled answer")
	}
}

func TestCheckAnsweredIsDeterministic(t *testing.T) {
	t.Parallel()

	// Two questions go unanswered; sorted order decides which is reported.
	questions := map[string]Question{"zulu": Noul{}, "alpha": Noul{}}

	for range 20 {
		_, err := decodeSystemOne(meta(`{"model":"m","usage":{},"answers":{}}`), questions, "", discard())

		var responseErr *ResponseError
		if !errors.As(err, &responseErr) {
			t.Fatalf("decodeSystemOne() error = %v, want *ResponseError", err)
		}
		if responseErr.Field != "answers.alpha" {
			t.Fatalf("Field = %q, want answers.alpha", responseErr.Field)
		}
	}
}

func TestCheckAnsweredRunsBeforeFieldDecoding(t *testing.T) {
	t.Parallel()

	// An unanswered question outranks a malformed answer to a different one:
	// the caller's own question is the more useful thing to name.
	const body = `{"model":"m","usage":{},"answers":{"zulu":{"type":"noul"}}}`

	_, err := decodeSystemOne(meta(body), map[string]Question{"alpha": Noul{}, "zulu": Noul{}}, "", discard())

	var responseErr *ResponseError
	if !errors.As(err, &responseErr) {
		t.Fatalf("decodeSystemOne() error = %v, want *ResponseError", err)
	}
	if responseErr.Field != "answers.alpha" {
		t.Errorf("Field = %q, want answers.alpha", responseErr.Field)
	}
}

func TestQuestionKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		question Question
		want     string
	}{
		{Noul{}, kindNoul},
		{Choice{}, kindChoice},
		{Score{}, kindScore},
		{&Noul{}, kindNoul},
		{&Choice{}, kindChoice},
		{&Score{}, kindScore},
		{RawQuestion{"type": "noul"}, kindNoul},
		{RawQuestion{"type": "aurora"}, "aurora"},
		{RawQuestion{}, ""},
		{nil, ""},
	}

	for _, tt := range tests {
		if got := questionKind(tt.question); got != tt.want {
			t.Errorf("questionKind(%#v) = %q, want %q", tt.question, got, tt.want)
		}
	}
}
