package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
)

// SystemOneRequest asks named questions about a piece of state.
//
// See https://docs.typesafe.ai/concepts/system-one.
type SystemOneRequest struct {
	// State is what every question in the request refers to: a string, a map,
	// a slice, a struct, or any other JSON-marshalable value.
	//
	// See https://docs.typesafe.ai/concepts/state.
	State any

	// Questions maps a name you choose to the question to ask. The answers
	// come back under the same names. At least one is required.
	Questions map[string]Question

	// Model overrides the client's default model for this request.
	Model string

	// Extra adds top-level fields to the request body, shallow-merged over it.
	// A key that collides with state, model or questions replaces it. It is
	// the escape hatch for body fields the API accepts before this SDK models
	// them.
	Extra map[string]any
}

// SystemOne answers the request's questions about its state.
//
//	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
//	    State: "I was charged twice. Please help.",
//	    Questions: map[string]jev.Question{
//	        "billing": jev.Noul{Instructions: "Is this about billing?"},
//	    },
//	})
//
// It returns [ErrNoQuestions] or [ErrInvalidQuestion] before sending anything
// if the request cannot be answered, an [*APIError] for an unsuccessful
// response, a [*TransportError] if the request never reached the API, and a
// [*ResponseError] if the response body was not usable.
func (c *Client) SystemOne(ctx context.Context, req SystemOneRequest, opts ...CallOption) (*SystemOneResponse, error) {
	meta, err := c.systemOne(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	return decodeSystemOne(meta, endpointLabel(http.MethodPost, c.baseURL+systemOnePath), c.transport.Logger)
}

// SystemOneAs answers the request and decodes the response body into T instead
// of [SystemOneResponse].
//
// The body is decoded as the API sends it, so T mirrors the wire shape:
//
//	type myAnswers struct {
//	    jev.ResponseMeta // optional: filled in with the request ID and headers
//
//	    Answers struct {
//	        Billing jev.NoulAnswer   `json:"billing"`
//	        Tone    jev.ChoiceAnswer `json:"tone"`
//	    } `json:"answers"`
//	}
//
//	answers, err := client.SystemOneAs[myAnswers](ctx, req)
//
// Unlike [Client.SystemOne], missing fields are not reported: encoding/json
// leaves them at their zero value. Use pointer fields where absence matters.
func (c *Client) SystemOneAs[T any](ctx context.Context, req SystemOneRequest, opts ...CallOption) (*T, error) {
	meta, err := c.systemOne(ctx, req, opts)
	if err != nil {
		return nil, err
	}

	out := new(T)
	if err := json.Unmarshal(meta.Raw, out); err != nil {
		endpoint := endpointLabel(http.MethodPost, c.baseURL+systemOnePath)
		return nil, meta.invalid(endpoint, jsonFieldPath(err), err)
	}

	// Opt-in metadata: a T that embeds ResponseMeta gets it filled in.
	if setter, ok := any(out).(metaSetter); ok {
		setter.setMeta(meta)
	}
	return out, nil
}

func (c *Client) systemOne(ctx context.Context, req SystemOneRequest, opts []CallOption) (ResponseMeta, error) {
	if err := validateQuestions(req.Questions); err != nil {
		return ResponseMeta{}, err
	}

	call, err := applyCallOptions(opts)
	if err != nil {
		return ResponseMeta{}, err
	}

	// State is encoded once here: the result is checked against the shapes the
	// API accepts, then embedded verbatim rather than marshaled a second time.
	state, err := json.Marshal(req.State)
	if err != nil {
		return ResponseMeta{}, fmt.Errorf("%w: state: %w", ErrEncodeRequest, err)
	}
	if err := validateState(state); err != nil {
		return ResponseMeta{}, err
	}

	model := req.Model
	if model == "" {
		model = c.model
	}

	body := map[string]any{
		"state":     json.RawMessage(state),
		"model":     model,
		"questions": req.Questions,
	}
	maps.Copy(body, req.Extra)

	encoded, err := json.Marshal(body)
	if err != nil {
		return ResponseMeta{}, fmt.Errorf("%w: %w", ErrEncodeRequest, err)
	}

	return c.do(ctx, http.MethodPost, systemOnePath, encoded, call)
}

// validateState rejects a state the API will refuse. It must encode to a
// string, object or array; a bare number, boolean or null is not content.
func validateState(encoded []byte) error {
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) > 0 {
		switch trimmed[0] {
		case '"', '{', '[':
			return nil
		}
	}
	return fmt.Errorf("%w: it must be a string, object or array, got %s", ErrInvalidState, trimmed)
}
