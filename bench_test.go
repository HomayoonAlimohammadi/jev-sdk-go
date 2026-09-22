package jev

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// benchBody is one of each answer kind, the shape a typical call returns.
const benchBody = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{` +
	`"spam":{"type":"noul","noul":0.98},` +
	`"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},` +
	`"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

func benchRequest() SystemOneRequest {
	return SystemOneRequest{
		State: "I was charged twice this month. Please fix this ASAP.",
		Questions: map[string]Question{
			"spam":    Noul{Instructions: "Spam?"},
			"tone":    Choice{Instructions: "Tone?", Criteria: Labels("friendly", "hostile")},
			"quality": Score{Instructions: "Quality?", Criteria: Levels("bad", "ok", "great")},
		},
	}
}

// BenchmarkSystemOne measures a whole call through the public API against a
// local server, so the network is loopback and the SDK's own cost shows.
func BenchmarkSystemOne(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", jsonContentType)
		w.Write([]byte(benchBody))
	}))
	b.Cleanup(server.Close)

	client, err := New(WithAPIKey("k"), WithBaseURL(server.URL), WithRetry(RetryPolicy{}))
	if err != nil {
		b.Fatal(err)
	}
	req := benchRequest()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := client.SystemOne(b.Context(), req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeSystemOne isolates response decoding and validation.
func BenchmarkDecodeSystemOne(b *testing.B) {
	meta := ResponseMeta{StatusCode: http.StatusOK, Raw: []byte(benchBody)}
	questions := benchRequest().Questions

	b.ReportAllocs()
	for b.Loop() {
		if _, err := decodeSystemOne(meta, questions, "POST https://x/v1/systemone"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkValidateQuestions isolates the checks run before a request is sent.
func BenchmarkValidateQuestions(b *testing.B) {
	questions := benchRequest().Questions

	b.ReportAllocs()
	for b.Loop() {
		if err := validateQuestions(questions); err != nil {
			b.Fatal(err)
		}
	}
}
