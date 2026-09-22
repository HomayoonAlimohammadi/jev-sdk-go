package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder captures the requests a test's handler sees.
type recorder struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
}

func (r *recorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	// Put the body back so the handler under test can still read it.
	req.Body = io.NopCloser(bytes.NewReader(body))

	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.Clone(context.Background()))
	r.bodies = append(r.bodies, string(body))
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recorder) last() (*http.Request, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.requests)
	return r.requests[n-1], r.bodies[n-1]
}

// newTestClient starts a server running handler and returns a client pointed
// at it, with retries off unless a test opts back in.
func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...ClientOption) (*Client, *recorder) {
	t.Helper()

	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	base := []ClientOption{WithAPIKey("test-key"), WithBaseURL(server.URL), WithRetry(RetryPolicy{})}
	client, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, rec
}

func respondJSON(status int, body string, headers ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.Header().Set("Content-Type", jsonContentType)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

func noulRequest() SystemOneRequest {
	return SystemOneRequest{
		State:     "hello",
		Questions: map[string]Question{"q": Noul{Instructions: "?"}},
	}
}

func TestSystemOneRoundTrip(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody, requestIDHeader, "req-42"))

	resp, err := client.SystemOne(t.Context(), SystemOneRequest{
		State: map[string]any{"document": "Hello ἰ෍"},
		Questions: map[string]Question{
			"spam":    Noul{Instructions: "Spam?"},
			"tone":    Choice{Instructions: "Tone?", Criteria: map[string]any{"friendly": nil, "hostile": nil}},
			"quality": Score{Instructions: "Quality?", Criteria: []any{"bad", "ok", "great"}},
		},
	})
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	req, body := rec.last()
	if req.Method != http.MethodPost || req.URL.Path != systemOnePath {
		t.Errorf("request = %s %s, want POST %s", req.Method, req.URL.Path, systemOnePath)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if sent["model"] != DefaultModel {
		t.Errorf("model = %v, want %s", sent["model"], DefaultModel)
	}
	questions := sent["questions"].(map[string]any)
	if got := questions["quality"].(map[string]any)["type"]; got != "score" {
		t.Errorf("question type = %v, want score", got)
	}

	if resp.RequestID != "req-42" {
		t.Errorf("RequestID = %q, want req-42", resp.RequestID)
	}
	if resp.Nouls["spam"].Noul != 0.98 || resp.Choices["tone"].Choice != "friendly" || resp.Scores["quality"].Score != 1.7 {
		t.Errorf("answers not decoded: %+v", resp)
	}
}

func TestSystemOneValidatesBeforeSending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  SystemOneRequest
		want error
	}{
		{"no questions", SystemOneRequest{State: "x"}, ErrNoQuestions},
		{"empty score", SystemOneRequest{State: "x", Questions: map[string]Question{"r": Score{}}}, ErrInvalidQuestion},
		{
			"unencodable extra",
			SystemOneRequest{State: "x", Questions: map[string]Question{"q": Noul{}}, Extra: map[string]any{"bad": make(chan int)}},
			ErrEncodeRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody))
			_, err := client.SystemOne(t.Context(), tt.req)

			if !errors.Is(err, tt.want) {
				t.Errorf("SystemOne() error = %v, want %v", err, tt.want)
			}
			if rec.count() != 0 {
				t.Errorf("an invalid request reached the network")
			}
		})
	}
}

func TestSystemOneExtraShallowMerge(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody))

	_, err := client.SystemOne(t.Context(), SystemOneRequest{
		State:     "hi",
		Questions: map[string]Question{"q": Noul{Instructions: "?"}},
		Model:     "call-model",
		Extra:     map[string]any{"model": "override-model", "beam_width": 4, "nullable": nil},
	})
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	_, body := rec.last()
	var sent map[string]any
	json.Unmarshal([]byte(body), &sent)

	// Extra wins over the fields the SDK sets, and a nil value is sent as null.
	if sent["model"] != "override-model" {
		t.Errorf("model = %v, want override-model", sent["model"])
	}
	if sent["beam_width"] != 4.0 {
		t.Errorf("beam_width = %v, want 4", sent["beam_width"])
	}
	if value, ok := sent["nullable"]; !ok || value != nil {
		t.Errorf("nullable = %v (present %v), want an explicit null", value, ok)
	}
}

func TestSystemOneModelPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		clientModel string
		callModel   string
		want        string
	}{
		{"default", "", "", DefaultModel},
		{"client", "client-model", "", "client-model"},
		{"request overrides client", "client-model", "request-model", "request-model"},
		{"request without client", "", "request-model", "request-model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []ClientOption
			if tt.clientModel != "" {
				opts = append(opts, WithModel(tt.clientModel))
			}
			client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody), opts...)

			req := noulRequest()
			req.Model = tt.callModel
			if _, err := client.SystemOne(t.Context(), req); err != nil {
				t.Fatalf("SystemOne() error = %v", err)
			}

			_, body := rec.last()
			var sent map[string]any
			json.Unmarshal([]byte(body), &sent)
			if sent["model"] != tt.want {
				t.Errorf("model = %v, want %v", sent["model"], tt.want)
			}
		})
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	body := `{"models":[{"name":"jev-latest","description":"Fast model","release_date":"2026-08-01"}]}`
	client, rec := newTestClient(t, respondJSON(http.StatusOK, body))

	resp, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	req, sent := rec.last()
	if req.Method != http.MethodGet || req.URL.Path != modelsPath {
		t.Errorf("request = %s %s, want GET %s", req.Method, req.URL.Path, modelsPath)
	}
	if sent != "" {
		t.Errorf("GET carried a body: %q", sent)
	}
	if req.Header.Get(contentTypeHeader) != "" {
		t.Errorf("GET carried a Content-Type header")
	}

	if len(resp.Models) != 1 || resp.Models[0].Name != "jev-latest" {
		t.Errorf("Models = %+v", resp.Models)
	}
}

func TestProtectedHeaders(t *testing.T) {
	t.Parallel()

	injected := []string{
		authorizationHeader, "injected-secret",
		acceptHeader, "text/plain",
		userAgentHeader, "wrong",
		sdkHeader, "wrong",
		runtimeHeader, "wrong",
		contentTypeHeader, "text/plain",
		retryCountHeader, "99",
	}

	opts := []ClientOption{WithHeader("X-Team", "default"), WithHeader("X-Default", "kept")}
	for i := 0; i+1 < len(injected); i += 2 {
		opts = append(opts, WithHeader(injected[i], injected[i+1]))
	}
	client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody), opts...)

	callOpts := []CallOption{WithCallHeader("X-Team", "call")}
	for i := 0; i+1 < len(injected); i += 2 {
		callOpts = append(callOpts, WithCallHeader(injected[i], injected[i+1]))
	}
	if _, err := client.SystemOne(t.Context(), noulRequest(), callOpts...); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	req, _ := rec.last()
	checks := map[string]string{
		authorizationHeader: "Bearer test-key",
		acceptHeader:        jsonContentType,
		contentTypeHeader:   jsonContentType,
		userAgentHeader:     userAgent,
		sdkHeader:           userAgent,
		"X-Team":            "call", // a call header overrides a client header
		"X-Default":         "kept", // a client header survives
	}
	for name, want := range checks {
		if got := req.Header.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	if got := req.Header.Get(runtimeHeader); !strings.HasPrefix(got, "go/") {
		t.Errorf("header %s = %q, want a go runtime string", runtimeHeader, got)
	}
	if got := req.Header.Get(retryCountHeader); got != "" {
		t.Errorf("header %s = %q, want it absent on the first attempt", retryCountHeader, got)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status   int
		sentinel error
	}{
		{http.StatusBadRequest, ErrBadRequest},
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusUnprocessableEntity, ErrUnprocessable},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusInternalServerError, ErrServer},
		{http.StatusServiceUnavailable, ErrServer},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			t.Parallel()

			body := `{"detail":{"message":"Server explanation"}}`
			client, _ := newTestClient(t, respondJSON(tt.status, body, requestIDHeader, "req_123", retryAfterMsHeader, "125"))

			_, err := client.ListModels(t.Context())

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("ListModels() error = %v, want *APIError", err)
			}
			if !errors.Is(err, tt.sentinel) {
				t.Errorf("errors.Is(err, %v) = false", tt.sentinel)
			}
			if apiErr.StatusCode != tt.status || apiErr.Message != "Server explanation" {
				t.Errorf("APIError = %+v", apiErr)
			}
			if apiErr.RequestID != "req_123" || apiErr.RetryAfter != 125*time.Millisecond {
				t.Errorf("APIError metadata = %+v", apiErr)
			}
			if string(apiErr.Body) != body {
				t.Errorf("APIError.Body = %q, want the raw body", apiErr.Body)
			}
			if !strings.HasSuffix(apiErr.Endpoint, modelsPath) || !strings.HasPrefix(apiErr.Endpoint, "GET ") {
				t.Errorf("APIError.Endpoint = %q", apiErr.Endpoint)
			}
		})
	}
}

func TestTransportErrorReported(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := New(WithAPIKey("k"), WithBaseURL(url), WithRetry(RetryPolicy{MaxRetries: 1, RetryTransport: true}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ListModels(t.Context())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("ListModels() error = %v, want *TransportError", err)
	}
	if transportErr.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", transportErr.Attempts)
	}
	if transportErr.Timeout() {
		t.Errorf("Timeout() = true, want false for a refused connection")
	}
}

func TestPerAttemptTimeoutReportedAsTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	}, WithTimeout(50*time.Millisecond))
	t.Cleanup(func() { close(release) })

	_, err := client.ListModels(t.Context())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("ListModels() error = %v, want *TransportError", err)
	}
	if !transportErr.Timeout() {
		t.Errorf("Timeout() = false, want true")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false")
	}
}

func TestRetryRecoversAndCountsAttempts(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.Header().Set(retryAfterMsHeader, "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		respondJSON(http.StatusOK, systemOneBody)(w, r)
	}, WithRetry(DefaultRetryPolicy()))

	resp, err := client.SystemOne(t.Context(), noulRequest())
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}
	if resp.Model != "jev-latest" {
		t.Errorf("Model = %q", resp.Model)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.requests) != 3 {
		t.Fatalf("made %d attempts, want 3", len(rec.requests))
	}
	for i, want := range []string{"", "1", "2"} {
		if got := rec.requests[i].Header.Get(retryCountHeader); got != want {
			t.Errorf("attempt %d retry count = %q, want %q", i+1, got, want)
		}
	}
	for i, body := range rec.bodies {
		if body != rec.bodies[0] {
			t.Errorf("attempt %d body differs from the first", i+1)
		}
	}
}

func TestPerCallOverridesDoNotLeak(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(retryAfterMsHeader, "0")
		w.WriteHeader(http.StatusConflict)
	}, WithRetry(RetryPolicy{}))

	// 409 is not retried by default; the call policy opts it in for one call.
	callPolicy := RetryPolicy{MaxRetries: 2, RetryStatuses: []int{http.StatusConflict}}
	if _, err := client.ListModels(t.Context(), WithCallRetry(callPolicy), WithCallHeader("X-Call", "one")); err == nil {
		t.Fatal("ListModels() error = nil, want an API error")
	}
	if got := rec.count(); got != 3 {
		t.Fatalf("overridden call made %d attempts, want 3", got)
	}

	if _, err := client.ListModels(t.Context()); err == nil {
		t.Fatal("ListModels() error = nil, want an API error")
	}
	if got := rec.count(); got != 4 {
		t.Errorf("made %d attempts in total, want 4: the override must not persist", got)
	}

	req, _ := rec.last()
	if req.Header.Get("X-Call") != "" {
		t.Errorf("a call header leaked into the next call")
	}
}

func TestConcurrentCalls(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sent map[string]any
		json.Unmarshal(body, &sent)
		respondJSON(http.StatusOK, `{"model":"`+sent["model"].(string)+`","usage":{},"answers":{}}`)(w, r)
	})

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			model := "model-" + string(rune('a'+i))
			req := noulRequest()
			req.Model = model

			resp, err := client.SystemOne(t.Context(), req)
			if err != nil {
				t.Errorf("SystemOne() error = %v", err)
				return
			}
			if resp.Model != model {
				t.Errorf("Model = %q, want %q: responses crossed calls", resp.Model, model)
			}
		}()
	}
	wg.Wait()
}

func TestSystemOneAs(t *testing.T) {
	t.Parallel()

	type answers struct {
		ResponseMeta

		Model   string `json:"model"`
		Answers struct {
			Spam NoulAnswer   `json:"spam"`
			Tone ChoiceAnswer `json:"tone"`
		} `json:"answers"`
	}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, systemOneBody, requestIDHeader, "req-typed"))

	got, err := client.SystemOneAs[answers](t.Context(), noulRequest())
	if err != nil {
		t.Fatalf("SystemOneAs() error = %v", err)
	}

	if got.Model != "jev-latest" || got.Answers.Spam.Noul != 0.98 || got.Answers.Tone.Choice != "friendly" {
		t.Errorf("decoded = %+v", got)
	}
	// An embedded ResponseMeta is filled in.
	if got.RequestID != "req-typed" || got.StatusCode != http.StatusOK {
		t.Errorf("ResponseMeta = %+v, want it populated", got.ResponseMeta)
	}
}

func TestSystemOneAsWithoutMeta(t *testing.T) {
	t.Parallel()

	type answers struct {
		Model string `json:"model"`
	}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, systemOneBody))

	got, err := client.SystemOneAs[answers](t.Context(), noulRequest())
	if err != nil {
		t.Fatalf("SystemOneAs() error = %v", err)
	}
	if got.Model != "jev-latest" {
		t.Errorf("Model = %q", got.Model)
	}
}

func TestSystemOneAsPreservesAPIErrors(t *testing.T) {
	t.Parallel()

	type answers struct {
		Model string `json:"model"`
	}

	client, _ := newTestClient(t, respondJSON(http.StatusBadRequest, `{"detail":"Invalid request"}`, requestIDHeader, "req-error"))

	_, err := client.SystemOneAs[answers](t.Context(), noulRequest())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("SystemOneAs() error = %v, want *APIError", err)
	}
	if apiErr.Message != "Invalid request" || apiErr.RequestID != "req-error" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestSystemOneAsReportsDecodeFailure(t *testing.T) {
	t.Parallel()

	type answers struct {
		Model int `json:"model"` // the API sends a string
	}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, systemOneBody))

	_, err := client.SystemOneAs[answers](t.Context(), noulRequest())

	var responseErr *ResponseError
	if !errors.As(err, &responseErr) {
		t.Fatalf("SystemOneAs() error = %v, want *ResponseError", err)
	}
	if responseErr.Field != "model" {
		t.Errorf("Field = %q, want model", responseErr.Field)
	}
}

func TestClientSatisfiesNarrowInterface(t *testing.T) {
	t.Parallel()

	// A caller's own interface can hold the plain calls, which keeps the
	// client mockable. A generic method could never appear here.
	type api interface {
		SystemOne(context.Context, SystemOneRequest, ...CallOption) (*SystemOneResponse, error)
		ListModels(context.Context, ...CallOption) (*ListModelsResponse, error)
	}

	var _ api = (*Client)(nil)
}

func TestCallTimeoutOverride(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	}, WithTimeout(time.Minute))
	t.Cleanup(func() { close(release) })

	// The client's minute-long timeout would hang the test; the call's wins.
	start := time.Now()
	_, err := client.ListModels(t.Context(), WithCallTimeout(50*time.Millisecond))

	var transportErr *TransportError
	if !errors.As(err, &transportErr) || !transportErr.Timeout() {
		t.Fatalf("ListModels() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("call took %v, want the per-call timeout to apply", elapsed)
	}
}

func TestCallTimeoutZeroInherits(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"models":[]}`))

	if _, err := client.ListModels(t.Context(), WithCallTimeout(0)); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if rec.count() != 1 {
		t.Errorf("made %d requests, want 1", rec.count())
	}
}

func TestCallOptionErrors(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, systemOneBody))

	for _, opt := range []CallOption{WithCallTimeout(-time.Second)} {
		if _, err := client.ListModels(t.Context(), opt); !errors.Is(err, ErrInvalidTimeout) {
			t.Errorf("ListModels() error = %v, want ErrInvalidTimeout", err)
		}
		if _, err := client.SystemOne(t.Context(), noulRequest(), opt); !errors.Is(err, ErrInvalidTimeout) {
			t.Errorf("SystemOne() error = %v, want ErrInvalidTimeout", err)
		}
	}
	if rec.count() != 0 {
		t.Errorf("an invalid call option reached the network")
	}
}

func TestInvalidCallRetryPolicyRejected(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"models":[]}`))

	_, err := client.ListModels(t.Context(), WithCallRetry(RetryPolicy{BackoffJitter: 2}))
	if !errors.Is(err, ErrInvalidRetry) {
		t.Errorf("ListModels() error = %v, want ErrInvalidRetry", err)
	}
	if rec.count() != 0 {
		t.Errorf("an invalid retry policy reached the network")
	}
}

func TestCloseIdleConnections(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"models":[]}`))

	if _, err := client.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	client.CloseIdleConnections()

	// The client stays usable afterwards.
	if _, err := client.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() after CloseIdleConnections error = %v", err)
	}
}

func TestResponseErrorUnwrapsCause(t *testing.T) {
	t.Parallel()

	client, _ := newTestClient(t, respondJSON(http.StatusOK, `{"model":42,"usage":{}}`))

	_, err := client.SystemOne(t.Context(), noulRequest())

	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("SystemOne() error = %v, want it to unwrap to a json error", err)
	}
}
