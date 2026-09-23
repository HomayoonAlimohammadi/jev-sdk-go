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

	// The fake answers as OpenRouter does: extra fields on the response and
	// its own catalog at /v1/models.
	api := fakeapi.New(t, fakeapi.OpenRouter())

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("sk-or-test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	for _, want := range []string{"churn", "generation gen-fake-1 via TypeSafe, cost $", "model listing is not available through OpenRouter"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out.String())
		}
	}
	if auth := api.Requests()[0].Header.Get("Authorization"); auth != "Bearer sk-or-test" {
		t.Errorf("Authorization = %q, want the OpenRouter key as a bearer token", auth)
	}
}
