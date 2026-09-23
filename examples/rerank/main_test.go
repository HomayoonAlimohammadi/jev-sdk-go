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

	for _, doc := range candidates {
		if !strings.Contains(out.String(), doc.Title) {
			t.Errorf("output is missing %q:\n%s", doc.Title, out.String())
		}
	}
	// Every candidate is rated in one call.
	if got := len(api.Requests()); got != 1 {
		t.Errorf("made %d calls, want 1", got)
	}
}
