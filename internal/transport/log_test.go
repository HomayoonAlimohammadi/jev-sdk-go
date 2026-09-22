package transport

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsSecret(t *testing.T) {
	t.Parallel()

	secret := []string{
		"Authorization", "authorization", "Proxy-Authorization", "X-API-Key", "api-key",
		"Cookie", "Set-Cookie", "X-Access-Token", "X-Client-Secret", "x-MiXeD-ToKeN",
		// Names the SDK has never heard of, which callers really do set.
		"X-Signature", "X-Hub-Signature-256", "X-Auth", "Authentication",
		"X-Access-Key", "X-Session-Id", "Ocp-Apim-Subscription-Key", "X-Password",
	}
	public := []string{"Accept", "Content-Type", "X-Typesafe-Request-Id", "X-Team", "User-Agent", "X-Request-Id"}

	for _, name := range secret {
		if !isSecret(name) {
			t.Errorf("isSecret(%q) = false, want true", name)
		}
	}
	for _, name := range public {
		if isSecret(name) {
			t.Errorf("isSecret(%q) = true, want false", name)
		}
	}
}

func TestLoggingRedactsCredentials(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "response-credential")
		w.Header().Set("X-Visible", "response-visible")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	var logged bytes.Buffer
	client := newClient(newClock())
	client.Logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	req := request(server.URL)
	req.Header = http.Header{
		"Authorization":   []string{"Bearer auth-credential"},
		"X-Api-Key":       []string{"key-credential"},
		"X-Client-Secret": []string{"secret-credential"},
		"X-Visible":       []string{"request-visible"},
	}

	if _, _, err := client.Send(t.Context(), req, alwaysRetry(2, time.Millisecond)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	output := logged.String()
	for _, credential := range []string{"auth-credential", "key-credential", "secret-credential", "response-credential"} {
		if strings.Contains(output, credential) {
			t.Errorf("log output leaked %q", credential)
		}
	}
	for _, visible := range []string{"request-visible", "response-visible", redacted} {
		if !strings.Contains(output, visible) {
			t.Errorf("log output is missing %q", visible)
		}
	}
	if !strings.Contains(output, "jev: retrying") {
		t.Error("log output is missing the retry line")
	}
}

func TestLoggingOmitsBodiesUnlessAsked(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"answer":"secret-response-body"}`))
	}))
	defer server.Close()

	for _, logBodies := range []bool{false, true} {
		t.Run(fmt.Sprintf("LogBodies=%v", logBodies), func(t *testing.T) {
			var logged bytes.Buffer
			client := newClient(newClock())
			client.Logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

			req := request(server.URL)
			req.LogBodies = logBodies
			req.Body = []byte(`{"state":"secret-request-body"}`)

			if _, _, err := client.Send(t.Context(), req, alwaysRetry(1, 0)); err != nil {
				t.Fatalf("Send() error = %v", err)
			}

			output := logged.String()
			for _, body := range []string{"secret-request-body", "secret-response-body"} {
				if got := strings.Contains(output, body); got != logBodies {
					t.Errorf("body %q logged = %v, want %v", body, got, logBodies)
				}
			}
			// The rest of the record is emitted either way.
			if !strings.Contains(output, "jev: response") {
				t.Error("log output is missing the response record")
			}
		})
	}
}

func TestLoggingUsesTheSanitizedURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	var logged bytes.Buffer
	client := newClient(newClock())
	client.Logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Every log site must use SafeURL, never the URL that goes on the wire.
	req := request(server.URL)
	req.URL = strings.Replace(server.URL, "http://", "http://svc:url-credential@", 1)
	req.SafeURL = server.URL

	if _, _, err := client.Send(t.Context(), req, alwaysRetry(2, time.Millisecond)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if output := logged.String(); strings.Contains(output, "url-credential") {
		t.Errorf("log output leaked the URL credential: %q", output)
	}
}

func TestLoggingIncludesBodiesAtDebug(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-log")
		w.Write([]byte(`{"answer":"visible-response-body"}`))
	}))
	defer server.Close()

	for _, tt := range []struct {
		level    slog.Level
		wantBody bool
		wantInfo bool
	}{
		{slog.LevelDebug, true, true},
		{slog.LevelInfo, false, true},
		{slog.LevelWarn, false, false},
	} {
		t.Run(tt.level.String(), func(t *testing.T) {
			var logged bytes.Buffer
			client := newClient(newClock())
			client.Logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: tt.level}))

			if _, _, err := client.Send(t.Context(), request(server.URL), alwaysRetry(1, 0)); err != nil {
				t.Fatalf("Send() error = %v", err)
			}

			output := logged.String()
			if got := strings.Contains(output, "visible-response-body"); got != tt.wantBody {
				t.Errorf("body logged = %v, want %v", got, tt.wantBody)
			}
			if got := strings.Contains(output, "req-log"); got != tt.wantInfo {
				t.Errorf("request id logged = %v, want %v", got, tt.wantInfo)
			}
		})
	}
}

func TestDisabledLoggingAllocatesNothing(t *testing.T) {
	// slog boxes arguments into ...any before it can check the level, so each
	// call site must check first. A client that logs nothing must pay nothing.
	client := newClient(newClock())
	req := request("https://api.test/v1/models")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"req-1"}}}
	body := []byte(`{"models":[]}`)
	ctx := context.Background()

	for name, logCall := range map[string]func(){
		"request":  func() { client.logRequest(ctx, req, req.Header) },
		"response": func() { client.logResponse(ctx, req, resp, body, time.Millisecond) },
	} {
		if allocs := testing.AllocsPerRun(100, logCall); allocs != 0 {
			t.Errorf("%s logging allocated %v times per run with logging off, want 0", name, allocs)
		}
	}
}
