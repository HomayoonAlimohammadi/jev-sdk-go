package jev

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Internal decoding failures. They never escape the package: each one is
// converted into a [ResponseError] carrying the offending field path.
var (
	errMissingField       = errors.New("missing or invalid field")
	errMissingAnswer      = errors.New("no answer for this question")
	errAnswerKindMismatch = errors.New("answer type does not match the question")
)

// ResponseMeta is the HTTP metadata attached to every response.
//
// Embedding it in a type passed to [SystemOneAs] opts that type in:
// the SDK fills it after decoding the body.
type ResponseMeta struct {
	// RequestID is the x-typesafe-request-id response header, if the server
	// sent one. Quote it in bug reports.
	RequestID string

	// StatusCode is the HTTP response status.
	StatusCode int

	// Header holds the response headers.
	Header http.Header

	// Raw is the untouched response body.
	Raw []byte
}

func (m *ResponseMeta) setMeta(meta ResponseMeta) { *m = meta }

// metaSetter is satisfied by any type embedding [ResponseMeta], including one
// declared in another package, because embedding promotes the method.
type metaSetter interface {
	setMeta(ResponseMeta)
}

// SystemOneResponse is the answers to one System One request.
//
// See https://docs.typesafe.ai/concepts/system-one.
type SystemOneResponse struct {
	ResponseMeta

	// Model is the model that answered. It may differ from the alias asked for.
	Model string

	// Usage reports the tokens the request consumed.
	Usage Usage

	// Answers holds every answer, keyed by the question name from the request.
	Answers map[string]Answer

	// Nouls, Choices and Scores are Answers split by kind, so a caller that
	// knows what it asked reaches its answer without a type assertion.
	Nouls   map[string]NoulAnswer
	Choices map[string]ChoiceAnswer
	Scores  map[string]ScoreAnswer
}

// ModelMetadata describes one model available to the account.
type ModelMetadata struct {
	// Name is accepted by [SystemOneRequest.Model].
	Name string `json:"name"`

	// Description explains the model and its capabilities.
	Description string `json:"description"`

	// ReleaseDate is formatted as YYYY-MM-DD.
	ReleaseDate string `json:"release_date"`
}

// ListModelsResponse is the models available to the account.
type ListModelsResponse struct {
	ResponseMeta

	Models []ModelMetadata
}

type systemOneWire struct {
	Model   *string               `json:"model"`
	Usage   *Usage                `json:"usage"`
	Answers map[string]answerWire `json:"answers"`
}

// decodeSystemOne parses a System One response body, reporting the first
// field that is missing or structurally wrong.
//
// Each answer is decoded exactly once. Where several things are wrong, the
// report follows a fixed precedence rather than map order: a malformed answer
// anywhere, then the top-level fields, then an unanswered question, then a
// field-level problem, and within each the lowest answer or question name.
func decodeSystemOne(meta ResponseMeta, questions map[string]Question, endpoint string) (*SystemOneResponse, error) {
	var wire systemOneWire
	if err := json.Unmarshal(meta.Raw, &wire); err != nil {
		return nil, meta.invalid(endpoint, jsonFieldPath(err), err)
	}

	if name, malformed := firstUntyped(wire.Answers); malformed {
		return nil, meta.invalid(endpoint, "answers."+name+".type", errMissingField)
	}
	if wire.Model == nil {
		return nil, meta.invalid(endpoint, "model", errMissingField)
	}
	if wire.Usage == nil {
		return nil, meta.invalid(endpoint, "usage", errMissingField)
	}

	// Every question asked must come back answered, and answered in kind. A map
	// lookup on a missing answer would otherwise yield a zero value, and a
	// NoulAnswer{} reads as a confident "no".
	if field, err := checkAnswered(questions, wire.Answers); err != nil {
		return nil, meta.invalid(endpoint, field, err)
	}

	answers, field, err := decodeAnswers(wire.Answers)
	if err != nil {
		return nil, meta.invalid(endpoint, field, err)
	}

	nouls, choices, scores := partition(answers)
	return &SystemOneResponse{
		ResponseMeta: meta,
		Model:        *wire.Model,
		Usage:        *wire.Usage,
		Answers:      answers,
		Nouls:        nouls,
		Choices:      choices,
		Scores:       scores,
	}, nil
}

// firstUntyped reports the lowest-named answer that carries no string "type",
// whether because the field is absent or because the answer is not an object.
func firstUntyped(answers map[string]answerWire) (string, bool) {
	var (
		found bool
		first string
	)
	for name, answer := range answers {
		if !answer.typed() && (!found || name < first) {
			found, first = true, name
		}
	}
	return first, found
}

// decodeAnswers converts every answer, reporting the field-level problem of
// the lowest-named answer that has one. An answer of a kind this SDK version
// does not model is kept as an [UnknownAnswer].
func decodeAnswers(wires map[string]answerWire) (map[string]Answer, string, error) {
	answers := make(map[string]Answer, len(wires))

	var (
		failed    bool
		firstName string
		firstPath string
		firstErr  error
	)
	for name, wire := range wires {
		if failed && name >= firstName {
			continue
		}

		answer, field, err := wire.decode()
		if err != nil {
			failed, firstName, firstPath, firstErr = true, name, joinPath("answers."+name, field), err
			continue
		}
		answers[name] = answer
	}

	if failed {
		return nil, firstPath, firstErr
	}
	return answers, "", nil
}

// checkAnswered reports the lowest-named question that went unanswered, or was
// answered by something other than what it asked for.
//
// An answer of a kind this SDK version does not model still counts as
// answered: it matches the question's type, and the payload is kept.
func checkAnswered(questions map[string]Question, answers map[string]answerWire) (string, error) {
	var (
		failed    bool
		firstName string
		firstPath string
		firstErr  error
	)
	for name, question := range questions {
		if failed && name >= firstName {
			continue
		}

		answer, answered := answers[name]
		switch {
		case !answered:
			failed, firstName, firstPath, firstErr = true, name, "answers."+name, errMissingAnswer
		case *answer.Type != questionKind(question):
			failed, firstName, firstPath, firstErr = true, name, "answers."+name+".type", errAnswerKindMismatch
		}
	}
	return firstPath, firstErr
}

type modelWire struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ReleaseDate *string `json:"release_date"`
}

type listModelsWire struct {
	Models *[]modelWire `json:"models"`
}

// decodeListModels parses a models listing, reporting the first missing field
// with its index, such as "models[1].name".
func decodeListModels(meta ResponseMeta, endpoint string) (*ListModelsResponse, error) {
	var wire listModelsWire
	if err := json.Unmarshal(meta.Raw, &wire); err != nil {
		return nil, meta.invalid(endpoint, jsonFieldPath(err), err)
	}
	if wire.Models == nil {
		return nil, meta.invalid(endpoint, "models", errMissingField)
	}

	models := make([]ModelMetadata, 0, len(*wire.Models))
	for i, model := range *wire.Models {
		var missing string
		switch {
		case model.Name == nil:
			missing = "name"
		case model.Description == nil:
			missing = "description"
		case model.ReleaseDate == nil:
			missing = "release_date"
		}
		if missing != "" {
			return nil, meta.invalid(endpoint, indexedPath("models", i, missing), errMissingField)
		}

		models = append(models, ModelMetadata{
			Name:        *model.Name,
			Description: *model.Description,
			ReleaseDate: *model.ReleaseDate,
		})
	}

	return &ListModelsResponse{ResponseMeta: meta, Models: models}, nil
}

func (m ResponseMeta) invalid(endpoint, field string, err error) *ResponseError {
	return &ResponseError{
		Field:      field,
		StatusCode: m.StatusCode,
		Body:       m.Raw,
		Header:     m.Header,
		RequestID:  m.RequestID,
		Endpoint:   endpoint,
		Err:        err,
	}
}

// jsonFieldPath recovers the dotted path encoding/json reports for a value of
// the wrong type. It is empty when the whole body was unusable.
func jsonFieldPath(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return typeErr.Field
	}
	return ""
}

func joinPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	if field == "" {
		return prefix
	}
	return prefix + "." + field
}

func indexedPath(prefix string, index int, field string) string {
	return joinPath(prefix+"["+strconv.Itoa(index)+"]", field)
}
