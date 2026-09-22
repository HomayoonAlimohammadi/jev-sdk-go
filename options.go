package jev

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type clientConfig struct {
	apiKey     string
	baseURL    string
	model      string
	timeout    time.Duration
	header     http.Header
	retry      RetryPolicy
	logger     *slog.Logger
	httpClient *http.Client
}

// ClientOption configures a [Client]. Options are applied in order, and each
// one overrides the environment.
type ClientOption func(*clientConfig) error

// WithAPIKey sets the API key, overriding TYPESAFE_API_KEY. Surrounding
// whitespace is trimmed.
func WithAPIKey(key string) ClientOption {
	return func(c *clientConfig) error {
		c.apiKey = key
		return nil
	}
}

// WithBaseURL sets the API root, overriding TYPESAFE_BASE_URL. Trailing
// slashes are trimmed.
func WithBaseURL(rawURL string) ClientOption {
	return func(c *clientConfig) error {
		c.baseURL = rawURL
		return nil
	}
}

// WithModel sets the model used when a request does not name one, overriding
// TYPESAFE_DEFAULT_MODEL.
func WithModel(model string) ClientOption {
	return func(c *clientConfig) error {
		c.model = model
		return nil
	}
}

// WithTimeout bounds each HTTP attempt. It must be positive. To bound a whole
// call including retries, set [RetryPolicy.Budget] or give the call a context
// with a deadline.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) error {
		c.timeout = d
		return nil
	}
}

// WithRetry replaces the retry policy. Pass RetryPolicy{} to disable retries.
func WithRetry(policy RetryPolicy) ClientOption {
	return func(c *clientConfig) error {
		c.retry = policy
		return nil
	}
}

// WithHeader sets a header on every request. It may be used repeatedly; a
// later call replaces an earlier one for the same name. Authentication,
// content negotiation and SDK identification headers cannot be replaced.
func WithHeader(name, value string) ClientOption {
	return func(c *clientConfig) error {
		c.header.Set(name, value)
		return nil
	}
}

// WithHTTPClient supplies the [http.Client] used for every request. Use it for
// proxies, custom TLS, connection tuning or instrumentation. The client is not
// modified, and it is the caller's to close.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *clientConfig) error {
		if client == nil {
			return fmt.Errorf("jev: WithHTTPClient was given a nil client")
		}
		c.httpClient = client
		return nil
	}
}

// WithLogger sends the SDK's logs to l. Credential-bearing headers are
// redacted, but request and response bodies are logged verbatim at debug
// level. Without this option the SDK logs nothing.
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *clientConfig) error {
		if l == nil {
			return fmt.Errorf("jev: WithLogger was given a nil logger")
		}
		c.logger = l
		return nil
	}
}

type callConfig struct {
	retry   *RetryPolicy
	timeout time.Duration
	header  http.Header
}

// CallOption overrides a client setting for one call.
type CallOption func(*callConfig) error

func applyCallOptions(opts []CallOption) (callConfig, error) {
	var call callConfig
	for _, opt := range opts {
		if err := opt(&call); err != nil {
			return callConfig{}, err
		}
	}
	return call, nil
}

// WithCallRetry replaces the client's retry policy for one call. It does not
// merge with the client policy: the given one is used as written.
func WithCallRetry(policy RetryPolicy) CallOption {
	return func(c *callConfig) error {
		c.retry = &policy
		return nil
	}
}

// WithCallTimeout overrides the per-attempt timeout for one call. Zero
// inherits the client's timeout.
func WithCallTimeout(d time.Duration) CallOption {
	return func(c *callConfig) error {
		if d < 0 {
			return fmt.Errorf("%w: timeout must not be negative, got %v", ErrInvalidTimeout, d)
		}
		c.timeout = d
		return nil
	}
}

// WithCallHeader sets a header for one call, overriding any client header of
// the same name.
func WithCallHeader(name, value string) CallOption {
	return func(c *callConfig) error {
		if c.header == nil {
			c.header = http.Header{}
		}
		c.header.Set(name, value)
		return nil
	}
}
