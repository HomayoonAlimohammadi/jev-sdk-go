package main

import (
	"bytes"
	"net/http"
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

	// Results come back in input order, whatever order the calls finished in.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, message := range messages {
		if !strings.HasPrefix(lines[i], message[:min(len(message), 20)]) {
			t.Errorf("line %d = %q, want message %d first", i, lines[i], i)
		}
	}
	if want := "6 classified, 0 failed"; !strings.Contains(out.String(), want) {
		t.Errorf("output is missing %q:\n%s", want, out.String())
	}
}

func TestRunReportsFailuresPerItem(t *testing.T) {
	t.Parallel()

	// A permanent failure for some calls must not lose the rest of the batch.
	api := fakeapi.New(t, fakeapi.FailFirst(2, http.StatusBadRequest, ""))

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	if want := "4 classified, 2 failed"; !strings.Contains(out.String(), want) {
		t.Errorf("output is missing %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "400 injected failure") {
		t.Errorf("failures do not carry the API's explanation:\n%s", out.String())
	}
}
