package jev

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestExtractMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "status code (no body)"},
		{"plain text", "plain text", "plain text"},
		{"json string", `"quoted text"`, "quoted text"},

		{"error wins", `{"error":"error","message":"message","detail":"detail"}`, "error"},
		{"nested error", `{"error":{"message":"nested error"},"message":"message"}`, "nested error"},
		{"message", `{"message":"message","detail":"detail"}`, "message"},
		{"detail", `{"detail":"detail"}`, "detail"},
		{"nested detail", `{"detail":{"message":"nested detail"}}`, "nested detail"},

		{
			"validation entries",
			`{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}`,
			"questions.q.score.criteria.0: Invalid; Missing",
		},

		{"unrecognized object", `{"unexpected":true}`, `{"unexpected":true}`},
		{"error not a string", `{"error":42}`, `{"error":42}`},
		{"empty detail list", `{"detail":[]}`, `{"detail":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := extractMessage([]byte(tt.body)); got != tt.want {
				t.Errorf("extractMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractMessageTruncates(t *testing.T) {
	t.Parallel()

	body := `{"x":"` + strings.Repeat("é", 300) + `"}`
	got := extractMessage([]byte(body))

	if !strings.HasSuffix(got, "…") {
		t.Errorf("extractMessage() = %q, want a truncation marker", got)
	}
	if len(got) > maxErrorBodyLength+len("…") {
		t.Errorf("extractMessage() length = %d, want <= %d", len(got), maxErrorBodyLength+len("…"))
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("extractMessage() = %q, want no truncation mid-rune", got)
		}
	}
}

func TestAPIErrorError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{
			"full",
			&APIError{StatusCode: 503, Message: "attempt 3", Endpoint: "POST https://api.typesafe.ai/v1/systemone", RequestID: "request-3"},
			"POST https://api.typesafe.ai/v1/systemone: 503 attempt 3 (request_id=request-3)",
		},
		{"no endpoint", &APIError{StatusCode: 400, Message: "bad"}, "400 bad"},
		{"no message", &APIError{StatusCode: 500}, "500"},
		{"no request id", &APIError{StatusCode: 404, Message: "gone", Endpoint: "GET https://x/y"}, "GET https://x/y: 404 gone"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIErrorIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   error
	}{
		{http.StatusBadRequest, ErrBadRequest},
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusUnprocessableEntity, ErrUnprocessable},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusInternalServerError, ErrServer},
		{http.StatusServiceUnavailable, ErrServer},
		{599, ErrServer},
	}

	sentinels := []error{ErrBadRequest, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrUnprocessable, ErrRateLimited, ErrServer}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			t.Parallel()

			err := error(&APIError{StatusCode: tt.status})
			for _, sentinel := range sentinels {
				want := sentinel == tt.want
				if got := errors.Is(err, sentinel); got != want {
					t.Errorf("errors.Is(%d, %v) = %v, want %v", tt.status, sentinel, got, want)
				}
			}

			var api *APIError
			if !errors.As(err, &api) || api.StatusCode != tt.status {
				t.Errorf("errors.As() failed to recover the *APIError")
			}
		})
	}
}

func TestAPIErrorIsUncategorized(t *testing.T) {
	t.Parallel()

	// 408 and 409 have no sentinel; they are still *APIError.
	for _, status := range []int{http.StatusRequestTimeout, http.StatusConflict, http.StatusFound} {
		err := error(&APIError{StatusCode: status})
		for _, sentinel := range []error{ErrBadRequest, ErrRateLimited, ErrServer, ErrNotFound} {
			if errors.Is(err, sentinel) {
				t.Errorf("errors.Is(%d, %v) = true, want false", status, sentinel)
			}
		}
	}
}

type fakeNetError struct{ timeout bool }

func (e fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool   { return e.timeout }
func (e fakeNetError) Temporary() bool { return false }

var _ net.Error = fakeNetError{}

func TestTransportError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantTimeout bool
	}{
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("dial: %w", context.DeadlineExceeded), true},
		{"net timeout", fakeNetError{timeout: true}, true},
		{"net non-timeout", fakeNetError{timeout: false}, false},
		{"canceled", context.Canceled, false},
		{"plain", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := &TransportError{Endpoint: "GET https://x/y", Attempts: 3, Err: tt.err}
			if got := err.Timeout(); got != tt.wantTimeout {
				t.Errorf("Timeout() = %v, want %v", got, tt.wantTimeout)
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("errors.Is() could not unwrap to the cause")
			}
			if want := "GET https://x/y: " + tt.err.Error(); err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestResponseErrorError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *ResponseError
		want string
	}{
		{
			"with field",
			&ResponseError{StatusCode: 200, Field: "answers.tone.confidence", Endpoint: "POST https://api.typesafe.ai/v1/systemone", RequestID: "req-123"},
			`POST https://api.typesafe.ai/v1/systemone: 200 invalid response data at "answers.tone.confidence" (request_id=req-123)`,
		},
		{"whole body", &ResponseError{StatusCode: 200}, "200 invalid response data"},
		{"nested index", &ResponseError{StatusCode: 200, Field: "models[1].name"}, `200 invalid response data at "models[1].name"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func FuzzExtractMessage(f *testing.F) {
	f.Add(`{"detail":[{"loc":["body","a",0],"msg":"x"}]}`)
	f.Add(`{"error":{"message":"m"}}`)
	f.Add("plain")
	f.Add("")

	f.Fuzz(func(t *testing.T, body string) {
		extractMessage([]byte(body)) // must not panic
	})
}
