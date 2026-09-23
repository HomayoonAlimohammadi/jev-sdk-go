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

	// One decision line per ticket, each a known routing outcome.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(tickets) {
		t.Fatalf("got %d decisions, want %d:\n%s", len(lines), len(tickets), out.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, "human review") && !strings.Contains(line, "on-call") && !strings.Contains(line, "queue for") {
			t.Errorf("unexpected decision: %s", line)
		}
	}
	if got := len(api.Requests()); got != len(tickets) {
		t.Errorf("made %d calls, want one per ticket (%d)", got, len(tickets))
	}
}
