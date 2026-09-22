package jev

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// clearEnv removes the SDK's environment variables for one test. It cannot run
// in parallel, because t.Setenv mutates process state.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{APIKeyEnv, BaseURLEnv, DefaultModelEnv} {
		t.Setenv(name, "")
	}
}

func TestConfigPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		opts      []ClientOption
		wantKey   string
		wantURL   string
		wantModel string
	}{
		{
			name:      "defaults",
			env:       map[string]string{APIKeyEnv: "env-key"},
			wantKey:   "env-key",
			wantURL:   DefaultBaseURL,
			wantModel: DefaultModel,
		},
		{
			name: "environment",
			env: map[string]string{
				APIKeyEnv:       "  env-key  ",
				BaseURLEnv:      "  https://env.test///  ",
				DefaultModelEnv: "  env-model  ",
			},
			wantKey:   "env-key",
			wantURL:   "https://env.test",
			wantModel: "env-model",
		},
		{
			name: "options override the environment",
			env: map[string]string{
				APIKeyEnv:       "env-key",
				BaseURLEnv:      "https://env.test",
				DefaultModelEnv: "env-model",
			},
			opts: []ClientOption{
				WithAPIKey("  code-key  "),
				WithBaseURL("https://code.test///"),
				WithModel("code-model"),
			},
			wantKey:   "code-key",
			wantURL:   "https://code.test",
			wantModel: "code-model",
		},
		{
			name: "whitespace-only environment values are ignored",
			env: map[string]string{
				APIKeyEnv:       "env-key",
				BaseURLEnv:      " \t ",
				DefaultModelEnv: " \t ",
			},
			wantKey:   "env-key",
			wantURL:   DefaultBaseURL,
			wantModel: DefaultModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			client, err := New(tt.opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if client.apiKey != tt.wantKey {
				t.Errorf("apiKey = %q, want %q", client.apiKey, tt.wantKey)
			}
			if client.baseURL != tt.wantURL {
				t.Errorf("baseURL = %q, want %q", client.baseURL, tt.wantURL)
			}
			if client.model != tt.wantModel {
				t.Errorf("model = %q, want %q", client.model, tt.wantModel)
			}
			if client.timeout != DefaultTimeout {
				t.Errorf("timeout = %v, want %v", client.timeout, DefaultTimeout)
			}
		})
	}
}

func TestMissingAPIKey(t *testing.T) {
	for _, value := range []string{"", " \t\n "} {
		t.Run(strings.TrimSpace(value)+"blank", func(t *testing.T) {
			clearEnv(t)
			t.Setenv(APIKeyEnv, value)

			if _, err := New(); !errors.Is(err, ErrNoAPIKey) {
				t.Errorf("New() error = %v, want ErrNoAPIKey", err)
			}
		})
	}
}

func TestInvalidAPIKey(t *testing.T) {
	const credential = "ts_live_private"

	for _, suffix := range []string{"\n", "\r", "\t", "\x1f", "\x7f", " ", "\u00e9", "\u200b", "\x00"} {
		t.Run(escapeName(suffix), func(t *testing.T) {
			clearEnv(t)
			t.Setenv(APIKeyEnv, "env-key")

			key := credential + suffix + "suffix"
			_, err := New(WithAPIKey(key))
			if !errors.Is(err, ErrInvalidAPIKey) {
				t.Fatalf("New() error = %v, want ErrInvalidAPIKey", err)
			}
			// An invalid explicit key must not silently fall back to the
			// environment, and must never be echoed back.
			if strings.Contains(err.Error(), credential) {
				t.Errorf("New() error leaked the key: %v", err)
			}
		})
	}
}

func TestEmptyExplicitKeyDoesNotFallBackToEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "env-key")

	if _, err := New(WithAPIKey("   ")); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("New() error = %v, want ErrNoAPIKey", err)
	}
}

func TestInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []ClientOption
		want error
	}{
		{"zero timeout", []ClientOption{WithTimeout(0)}, ErrInvalidTimeout},
		{"negative timeout", []ClientOption{WithTimeout(-time.Second)}, ErrInvalidTimeout},
		{"bad base url", []ClientOption{WithBaseURL("not a url")}, ErrInvalidBaseURL},
		{"base url without scheme", []ClientOption{WithBaseURL("api.typesafe.ai")}, ErrInvalidBaseURL},
		{"empty base url", []ClientOption{WithBaseURL("")}, ErrInvalidBaseURL},
		{"negative retries", []ClientOption{WithRetry(RetryPolicy{MaxRetries: -1})}, ErrInvalidRetry},
		{"bad jitter", []ClientOption{WithRetry(RetryPolicy{BackoffJitter: 2})}, ErrInvalidRetry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(APIKeyEnv, "env-key")

			if _, err := New(tt.opts...); !errors.Is(err, tt.want) {
				t.Errorf("New() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNilOptionArguments(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "env-key")

	if _, err := New(WithHTTPClient(nil)); err == nil {
		t.Error("New(WithHTTPClient(nil)) error = nil, want an error")
	}
	if _, err := New(WithLogger(nil)); err == nil {
		t.Error("New(WithLogger(nil)) error = nil, want an error")
	}
}

func TestSuppliedHTTPClientIsUsedUnmodified(t *testing.T) {
	clearEnv(t)
	t.Setenv(APIKeyEnv, "env-key")

	httpClient := &http.Client{Timeout: 3 * time.Second}
	client, err := New(WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if client.httpClient != httpClient {
		t.Error("New() did not use the supplied http.Client")
	}
	if httpClient.Timeout != 3*time.Second {
		t.Errorf("New() modified the supplied http.Client: %+v", httpClient)
	}
}

func TestSafeURLStripsCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"plain", "https://api.typesafe.ai/v1/models", "https://api.typesafe.ai/v1/models"},
		{"userinfo", "https://user:secret@api.typesafe.ai/v1/models", "https://api.typesafe.ai/v1/models"},
		{"query and fragment", "https://api.typesafe.ai/v1/models?token=secret#frag", "https://api.typesafe.ai/v1/models"},
		{"unparseable", "://bad", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := safeURL(tt.url)
			if got != tt.want {
				t.Errorf("safeURL() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "secret") {
				t.Errorf("safeURL() leaked a credential: %q", got)
			}
		})
	}
}

func TestEndpointIsBuiltFromTheSanitizedBase(t *testing.T) {
	clearEnv(t)

	client, err := New(WithAPIKey("k"), WithBaseURL("https://svc:secret@gw.test/jev"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// The endpoint is precomputed from the base once, so it must match what
	// sanitizing each full URL would have produced.
	if got, want := client.endpoint(http.MethodGet, modelsPath), "GET https://gw.test/jev/v1/models"; got != want {
		t.Errorf("endpoint() = %q, want %q", got, want)
	}
	if want := "https://gw.test/jev/v1/models"; safeURL(client.baseURL+modelsPath) != want {
		t.Errorf("sanitizing the full URL = %q, want %q", safeURL(client.baseURL+modelsPath), want)
	}
	// The wire URL keeps the credentials; only labels drop them.
	if !strings.Contains(client.baseURL, "svc:secret@") {
		t.Errorf("baseURL = %q, want the credentials kept for the request itself", client.baseURL)
	}
}

func TestUserAgent(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(userAgent, sdkName+"/") {
		t.Errorf("userAgent = %q, want it to start with %q", userAgent, sdkName+"/")
	}
	if !strings.HasPrefix(runtimeInfo, "go/") {
		t.Errorf("runtimeInfo = %q, want it to start with go/", runtimeInfo)
	}
}

func escapeName(s string) string {
	return strings.NewReplacer("\n", "n", "\r", "r", "\t", "t", "\x00", "nul", " ", "space").Replace(s)
}
