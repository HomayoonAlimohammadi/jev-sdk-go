package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// secretHeaders are redacted from log output by exact name. Any header whose
// name contains "token" or "secret" is redacted too.
var secretHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"cookie":              true,
	"set-cookie":          true,
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
	return secretHeaders[lowered] || strings.Contains(lowered, "token") || strings.Contains(lowered, "secret")
}

func (c *Client) logRequest(ctx context.Context, req Request, header http.Header) {
	if !c.Logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	c.Logger.Debug("jev: request",
		"method", req.Method,
		"url", req.URL,
		"headers", loggableHeader(header),
		"body", string(req.Body),
	)
}

func (c *Client) logResponse(ctx context.Context, req Request, resp *http.Response, body []byte, elapsed time.Duration) {
	var requestID string
	if req.RequestIDHeader != "" {
		requestID = resp.Header.Get(req.RequestIDHeader)
	}

	c.Logger.Info("jev: response",
		"method", req.Method,
		"url", req.URL,
		"status", resp.StatusCode,
		"duration", elapsed,
		"request_id", requestID,
	)

	if c.Logger.Enabled(ctx, slog.LevelDebug) {
		c.Logger.Debug("jev: response body",
			"method", req.Method,
			"url", req.URL,
			"headers", loggableHeader(resp.Header),
			"body", string(body),
		)
	}
}
