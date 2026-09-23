package main

import (
	"bytes"
	"encoding/json"
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

	for _, want := range []string{"refund_requested", "product", "satisfaction", "usage as sent"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out.String())
		}
	}

	// The configured questions went out exactly as written.
	var sent struct {
		Questions map[string]map[string]any `json:"questions"`
	}
	if err := json.Unmarshal(api.Requests()[0].Body, &sent); err != nil {
		t.Fatal(err)
	}
	if got := sent.Questions["product"]["criteria"].(map[string]any)["cards"]; got != "Physical or virtual payment cards" {
		t.Errorf("product criteria = %v, want the configured description forwarded", sent.Questions["product"])
	}
}
