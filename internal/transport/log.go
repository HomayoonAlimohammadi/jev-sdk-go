package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// secretHeaders are redacted from log output by exact name.
var secretHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"cookie":              true,
	"set-cookie":          true,
}

// secretFragments redact by substring, so a header this SDK has never heard of
// is still covered. Callers set their own headers, and a denylist of exact
// names would miss X-Signature, Ocp-Apim-Subscription-Key and their kin.
var secretFragments = []string{
	"token", "secret", "key", "auth", "credential", "signature", "session", "password", "passwd", "pwd",
}

const redacted = "***"

// loggableHeader redacts credential-bearing headers. It is the single point
// where headers become log output, so no call site can leak one, and the
// redaction only runs when a handler actually wants the record.
type loggableHeader http.Header

var _ slog.LogValuer = loggableHeader{}

func (h loggableHeader) LogValue() slog.Value {
	attrs := make([]slog.Attr, 0, len(h))
	for name, values := range h {
		if isSecret(name) {
			attrs = append(attrs, slog.String(name, redacted))
			continue
		}
		attrs = append(attrs, slog.String(name, strings.Join(values, ", ")))
	}
	return slog.GroupValue(attrs...)
}

func isSecret(name string) bool {
	lowered := strings.ToLower(name)
	if secretHeaders[lowered] {
		return true
	}
	for _, fragment := range secretFragments {
		if strings.Contains(lowered, fragment) {
			return true
		}
	}
	return false
}

func (c *Client) logRequest(ctx context.Context, req Request, header http.Header) {
	if !c.Logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	attrs := []any{
		"method", req.Method,
		"url", req.SafeURL,
		"headers", loggableHeader(header),
	}
	if req.LogBodies {
		attrs = append(attrs, "body", string(req.Body))
	}
	c.Logger.Debug("jev: request", attrs...)
}

func (c *Client) logResponse(ctx context.Context, req Request, resp *http.Response, body []byte, elapsed time.Duration) {
	var requestID string
	if req.RequestIDHeader != "" {
		requestID = resp.Header.Get(req.RequestIDHeader)
	}

	c.Logger.Info("jev: response",
		"method", req.Method,
		"url", req.SafeURL,
		"status", resp.StatusCode,
		"duration", elapsed,
		"request_id", requestID,
	)

	if !c.Logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	attrs := []any{
		"method", req.Method,
		"url", req.SafeURL,
		"headers", loggableHeader(resp.Header),
	}
	if req.LogBodies {
		attrs = append(attrs, "body", string(body))
	}
	c.Logger.Debug("jev: response", attrs...)
}
