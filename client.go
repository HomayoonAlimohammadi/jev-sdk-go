package jev

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/HomayoonAlimohammadi/jev-sdk-go/internal/transport"
)

// Environment variables the SDK reads. An explicit option always wins, and an
// empty or whitespace-only value is ignored rather than shadowing the default.
const (
	APIKeyEnv       = "TYPESAFE_API_KEY"
	BaseURLEnv      = "TYPESAFE_BASE_URL"
	DefaultModelEnv = "TYPESAFE_DEFAULT_MODEL"
)

// Client defaults.
const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"

	// DefaultTimeout bounds each individual HTTP attempt, not the call as a
	// whole; see [RetryPolicy.Budget] for that.
	DefaultTimeout = 10 * time.Second
)

// Protocol constants.
const (
	modulePath = "github.com/HomayoonAlimohammadi/jev-sdk-go"
	sdkName    = "jev-sdk-go"

	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"

	jsonContentType = "application/json"

	authorizationHeader = "Authorization"
	acceptHeader        = "Accept"
	contentTypeHeader   = "Content-Type"
	userAgentHeader     = "User-Agent"
	sdkHeader           = "X-TypeSafe-SDK"
	runtimeHeader       = "X-TypeSafe-Runtime"
	retryCountHeader    = "X-TypeSafe-Retry-Count"
	requestIDHeader     = "X-TypeSafe-Request-Id"
)

// protectedHeaders are set by the SDK after the caller's headers are applied,
// so a caller cannot replace authentication or content negotiation.
var protectedHeaders = []string{
	authorizationHeader, acceptHeader, contentTypeHeader,
	userAgentHeader, sdkHeader, runtimeHeader, retryCountHeader,
}

var (
	userAgent   = sdkName + "/" + buildVersion()
	runtimeInfo = fmt.Sprintf("go/%s (%s; %s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)
)

// buildVersion reports the SDK's module version, as recorded in the importing
// binary. It is "dev" for a local build.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	for _, dep := range info.Deps {
		if dep.Path == modulePath && dep.Version != "" {
			return dep.Version
		}
	}
	if info.Main.Path == modulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Client talks to the TypeSafe AI API. It is safe for concurrent use, and a
// single client should be shared: it reuses connections through its
// underlying [http.Client].
type Client struct {
	apiKey  string
	baseURL string
	model   string
	timeout time.Duration
	header  http.Header
	retry   RetryPolicy

	httpClient *http.Client
	transport  *transport.Client
}

// New creates a client. With no options it reads TYPESAFE_API_KEY from the
// environment and talks to the public API with the default retry policy.
//
//	client, err := jev.New()
//	if err != nil {
//	    return err
//	}
func New(opts ...ClientOption) (*Client, error) {
	cfg := clientConfig{
		apiKey:  os.Getenv(APIKeyEnv),
		baseURL: envOr(BaseURLEnv, DefaultBaseURL),
		model:   envOr(DefaultModelEnv, DefaultModel),
		timeout: DefaultTimeout,
		retry:   DefaultRetryPolicy(),
		logger:  slog.New(slog.DiscardHandler),
		header:  http.Header{},
	}

	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	apiKey, err := validateAPIKey(cfg.apiKey)
	if err != nil {
		return nil, err
	}
	baseURL, err := normalizeBaseURL(cfg.baseURL)
	if err != nil {
		return nil, err
	}
	if cfg.timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout must be positive, got %v", ErrInvalidTimeout, cfg.timeout)
	}
	if err := cfg.retry.validate(); err != nil {
		return nil, err
	}

	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      cfg.model,
		timeout:    cfg.timeout,
		header:     cfg.header,
		retry:      cfg.retry,
		httpClient: httpClient,
		transport: &transport.Client{
			HTTP:   httpClient,
			Logger: cfg.logger,
			Now:    time.Now,
			Sleep:  transport.Sleep,
		},
	}, nil
}

// CloseIdleConnections releases connections the client is holding open but not
// using. Calling it is optional: a client needs no teardown, and dropping one
// leaks nothing.
func (c *Client) CloseIdleConnections() {
	c.httpClient.CloseIdleConnections()
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// validateAPIKey trims the key and rejects anything that cannot be sent in an
// Authorization header. The key itself never appears in the error.
func validateAPIKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("%w: pass jev.WithAPIKey or set %s", ErrNoAPIKey, APIKeyEnv)
	}

	for _, r := range key {
		if r < '!' || r > '~' {
			return "", fmt.Errorf("%w: it must contain only printable ASCII characters and no whitespace", ErrInvalidAPIKey)
		}
	}
	return key, nil
}

// normalizeBaseURL trims the URL and strips trailing slashes so a path can be
// appended directly. Credentials are left in place; they are removed only from
// the endpoint labels that reach logs and errors.
func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidBaseURL, raw)
	}
	return trimmed, nil
}

// endpointLabel describes a request for logs and errors, without credentials,
// query parameters or fragment.
func endpointLabel(method, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return method
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return method + " " + parsed.String()
}

// buildHeader layers call headers over client headers, then stamps the
// protected headers so neither layer can displace them.
func (c *Client) buildHeader(call callConfig, hasBody bool) http.Header {
	header := c.header.Clone()
	if header == nil {
		header = http.Header{}
	}
	for name, values := range call.header {
		header[http.CanonicalHeaderKey(name)] = slices.Clone(values)
	}
	for _, name := range protectedHeaders {
		header.Del(name)
	}

	header.Set(authorizationHeader, "Bearer "+c.apiKey)
	header.Set(acceptHeader, jsonContentType)
	header.Set(userAgentHeader, userAgent)
	header.Set(sdkHeader, userAgent)
	header.Set(runtimeHeader, runtimeInfo)
	if hasBody {
		header.Set(contentTypeHeader, jsonContentType)
	}
	return header
}

// do sends one request and turns the outcome into either response metadata or
// a domain error.
func (c *Client) do(ctx context.Context, method, path string, body []byte, call callConfig) (ResponseMeta, error) {
	policy := c.retry
	if call.retry != nil {
		policy = *call.retry
	}
	if err := policy.validate(); err != nil {
		return ResponseMeta{}, err
	}

	timeout := c.timeout
	if call.timeout > 0 {
		timeout = call.timeout
	}

	requestURL := c.baseURL + path
	endpoint := endpointLabel(method, requestURL)

	req := transport.Request{
		Method:           method,
		URL:              requestURL,
		Endpoint:         endpoint,
		Header:           c.buildHeader(call, body != nil),
		Body:             body,
		Timeout:          timeout,
		RetryCountHeader: retryCountHeader,
		RequestIDHeader:  requestIDHeader,
	}

	resp, attempts, err := c.transport.Send(ctx, req, c.policy(policy, endpoint))
	if err != nil {
		return ResponseMeta{}, &TransportError{Endpoint: endpoint, Attempts: attempts, Err: err}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ResponseMeta{}, apiError(endpoint, resp)
	}

	return ResponseMeta{
		RequestID:  resp.Header.Get(requestIDHeader),
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Raw:        resp.Body,
	}, nil
}

// policy adapts a [RetryPolicy] into the plain hooks the transport needs, so
// the transport stays free of domain types.
func (c *Client) policy(policy RetryPolicy, endpoint string) transport.Policy {
	return transport.Policy{
		MaxAttempts: policy.MaxRetries + 1,
		Budget:      policy.Budget,

		Retry: func(resp *transport.Response, err error) bool {
			if err != nil {
				return policy.retryable(nil, err)
			}
			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				return false
			}
			return policy.retryable(apiError(endpoint, resp), nil)
		},

		Delay: func(attempt int, resp *transport.Response) time.Duration {
			var header http.Header
			if resp != nil {
				header = resp.Header
			}
			return policy.delay(attempt, header, rand.Float64(), time.Now())
		},
	}
}

func apiError(endpoint string, resp *transport.Response) *APIError {
	retryAfter, _ := parseRetryAfter(resp.Header, time.Now())

	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    extractMessage(resp.Body),
		Body:       resp.Body,
		Header:     resp.Header,
		RequestID:  resp.Header.Get(requestIDHeader),
		Endpoint:   endpoint,
		RetryAfter: retryAfter,
	}
}
