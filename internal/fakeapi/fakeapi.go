// Package fakeapi is an in-process stand-in for the System One API. It answers
// whatever questions it is sent, in the documented wire format, so code that
// uses the SDK can run without a network or an API key.
//
// Its answers are deterministic but vary with the question and the state, so
// code that branches on them sees more than one branch. It is test support:
// nothing but tests should import it.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Request is one request the fake received.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// Server is a running fake. Pass its URL to jev.WithBaseURL; any path prefix
// on it is accepted, as a gateway's would be.
type Server struct {
	*httptest.Server

	openRouter bool

	mu       sync.Mutex
	failures []failure
	requests []Request
}

type failure struct {
	status     int
	retryAfter string
}

// Option configures a fake.
type Option func(*Server)

// OpenRouter makes the fake answer the way OpenRouter's System One endpoint
// does: each response also carries id, provider and usage.cost, no
// x-typesafe-request-id header is sent, and /v1/models returns OpenRouter's
// own catalog rather than TypeSafe's model list.
func OpenRouter() Option {
	return func(s *Server) { s.openRouter = true }
}

// FailFirst makes the first n requests fail with status before the fake
// answers normally. A non-empty retryAfter is sent as the Retry-After header.
func FailFirst(n, status int, retryAfter string) Option {
	return func(s *Server) {
		for range n {
			s.failures = append(s.failures, failure{status: status, retryAfter: retryAfter})
		}
	}
}

// New starts a fake that is closed when the test ends.
func New(tb testing.TB, opts ...Option) *Server {
	tb.Helper()

	s := &Server{}
	for _, opt := range opts {
		opt(s)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	tb.Cleanup(s.Close)
	return s
}

// Requests returns every request received so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	sequence := len(s.requests)
	var injected *failure
	if len(s.failures) > 0 {
		injected = &s.failures[0]
		s.failures = s.failures[1:]
	}
	s.mu.Unlock()

	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); !ok || token == "" {
		writeError(w, http.StatusUnauthorized, "missing or empty bearer token")
		return
	}
	if injected != nil {
		if injected.retryAfter != "" {
			w.Header().Set("Retry-After", injected.retryAfter)
		}
		writeError(w, injected.status, "injected failure")
		return
	}

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/systemone"):
		s.systemOne(w, body, sequence)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v1/models"):
		s.models(w)
	default:
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) systemOne(w http.ResponseWriter, body []byte, sequence int) {
	var req struct {
		State     json.RawMessage            `json:"state"`
		Model     string                     `json:"model"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": []any{map[string]any{"loc": []any{"body", "questions"}, "msg": "At least one question is required", "type": "missing"}},
		})
		return
	}

	answers := make(map[string]any, len(req.Questions))
	for name, question := range req.Questions {
		answers[name] = answer(name, req.State, question)
	}

	usage := map[string]any{"input_tokens": len(req.State)/4 + 1, "output_tokens": len(req.Questions)}
	response := map[string]any{"model": req.Model, "usage": usage, "answers": answers}

	if s.openRouter {
		response["id"] = fmt.Sprintf("gen-fake-%d", sequence)
		response["provider"] = "TypeSafe"
		usage["cost"] = 0.000042
	} else {
		w.Header().Set("X-TypeSafe-Request-Id", fmt.Sprintf("req-fake-%d", sequence))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) models(w http.ResponseWriter) {
	if s.openRouter {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":        []any{map[string]any{"id": "typesafe/jev-1.13", "name": "TypeSafe: Jev 1.13"}},
			"total_count": 1,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models": []any{map[string]any{"name": "jev-latest", "description": "Fake System One model.", "release_date": "2026-09-15"}},
	})
}

// answer builds a well-formed answer of the question's kind. A kind the fake
// does not know comes back with a payload of its own, as a newer API would.
func answer(name string, state, raw json.RawMessage) map[string]any {
	var question struct {
		Type     string          `json:"type"`
		Criteria json.RawMessage `json:"criteria"`
	}
	_ = json.Unmarshal(raw, &question)

	seed := seedOf(name, state)

	switch question.Type {
	case "noul":
		return map[string]any{"type": "noul", "noul": round(0.05 + 0.9*seed)}

	case "choice":
		var criteria map[string]json.RawMessage
		_ = json.Unmarshal(question.Criteria, &criteria)
		labels := slices.Sorted(maps.Keys(criteria))
		if len(labels) == 0 {
			break
		}
		winner := int(seed * float64(len(labels)))
		probabilities := spread(labels, winner)
		return map[string]any{
			"type":          "choice",
			"choice":        labels[winner],
			"confidence":    round(0.35 + 0.6*seed),
			"probabilities": probabilities,
		}

	case "score":
		var levels []json.RawMessage
		_ = json.Unmarshal(question.Criteria, &levels)
		if len(levels) == 0 {
			break
		}
		keys := make([]string, len(levels))
		legend := make(map[string]json.RawMessage, len(levels))
		for i, level := range levels {
			keys[i] = strconv.Itoa(i)
			legend[keys[i]] = level
		}
		peak := int(seed * float64(len(levels)))
		probabilities := spread(keys, peak)

		var expected float64
		for i, key := range keys {
			expected += float64(i) * probabilities[key]
		}
		return map[string]any{
			"type":          "score",
			"score":         round(expected),
			"confidence":    round(0.4 + 0.55*seed),
			"legend":        legend,
			"probabilities": probabilities,
		}
	}

	return map[string]any{"type": question.Type, "value": 1}
}

// spread gives the winner most of the probability and shares the rest evenly,
// so the distribution sums to 1.
func spread(keys []string, winner int) map[string]float64 {
	probabilities := make(map[string]float64, len(keys))
	if len(keys) == 1 {
		probabilities[keys[0]] = 1
		return probabilities
	}

	rest := 0.3 / float64(len(keys)-1)
	for i, key := range keys {
		probabilities[key] = rest
		if i == winner {
			probabilities[key] = 0.7
		}
	}
	return probabilities
}

// seedOf maps a question and its state to a stable value in [0, 1).
func seedOf(name string, state json.RawMessage) float64 {
	h := fnv.New32a()
	// A hash.Hash's Write never returns an error.
	_, _ = h.Write([]byte(name))
	_, _ = h.Write(state)
	return float64(h.Sum32()%1000) / 1000
}

func round(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
