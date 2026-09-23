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

	// One transient failure, so there is a retry to log and to count.
	api := fakeapi.New(t, fakeapi.FailFirst(1, http.StatusServiceUnavailable, "0"))

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL), jev.WithAPIKey("secret-key")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	for _, want := range []string{`"msg":"jev: retrying"`, `"msg":"jev: response"`, "request req-fake-2", "attempts: 2, of which retries: 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "secret-key") {
		t.Error("the API key reached the log output")
	}
}
