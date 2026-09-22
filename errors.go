package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxErrorBodyLength caps how much of an unrecognized error body is quoted
// back in an [APIError] message.
const maxErrorBodyLength = 200

// Configuration and input errors, all reported before any request is sent.
var (
	ErrNoAPIKey        = errors.New("jev: no API key provided")
	ErrInvalidAPIKey   = errors.New("jev: invalid API key")
	ErrInvalidTimeout  = errors.New("jev: invalid timeout")
	ErrInvalidRetry    = errors.New("jev: invalid retry policy")
	ErrInvalidBaseURL  = errors.New("jev: invalid base URL")
	ErrInvalidState    = errors.New("jev: invalid state")
	ErrNoQuestions     = errors.New("jev: at least one question is required")
	ErrInvalidQuestion = errors.New("jev: invalid question")
	ErrEncodeRequest   = errors.New("jev: request body could not be encoded as JSON")

	// ErrResponseTooLarge reports a response body over the limit set by
	// [WithMaxResponseBytes]. It arrives wrapped in a [*ResponseError].
	ErrResponseTooLarge = errors.New("jev: response body too large")
)

// Status categories. An [APIError] matches these through [errors.Is], so a
// caller can branch on the kind of failure without inspecting status codes:
//
//	if errors.Is(err, jev.ErrRateLimited) { ... }
var (
	ErrBadRequest    = errors.New("jev: bad request")
	ErrUnauthorized  = errors.New("jev: authentication failed")
	ErrForbidden     = errors.New("jev: permission denied")
	ErrNotFound      = errors.New("jev: not found")
	ErrUnprocessable = errors.New("jev: unprocessable entity")
	ErrRateLimited   = errors.New("jev: rate limited")
	ErrServer        = errors.New("jev: server error")
)

// APIError is an unsuccessful HTTP response from the API.
type APIError struct {
	// StatusCode is the HTTP response status.
	StatusCode int

	// Message is the server's explanation, extracted from the response body.
	Message string

	// Body is the raw response body.
	Body []byte

	// Header holds the response headers.
	Header http.Header

	// RequestID is the x-typesafe-request-id response header, if present.
	RequestID string

	// Endpoint is the request method and URL, without credentials, query
	// parameters or fragment.
	Endpoint string

	// RetryAfter is the delay the server asked for, from the retry-after or
	// retry-after-ms headers. It is zero when the server did not ask.
	RetryAfter time.Duration

	// Attempts is how many attempts were made, retries included, before this
	// response was returned.
	Attempts int
}

func (e *APIError) Error() string {
	var b strings.Builder
	if e.Endpoint != "" {
		b.WriteString(e.Endpoint)
		b.WriteString(": ")
	}
	b.WriteString(strconv.Itoa(e.StatusCode))
	if e.Message != "" {
		b.WriteByte(' ')
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request_id=")
		b.WriteString(e.RequestID)
		b.WriteByte(')')
	}
	return b.String()
}

// Is maps the response status onto the status category sentinels.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrBadRequest:
		return e.StatusCode == http.StatusBadRequest
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrUnprocessable:
		return e.StatusCode == http.StatusUnprocessableEntity
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	case ErrServer:
		return e.StatusCode >= http.StatusInternalServerError
	}
	return false
}

// TransportError is a request that never produced an HTTP response.
type TransportError struct {
	// Endpoint is the request method and URL, without credentials.
	Endpoint string

	// Attempts is how many attempts were made before giving up.
	Attempts int

	// Err is the underlying transport failure.
	Err error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("%s: %v", e.Endpoint, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// Timeout reports whether the request failed because it ran out of time,
// implementing the same convention as [net.Error].
func (e *TransportError) Timeout() bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &timeout) && timeout.Timeout())
}

// ResponseError is a successful HTTP response whose body was missing, or
// structurally invalid where the SDK requires data.
type ResponseError struct {
	// Field is a dotted path to the offending value, such as
	// "answers.tone.confidence" or "models[1].name". It is empty when the
	// whole body was unusable.
	Field string

	// StatusCode is the HTTP response status.
	StatusCode int

	// Body is the raw response body.
	Body []byte

	// Header holds the response headers.
	Header http.Header

	// RequestID is the x-typesafe-request-id response header, if present.
	RequestID string

	// Endpoint is the request method and URL, without credentials.
	Endpoint string

	// Err is the underlying decoding failure, if there was one.
	Err error
}

func (e *ResponseError) Error() string {
	var b strings.Builder
	if e.Endpoint != "" {
		b.WriteString(e.Endpoint)
		b.WriteString(": ")
	}
	b.WriteString(strconv.Itoa(e.StatusCode))
	if e.Field == "" {
		b.WriteString(" invalid response data")
	} else {
		fmt.Fprintf(&b, " invalid response data at %q", e.Field)
	}
	if e.RequestID != "" {
		b.WriteString(" (request_id=")
		b.WriteString(e.RequestID)
		b.WriteByte(')')
	}
	return b.String()
}

func (e *ResponseError) Unwrap() error { return e.Err }

// extractMessage pulls the server's explanation out of an error body, trying
// the shapes the API is known to produce before falling back to the body text.
func extractMessage(body []byte) string {
	if len(body) == 0 {
		return "status code (no body)"
	}

	if message := messageFromJSON(body); message != "" {
		return message
	}
	return truncate(strings.TrimSpace(string(body)), maxErrorBodyLength)
}

func messageFromJSON(body []byte) string {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}

	switch value := decoded.(type) {
	case string:
		return value
	case map[string]any:
		return messageFromObject(value)
	}
	return ""
}

// messageFromObject applies the server's message precedence: a top-level
// error, then message, then detail, each of which may be a string or carry a
// nested message. A detail array is a list of field validation failures.
func messageFromObject(body map[string]any) string {
	if text, ok := nested(body["error"]); ok {
		return text
	}
	if text, ok := body["message"].(string); ok {
		return text
	}
	if text, ok := nested(body["detail"]); ok {
		return text
	}
	if entries, ok := body["detail"].([]any); ok {
		return validationMessage(entries)
	}
	return ""
}

// nested accepts either a plain string or an object carrying a "message".
func nested(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case map[string]any:
		text, ok := value["message"].(string)
		return text, ok
	}
	return "", false
}

// validationMessage renders FastAPI-style validation entries as
// "questions.q.criteria: Field required", joined with "; ".
func validationMessage(entries []any) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		message, ok := object["msg"].(string)
		if !ok {
			continue
		}

		if path := locationPath(object["loc"]); path != "" {
			parts = append(parts, path+": "+message)
		} else {
			parts = append(parts, message)
		}
	}
	return strings.Join(parts, "; ")
}

// locationPath joins a validation error location, dropping the leading "body"
// segment that names the request location rather than a field.
func locationPath(location any) string {
	segments, ok := location.([]any)
	if !ok {
		return ""
	}

	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if text, ok := segment.(string); ok {
			if text == "body" {
				continue
			}
			parts = append(parts, text)
			continue
		}
		parts = append(parts, fmt.Sprint(segment))
	}
	return strings.Join(parts, ".")
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	// Back off to a rune boundary so the quoted body stays valid UTF-8.
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}
