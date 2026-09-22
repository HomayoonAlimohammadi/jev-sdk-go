//go:build integration

// These tests call the live API. Run them with:
//
//	TYPESAFE_API_KEY=... go test -tags=integration -run Integration ./...
package jev_test

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

func liveClient(t *testing.T) *jev.Client {
	t.Helper()

	if os.Getenv(jev.APIKeyEnv) == "" {
		t.Skipf("%s is not set", jev.APIKeyEnv)
	}

	client, err := jev.New(jev.WithTimeout(2 * time.Minute))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestIntegrationListModels(t *testing.T) {
	resp, err := liveClient(t).ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(resp.Models) == 0 {
		t.Fatal("ListModels() returned no models")
	}

	for _, model := range resp.Models {
		if model.Name == "" || model.Description == "" || model.ReleaseDate == "" {
			t.Errorf("incomplete model metadata: %+v", model)
		}
	}
}

func TestIntegrationSystemOne(t *testing.T) {
	resp, err := liveClient(t).SystemOne(t.Context(), jev.SystemOneRequest{
		State: map[string]any{
			"subject": "Charged twice this month",
			"body":    "I see two charges of $49. I only have one account. Please fix this ASAP.",
		},
		Questions: map[string]jev.Question{
			"billing": jev.Noul{
				Instructions: "Is this ticket about billing?",
				Criteria:     &jev.NoulCriteria{True: "Payments or invoices"},
			},
			"tone": jev.Choice{
				Instructions: "What is the customer's tone?",
				Criteria:     map[string]any{"calm": nil, "frustrated": nil, "angry": nil},
			},
			"urgency": jev.Score{
				Instructions: "How urgent is this ticket?",
				Criteria:     []any{"can wait", "this week", "today"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	if resp.Model == "" || resp.RequestID == "" {
		t.Errorf("response metadata = %+v", resp.ResponseMeta)
	}

	if noul := resp.Nouls["billing"].Noul; noul < 0 || noul > 1 {
		t.Errorf("billing noul = %v, want it within [0, 1]", noul)
	}

	tone := resp.Choices["tone"]
	if _, ok := map[string]bool{"calm": true, "frustrated": true, "angry": true}[tone.Choice]; !ok {
		t.Errorf("tone = %q, want one of the requested labels", tone.Choice)
	}
	if total := sum(tone.Probabilities); math.Abs(total-1) > 0.1 {
		t.Errorf("tone probabilities sum to %v, want about 1", total)
	}

	urgency := resp.Scores["urgency"]
	if urgency.Score < 0 || urgency.Score > 2 {
		t.Errorf("urgency = %v, want it within [0, 2]", urgency.Score)
	}
	for level, want := range map[int]string{0: "can wait", 1: "this week", 2: "today"} {
		if urgency.Legend[level] != want {
			t.Errorf("legend[%d] = %v, want %q", level, urgency.Legend[level], want)
		}
	}
}

func TestIntegrationSystemOneAs(t *testing.T) {
	type answers struct {
		jev.ResponseMeta

		Answers struct {
			Billing jev.NoulAnswer `json:"billing"`
		} `json:"answers"`
	}

	got, err := liveClient(t).SystemOneAs[answers](t.Context(), jev.SystemOneRequest{
		State:     "I see two charges of $49. Please fix this.",
		Questions: map[string]jev.Question{"billing": jev.Noul{Instructions: "Is this about billing?"}},
	})
	if err != nil {
		t.Fatalf("SystemOneAs() error = %v", err)
	}

	if got.RequestID == "" {
		t.Error("an embedded ResponseMeta was not populated")
	}
	if noul := got.Answers.Billing.Noul; noul < 0 || noul > 1 {
		t.Errorf("billing noul = %v, want it within [0, 1]", noul)
	}
}

func TestIntegrationContextCancellation(t *testing.T) {
	client := liveClient(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := client.ListModels(ctx); err == nil {
		t.Fatal("ListModels() error = nil, want a cancellation error")
	}
}

func sum(values map[string]float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total
}
