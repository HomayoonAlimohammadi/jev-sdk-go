package main

import (
	"bytes"
	"strings"
	"testing"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
	"github.com/HomayoonAlimohammadi/jev-sdk-go/internal/fakeapi"
)

func TestRun(t *testing.T) {
	t.Setenv("GATEWAY_KEY", "gateway-secret")

	api := fakeapi.New(t)

	var out bytes.Buffer
	if err := run(t.Context(), &out, jev.WithBaseURL(api.URL+"/typesafe"), jev.WithAPIKey("test")); err != nil {
		t.Fatalf("run() error = %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), "ticket T-48213: bug") {
		t.Errorf("output is missing the result:\n%s", out.String())
	}

	// The prefix, the gateway's headers and the per-call header all arrived,
	// and the protected Authorization header was not displaced.
	request := api.Requests()[0]
	if request.Path != "/typesafe/v1/systemone" {
		t.Errorf("path = %q, want the gateway prefix kept", request.Path)
	}
	for header, want := range map[string]string{
		"X-Gateway-Key": "gateway-secret",
		"X-Team":        "support-tooling",
		"X-Ticket-Id":   "T-48213",
		"Authorization": "Bearer test",
	} {
		if got := request.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
