package fakeapi_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
	"github.com/HomayoonAlimohammadi/jev-sdk-go/internal/fakeapi"
)

type level int

func request() jev.SystemOneRequest {
	return jev.SystemOneRequest{
		State: map[string]any{"ticket": "I was charged twice."},
		Questions: map[string]jev.Question{
			"billing": jev.Noul{Instructions: "About billing?"},
			"team":    jev.Choice{Criteria: jev.Labels("billing", "technical", "other")},
			"urgency": jev.ScoreOf[level]{Criteria: jev.Levels("low", "mid", "high")},
			"future":  jev.RawQuestion{"type": "aurora"},
		},
	}
}

func TestAnswersDecodeThroughTheSDK(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t)
	client, err := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(api.URL))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.SystemOne(t.Context(), request())
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if noul := resp.Nouls["billing"].Noul; noul < 0 || noul > 1 {
		t.Errorf("noul = %v, want a probability", noul)
	}

	team := resp.Choices["team"]
	if _, offered := map[string]bool{"billing": true, "technical": true, "other": true}[team.Choice]; !offered {
		t.Errorf("choice = %q, want one of the offered labels", team.Choice)
	}
	if sum := total(team.Probabilities); math.Abs(sum-1) > 1e-9 {
		t.Errorf("choice probabilities sum to %v, want 1", sum)
	}

	urgency := resp.Scores["urgency"]
	if urgency.Score < 0 || urgency.Score > 2 || len(urgency.Legend) != 3 {
		t.Errorf("score = %+v, want an expected level over three", urgency)
	}

	if _, ok := resp.Answers["future"].(jev.UnknownAnswer); !ok {
		t.Errorf("Answers[future] = %#v, want an UnknownAnswer", resp.Answers["future"])
	}
	if resp.RequestID == "" {
		t.Error("RequestID is empty, want the fake's request ID")
	}
}

func TestAnswersAreDeterministicButVary(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t)
	client, _ := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(api.URL))

	ask := func(state string) float64 {
		t.Helper()
		resp, err := client.SystemOne(t.Context(), jev.SystemOneRequest{
			State:     state,
			Questions: map[string]jev.Question{"q": jev.Noul{Instructions: "?"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Nouls["q"].Noul
	}

	if ask("same") != ask("same") {
		t.Error("the same question about the same state was answered differently")
	}
	seen := map[float64]bool{}
	for _, state := range []string{"a", "b", "c", "d", "e"} {
		seen[ask(state)] = true
	}
	if len(seen) < 2 {
		t.Error("five different states all got the same answer; the fake should vary")
	}
}

func TestOpenRouterMode(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t, fakeapi.OpenRouter())
	client, _ := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(api.URL))

	resp, err := client.SystemOne(t.Context(), request())
	if err != nil {
		t.Fatalf("SystemOne() error = %v, want OpenRouter's extra fields tolerated", err)
	}

	var extra struct {
		ID    string `json:"id"`
		Usage struct {
			Cost *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(resp.Raw, &extra); err != nil || extra.ID == "" || extra.Usage.Cost == nil {
		t.Errorf("Raw = %s, want id and usage.cost", resp.Raw)
	}

	// OpenRouter's /v1/models is its own catalog, which the SDK rejects.
	_, err = client.ListModels(t.Context())
	var responseErr *jev.ResponseError
	if !errors.As(err, &responseErr) || responseErr.Field != "models" {
		t.Errorf("ListModels() error = %v, want a *ResponseError at models", err)
	}
}

func TestFailFirst(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t, fakeapi.FailFirst(2, http.StatusServiceUnavailable, "0"))
	client, _ := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(api.URL))

	if _, err := client.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() error = %v, want the default policy to retry past two 503s", err)
	}
	if got := len(api.Requests()); got != 3 {
		t.Errorf("fake saw %d requests, want 3", got)
	}
}

func TestRejectsMissingKeyAndAcceptsPathPrefix(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t)

	client, _ := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(api.URL+"/gateway/typesafe"))
	if _, err := client.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() error = %v, want a path prefix accepted", err)
	}
	if path := api.Requests()[0].Path; path != "/gateway/typesafe/v1/models" {
		t.Errorf("path = %q, want the prefix kept", path)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, api.URL+"/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d without a key, want 401", resp.StatusCode)
	}
}

func total(m map[string]float64) float64 {
	var sum float64
	for _, v := range m {
		sum += v
	}
	return sum
}
