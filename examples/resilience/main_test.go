package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
	"github.com/HomayoonAlimohammadi/jev-sdk-go/internal/fakeapi"
)

func TestRunRecoversFromTransientFailures(t *testing.T) {
	t.Parallel()

	// The first two attempts fail; the policy retries past them.
	api := fakeapi.New(t, fakeapi.FailFirst(2, http.StatusServiceUnavailable, "0"))

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	for _, want := range []string{
		"rubric without levels: refused before sending",
		"retried call:          urgent",
		"fail-fast call:        urgent",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out.String())
		}
	}
	// The refused request never reached the API; the retried one took three
	// attempts; the fail-fast one took one.
	if got := len(api.Requests()); got != 4 {
		t.Errorf("fake saw %d requests, want 4", got)
	}
}

func TestRunFailFastDoesNotRetry(t *testing.T) {
	t.Parallel()

	// Five failures exhaust the retried call; the sixth request, the
	// fail-fast call's only attempt, then succeeds.
	api := fakeapi.New(t, fakeapi.FailFirst(5, http.StatusServiceUnavailable, "0"))

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	if want := "retried call:          API unavailable after 5 attempts"; !strings.Contains(out.String(), want) {
		t.Errorf("output is missing %q:\n%s", want, out.String())
	}
	if want := "fail-fast call:        urgent"; !strings.Contains(out.String(), want) {
		t.Errorf("output is missing %q:\n%s", want, out.String())
	}
}
