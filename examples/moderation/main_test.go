package main

import (
	"bytes"
	"strings"
	"testing"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
	"github.com/HomayoonAlimohammadi/jev-sdk-go/internal/fakeapi"
)

func TestRun(t *testing.T) {
	t.Parallel()

	api := fakeapi.New(t)

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	for name := range policies {
		if !strings.Contains(out.String(), name) {
			t.Errorf("output is missing policy %q:\n%s", name, out.String())
		}
	}
	if !strings.Contains(out.String(), "verdict: ") {
		t.Errorf("output has no verdict:\n%s", out.String())
	}
	// All policies go out in a single call, with the struct as the state.
	requests := api.Requests()
	if len(requests) != 1 || !strings.Contains(string(requests[0].Body), `"account_age_days":2`) {
		t.Errorf("want one call carrying the structured state, got %d", len(requests))
	}
}
