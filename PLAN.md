# jev-sdk-go — Implementation Plan

A Go SDK for the [TypeSafe AI](https://typesafe.ai) API, ported from
[`typesafe-sdk-python`](https://github.com/typesafe-ai/typesafe-sdk-python) v0.7.1.

This is a **behavioural** port, not a structural one. The Python SDK's shape is driven by
pydantic, httpx, tenacity and the sync/async split — none of which have a place in Go. What
carries over is the wire protocol, the config resolution rules, the retry semantics, the error
taxonomy and the response validation guarantees. Everything else is redesigned.

---

## 1. Fixed decisions

| Decision | Value | Rationale |
|---|---|---|
| Module path | `github.com/HomayoonAlimohammadi/jev-sdk-go` | Matches the repo. |
| Package name | `jev` | Repo is `jev-sdk-go`; `-go` suffix is dropped by convention. Gives non-stuttering `jev.Client`, `jev.APIError`, `jev.Noul`. |
| Minimum Go | **1.27** | Required by `(*Client).SystemOneAs[T]` — generic methods on concrete types landed in 1.27. See §10 risk 1. |
| Dependencies | **zero** (stdlib only) | `net/http`, `encoding/json`, `log/slog`, `math/rand/v2`, `errors`. A client SDK that drags in a dependency tree is a liability for its consumers. |
| Concurrency model | one `*jev.Client`, `context.Context` on every call, safe for concurrent use | Collapses the Python sync/async duplication (~500 lines) into a single path. |
| Custom response decoding | `(*Client).SystemOneAs[T]` + `resp.Raw` | Chosen replacement for pydantic `response_model`. |
| JSON library | `encoding/json` (v1), **not** `encoding/json/v2` | v2's `omitempty` drops empty strings/slices regardless of nilness. The wire protocol distinguishes "field absent" from "field explicitly empty" (`Noul(instructions="")` must serialize as `{"instructions":""}`), and only v1's `omitempty`-on-`any` semantics preserve that. Verified against go1.27. |
| Env var names | `TYPESAFE_*` (unchanged) | Same service; a user running both SDKs must not need two sets of credentials. |

---

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  package jev — domain layer (the public API)                 │
│                                                              │
│  Client · SystemOne · SystemOneAs[T] · ListModels            │
│  Question / Answer / Response types                          │
│  RetryPolicy · APIError · TransportError · ResponseError     │
│  config resolution (env + options), question validation      │
└───────────────────────────┬──────────────────────────────────┘
                            │  domain structs  ->  wire bytes
                            │  HTTP result     ->  domain errors
┌───────────────────────────▼──────────────────────────────────┐
│  internal/transport — HTTP mechanics                         │
│                                                              │
│  request construction · protected-header merge               │
│  attempt loop: backoff, jitter, budget, Retry-After          │
│  body replay across attempts · response draining             │
│  secret redaction · slog emission                            │
└───────────────────────────┬──────────────────────────────────┘
                            │
                     net/http.Client
```

**Layer rule:** `internal/transport` knows nothing about nouls, choices or scores. It moves bytes
and decides *whether to try again*, parameterised by plain function values supplied by `jev`
(`shouldRetry func(status int, err error) bool`, `delay func(attempt int, hdr http.Header) time.Duration`).
This keeps `RetryPolicy` public in `jev` without creating an import cycle.

### File layout

```
go.mod                      module github.com/HomayoonAlimohammadi/jev-sdk-go, go 1.27
doc.go                      package overview, quickstart
client.go                   Client, New, config resolution, endpoint plumbing
options.go                  ClientOption, CallOption
systemone.go                SystemOneRequest, SystemOne, SystemOneAs[T]
models.go                   ListModels, ModelMetadata, ListModelsResponse
question.go                 Question, Noul, Choice, Score, RawQuestion, validation
answer.go                   Answer, NoulAnswer, ChoiceAnswer, ScoreAnswer, Usage, decoding
response.go                 SystemOneResponse, ResponseMeta
errors.go                   sentinels, APIError, TransportError, ResponseError, message extraction
retry.go                    RetryPolicy, DefaultRetryPolicy, backoff, Retry-After parsing
example_test.go             runnable Example functions (replaces Python's sybil doctests)
internal/transport/
    request.go              Request value, header merge, body replay
    send.go                 attempt loop
    redact.go               secret-header redaction, URL userinfo stripping
    log.go                  slog call sites
examples/
    basic/main.go
    concurrent/main.go
```

---

## 3. Public API

```go
package jev // import "github.com/HomayoonAlimohammadi/jev-sdk-go"

// ---------- construction ----------

func New(opts ...ClientOption) (*Client, error)

type Client struct{ /* unexported */ }

func (c *Client) CloseIdleConnections()

type ClientOption func(*clientConfig) error

func WithAPIKey(key string) ClientOption
func WithBaseURL(url string) ClientOption
func WithModel(model string) ClientOption
func WithTimeout(d time.Duration) ClientOption      // per-attempt deadline
func WithRetry(p RetryPolicy) ClientOption
func WithHeader(name, value string) ClientOption    // repeatable
func WithHTTPClient(hc *http.Client) ClientOption   // full transport control
func WithLogger(l *slog.Logger) ClientOption

// ---------- calls ----------

type SystemOneRequest struct {
    State     any                 // string | map | slice | struct | json.RawMessage
    Questions map[string]Question // required, non-empty
    Model     string              // "" inherits the client default
    Extra     map[string]any      // shallow-merged over the body, last write wins
}

func (c *Client) SystemOne(ctx context.Context, req SystemOneRequest, opts ...CallOption) (*SystemOneResponse, error)
func (c *Client) SystemOneAs[T any](ctx context.Context, req SystemOneRequest, opts ...CallOption) (*T, error)
func (c *Client) ListModels(ctx context.Context, opts ...CallOption) (*ListModelsResponse, error)

type CallOption func(*callConfig)

func WithCallRetry(p RetryPolicy) CallOption
func WithCallTimeout(d time.Duration) CallOption
func WithCallHeader(name, value string) CallOption

// ---------- questions ----------

// Question is a sealed union: only Noul, Choice, Score and RawQuestion implement it.
type Question interface{ question() }

type Noul struct {
    Instructions any           `json:"instructions,omitempty"`
    Criteria     *NoulCriteria `json:"criteria,omitempty"`
}

type NoulCriteria struct {
    True  any `json:"true,omitempty"`
    False any `json:"false,omitempty"`
}

type Choice struct {
    Instructions any            `json:"instructions,omitempty"`
    Criteria     map[string]any `json:"criteria"` // nil values marshal as null: an undescribed label
}

type Score struct {
    Instructions any   `json:"instructions,omitempty"`
    Criteria     []any `json:"criteria"` // ordered, non-empty; index == score
}

// RawQuestion passes an arbitrary object through untouched — the escape hatch for question
// kinds or fields the API gains before this SDK models them.
type RawQuestion map[string]any

// ---------- answers ----------

// Answer is a sealed union: NoulAnswer, ChoiceAnswer, ScoreAnswer.
type Answer interface{ answer() }

type NoulAnswer struct {
    Noul float64 `json:"noul"` // P(yes), 0..1
}

type ChoiceAnswer struct {
    Choice        string             `json:"choice"`
    Confidence    float64            `json:"confidence"`
    Probabilities map[string]float64 `json:"probabilities"`
}

type ScoreAnswer struct {
    Score         float64         `json:"score"`
    Confidence    float64         `json:"confidence"`
    Legend        map[int]any     `json:"legend"`        // integer keys; encoding/json handles both directions
    Probabilities map[int]float64 `json:"probabilities"`
}

type Usage struct {
    InputTokens  *int `json:"input_tokens"`  // nil when the API did not report it
    OutputTokens *int `json:"output_tokens"`
}

// ---------- responses ----------

type ResponseMeta struct {
    RequestID  string      // x-typesafe-request-id
    StatusCode int
    Header     http.Header
    Raw        []byte      // the untouched response body
}

type SystemOneResponse struct {
    ResponseMeta

    Model   string
    Usage   Usage
    Answers map[string]Answer // every answer, keyed by question name

    // Pre-partitioned views. Populated eagerly at decode time — cheap, and it removes the
    // need for Python's cached_property machinery.
    Nouls   map[string]NoulAnswer
    Choices map[string]ChoiceAnswer
    Scores  map[string]ScoreAnswer
}

type ModelMetadata struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    ReleaseDate string `json:"release_date"` // YYYY-MM-DD
}

type ListModelsResponse struct {
    ResponseMeta
    Models []ModelMetadata `json:"models"`
}

// ---------- errors ----------

var (
    ErrNoAPIKey        = errors.New("jev: no API key provided")
    ErrInvalidAPIKey   = errors.New("jev: invalid API key")
    ErrInvalidTimeout  = errors.New("jev: invalid timeout")
    ErrInvalidRetry    = errors.New("jev: invalid retry policy")
    ErrNoQuestions     = errors.New("jev: at least one question is required")
    ErrInvalidQuestion = errors.New("jev: invalid question")

    // Status categories, matched through (*APIError).Is.
    ErrBadRequest    = errors.New("jev: bad request")           // 400
    ErrUnauthorized  = errors.New("jev: authentication failed") // 401
    ErrForbidden     = errors.New("jev: permission denied")     // 403
    ErrNotFound      = errors.New("jev: not found")             // 404
    ErrUnprocessable = errors.New("jev: unprocessable entity")  // 422
    ErrRateLimited   = errors.New("jev: rate limited")          // 429
    ErrServer        = errors.New("jev: server error")          // 5xx
)

// APIError is an unsuccessful HTTP response.
type APIError struct {
    StatusCode int
    Message    string        // extracted from the body, see §5.4
    Body       []byte
    Header     http.Header
    RequestID  string
    Endpoint   string        // "POST https://api.typesafe.ai/v1/systemone" — no credentials, query or fragment
    RetryAfter time.Duration // parsed from retry-after-ms / retry-after; 0 when absent
}

func (e *APIError) Error() string
func (e *APIError) Is(target error) bool // maps StatusCode onto the sentinels above

// TransportError is a request that never produced an HTTP response.
type TransportError struct {
    Endpoint string
    Attempts int
    Err      error
}

func (e *TransportError) Error() string
func (e *TransportError) Unwrap() error
func (e *TransportError) Timeout() bool // true for deadline/timeout causes

// ResponseError is a successful HTTP response whose body was missing or structurally invalid.
type ResponseError struct {
    Field      string // dotted path: "answers.tone.confidence", "models[1].name"
    StatusCode int
    Body       []byte
    Header     http.Header
    RequestID  string
    Endpoint   string
    Err        error
}

func (e *ResponseError) Error() string
func (e *ResponseError) Unwrap() error

// ---------- retry ----------

type RetryPolicy struct {
    MaxRetries        int           // retries after the first attempt; 0 disables
    BackoffInitial    time.Duration
    BackoffMax        time.Duration
    BackoffJitter     float64       // 0..1, fraction subtracted from each delay
    RetryStatuses     []int         // status codes that are retried
    RespectRetryAfter bool
    RetryTransport    bool          // retry connection/timeout failures
    Budget            time.Duration // total wall clock per SDK call incl. delays; 0 disables
    Retry             func(apiErr *APIError, err error) bool // extra opt-in predicate
}

func DefaultRetryPolicy() RetryPolicy
```

### Usage

```go
c, err := jev.New() // reads TYPESAFE_API_KEY
if err != nil { return err }

resp, err := c.SystemOne(ctx, jev.SystemOneRequest{
    State: "I was charged twice. Please fix this ASAP.",
    Questions: map[string]jev.Question{
        "billing": jev.Noul{Instructions: "Is this about billing?"},
        "tone":    jev.Choice{Criteria: map[string]any{"calm": nil, "angry": nil}},
        "urgency": jev.Score{Criteria: []any{"can wait", "this week", "today"}},
    },
})

var apiErr *jev.APIError
switch {
case errors.Is(err, jev.ErrRateLimited):
    errors.As(err, &apiErr)
    time.Sleep(apiErr.RetryAfter)
case err != nil:
    return err
}

resp.Nouls["billing"].Noul          // float64
resp.Choices["tone"].Choice         // string
resp.Scores["urgency"].Legend[0]    // any
```

---

## 4. Deliberate divergences from the Python SDK

Each one is a considered choice, not an omission.

| # | Python | Go | Why |
|---|---|---|---|
| 1 | `TypeSafeClient` + `AsyncTypeSafeClient`, ~500 duplicated lines | one `*Client`, `ctx` on every call | Go has no coloured functions. Callers bring their own `errgroup`. |
| 2 | 8 exception subclasses per status | one `*APIError` + sentinel `errors.Is` targets | Go has no exception hierarchies; `errors.Is`/`As` is the idiom. `RetryAfter` lives on `APIError` unconditionally rather than only on a rate-limit subtype. |
| 3 | `client.models.list()` resource namespace | `c.ListModels(ctx)` | Two endpoints do not justify a sub-object. Revisit if the API grows resources. |
| 4 | `close()` / `aclose()`, context managers | no `Close`; `CloseIdleConnections()` | `*http.Client` needs no teardown. Nothing leaks if the caller drops the client. |
| 5 | `httpx.Timeout` with connect/read/write/pool granularity | `WithTimeout` (per-attempt deadline) + `WithHTTPClient` | Granular timeouts are an `http.Transport` concern in Go. Callers who need them configure the transport directly. |
| 6 | `TYPESAFE_LOG_LEVEL` mutates the logger at import | `WithLogger(*slog.Logger)`, default discard | Library code must not configure global logging or read env at init. |
| 7 | `response_model` lifts `answers.<name>` to top-level fields | `SystemOneAs[T]` unmarshals the body **as-is** | Lifting requires reflecting over `T`'s json tags to rewrite the payload — surprising action at a distance. Callers nest a struct with `json:"answers"` instead. Documented with an example. |
| 8 | `raw_http_response` exposes a live `httpx.Response` | `ResponseMeta{RequestID, StatusCode, Header, Raw}` | Handing back an `*http.Response` whose body is already drained and closed is a footgun. |
| 9 | `retry.exceptions: set[type]` + `retry.predicate` | one `Retry func(*APIError, error) bool` | Two hooks that OR together collapse into one. |
| 10 | `NoulCriteria{"true": None}` serializes as `{"true":null}` | `NoulCriteria{True: nil}` omits the key | Per the API docs, absent and null both mean "undescribed", so the distinction carries no meaning. `Choice.Criteria` map values still emit `null`, because there the *key* is the payload. |
| 11 | `cached_property` for `nouls`/`choices`/`scores` | plain fields populated at decode | Partitioning a handful of answers is cheaper than the laziness machinery. |
| 12 | `__version__` from package metadata | `runtime/debug.ReadBuildInfo()`, falling back to `"dev"` | No generated version file to keep in sync. |

**Not a divergence:** the wire format, endpoint paths, header names, config precedence, retry
timing, `Retry-After` parsing, error-message extraction and response validation all match Python
exactly. §5 is the contract.

---

## 5. Behavioural contract (ported verbatim)

### 5.1 Configuration

Precedence: **explicit option > environment > default**. Environment values are trimmed;
empty or whitespace-only values are ignored (they do not shadow the default).

| Setting | Env | Default |
|---|---|---|
| API key | `TYPESAFE_API_KEY` | — (required) |
| Base URL | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| Model | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |
| Timeout | — | `10s` |

- API key: trimmed, then rejected unless non-empty, printable ASCII, no spaces (`ErrInvalidAPIKey`).
  An invalid **explicit** key must not silently fall back to the environment.
- The key must never appear in an error string, `Error()` output, or a log record.
- Base URL: all trailing `/` stripped.
- Timeout: must be positive and finite.

### 5.2 Request construction

- `POST /v1/systemone`, body `{"state":…, "model":…, "questions":…}`, then `Extra` shallow-merged
  over it (last write wins; a colliding key replaces, it does not deep-merge).
- `GET /v1/models`, no body.
- Headers set **last**, so caller headers can never override them:
  `Authorization: Bearer <key>`, `Accept: application/json`, `Content-Type: application/json`
  (bodied requests only), `User-Agent: jev-sdk-go/<version>`, `X-TypeSafe-SDK: jev-sdk-go/<version>`,
  `X-TypeSafe-Runtime: go/<version> (<GOOS>; <GOARCH>)`.
- Any caller-supplied `X-TypeSafe-Retry-Count` is stripped. The SDK sets it to the retry ordinal
  (`1`, `2`, …) on retries only; it is absent on the first attempt.
- Call headers override client headers, which override `http.Client` headers.
- The supplied `*http.Client`'s own `Headers`/`BaseURL`/auth are never mutated.

### 5.3 Client-side validation (before any network call)

- Empty `Questions` → `ErrNoQuestions`.
- `Score` with empty `Criteria` → `ErrInvalidQuestion`, message naming the question.
- `RawQuestion` must carry a non-empty string `"type"`; `"choice"`/`"score"` must carry `"criteria"`;
  `"score"` criteria must be a non-empty array. Anything else about a `RawQuestion` is the API's
  problem, by design.
- A body that cannot be marshalled → error before dispatch.

### 5.4 Error message extraction

First match wins, against the decoded body:

1. `error` (string)
2. `error.message` (string)
3. `message` (string)
4. `detail` (string)
5. `detail.message` (string)
6. `detail` (array): join each `"<loc joined by '.', minus a leading \"body\"> : <msg>"` with `"; "`
7. fallback: the raw body, truncated to 200 bytes with `…` appended
8. empty body: `"status code (no body)"`

`Error()` renders `"<endpoint>: <status> <message> (request_id=<id>)"`, omitting the parts that are absent.

### 5.5 `Retry-After` parsing

Try `retry-after-ms` (milliseconds) first, then `retry-after` (seconds, or an HTTP-date).

- Non-numeric `retry-after` → parse as HTTP-date; result is `max(0, date - now)`.
- Negative `retry-after-ms` → fall through to `retry-after`.
- Negative `retry-after` → no server delay (fall back to backoff).
- Non-finite value, or a product that overflows → fall through / no delay.
- Empty `retry-after` → `0`.
- A server-requested delay is honoured **however long it is** — the budget check (§5.6) is what
  stops an unreasonable wait.

### 5.6 Retry semantics

Defaults (`DefaultRetryPolicy()`): `MaxRetries: 2`, `BackoffInitial: 500ms`, `BackoffMax: 5s`,
`BackoffJitter: 0.25`, `RetryStatuses: {408, 429, 500…599}`, `RespectRetryAfter: true`,
`RetryTransport: true`, `Budget: 30s`.

- Delay for attempt *n* (1-based): `d = min(BackoffInitial << (n-1), BackoffMax)`, then
  `d -= rand.Float64() * BackoffJitter * d`. Overflow on the shift clamps to `BackoffMax`.
  `BackoffInitial == 0 || BackoffMax == 0` → no delay.
- `RespectRetryAfter` and a parseable header → that delay replaces the backoff.
- **Budget:** each SDK call gets a fresh budget. Stop *before* a retry whose delay would make
  elapsed time reach or exceed `Budget`, and return the last error. `Budget: 0` disables the limit.
- A per-call `WithCallRetry` policy fully replaces the client policy for that call; it does not merge.
- The request body is buffered once and replayed per attempt (`bytes.Reader` + `GetBody`).
- Non-retried response bodies are drained and closed so the connection is reusable.
- `ctx` cancellation aborts immediately, including during a backoff sleep, and is returned
  unwrapped enough that `errors.Is(err, context.Canceled)` holds.
- Concurrent calls on one client keep independent attempt state.

> **Zero-value note:** `RetryPolicy{}` performs no retries. Start from `DefaultRetryPolicy()` and
> adjust; the SDK does not silently substitute defaults for individual zero fields, because
> `BackoffInitial: 0` is a meaningful "no backoff" setting.

### 5.7 Response decoding

Non-2xx → `*APIError`. Otherwise:

- Body not a JSON object → `ResponseError{Field: ""}`.
- **Required fields are checked explicitly.** `encoding/json` zero-fills missing fields, which
  would silently turn an omitted `confidence` into `0.0`. Each answer is decoded through an
  internal struct whose required scalars are pointers, then validated; the first missing or
  ill-typed field produces `ResponseError` with its dotted path (`answers.tone.confidence`,
  `models[1].name`).
- Each answer must be an object with a string `type`, else `ResponseError{Field: "answers.<n>.type"}`.
- An answer whose `type` is unrecognised is **dropped with a `slog` warning**, not an error —
  forward compatibility. It remains visible in `resp.Raw`.
- Unknown extra fields anywhere are ignored.
- `Legend` / `Probabilities` string keys are coerced to `int`; a non-integer key is a validation error.
- `Usage.InputTokens` / `OutputTokens` stay nil when absent.
- `ResponseMeta` is populated on every successful decode.

### 5.8 Logging

`log/slog`, default `slog.New(slog.DiscardHandler)`.

- `Info`: one line per request (`method`, `url`, `status`, `duration`, `request_id`), one per retry.
- `Debug`: request and response headers and bodies.
- **Redaction is a single chokepoint in `internal/transport/redact.go`.** Header values are
  redacted to `***` when the name is in `{authorization, proxy-authorization, x-api-key, api-key,
  cookie, set-cookie}` or contains `token` / `secret` (case-insensitive). URL userinfo is stripped
  before any URL is logged or embedded in an error.
- Bodies are *not* redacted — same caveat as Python, documented on `WithLogger`.

---

## 6. Implementation phases

Each phase is test-first: write the table-driven test, watch it fail, implement, watch it pass.
Each phase ends green under `go test ./... -race` and is one commit.

### Phase 0 — Scaffolding
`go.mod` (`go 1.27`), `LICENSE` (MIT, matching Python), `.gitignore`, `Makefile`
(`test`, `lint`, `vet`, `cover`, `vulncheck`), `README.md` stub, `doc.go`, CI workflow (§8).

### Phase 1 — Question types and encoding
`question.go`. The sealed `Question` interface; `Noul`/`Choice`/`Score` with `MarshalJSON`
injecting the `type` discriminator via the shadow-type trick; `RawQuestion` verbatim passthrough;
`validateQuestions`.

*Tests:* golden JSON for every question shape, including the boundary cases that motivated the
`encoding/json` v1 choice — `Noul{}` → `{"type":"noul"}`, `Noul{Instructions: ""}` →
`{"type":"noul","instructions":""}`, `Noul{Instructions: []any{}}` → `…"instructions":[]`,
`Choice{Criteria: {"a": nil}}` → `…"criteria":{"a":null}`. Validation rejection table.
`RawQuestion` is not mutated by encoding.

### Phase 2 — Answers, responses, decoding
`answer.go`, `response.go`. Sealed `Answer`; discriminated decode via
`map[string]json.RawMessage`; presence validation with dotted field paths; unknown-type drop;
eager `Nouls`/`Choices`/`Scores` partitioning; integer-key coercion.

*Tests:* the full Python `test_responses.py` matrix of `(body, expected field path)`; unknown
answer type is dropped and still present in `Raw`; unknown extra fields tolerated; `Usage` nil
handling; integer keys round-trip.

### Phase 3 — Errors
`errors.go`. Sentinels, `APIError` with `Is`, `TransportError` with `Unwrap`/`Timeout`,
`ResponseError`, message extraction, endpoint formatting.

*Tests:* the `test_errors.py` / `test_clients.py::test_error_messages` extraction table;
status → sentinel mapping; `errors.Is`/`errors.As` behaviour; no credential ever reaches
`Error()`; `Endpoint` carries no userinfo, query or fragment.

### Phase 4 — Retry policy
`retry.go`. `DefaultRetryPolicy`, validation, backoff with jitter and overflow clamp,
`Retry-After` parsing, budget arithmetic.

*Tests:* the `test_retry.py::test_parse_retry_after` table verbatim; backoff sequence with
`rand` stubbed to 0 and 1 (`0.5s, 1s, 2s, 4s, 5s, 5s…`, and `0.375s` at full jitter); extreme
values; budget stop-before-delay boundary (`elapsed + delay >= Budget`); policy validation errors.

### Phase 5 — Transport
`internal/transport`. Request value, protected-header merge, attempt loop, body replay, response
draining, redaction, slog call sites. A `now func() time.Time` and `sleep func(context.Context, time.Duration) error`
are injectable so the budget and backoff tests use a fake clock instead of real sleeps.

*Tests:* `httptest.Server`-driven; header precedence and protection; `X-TypeSafe-Retry-Count`
sequence `[absent, "1", "2"]`; body identical across retries; ctx cancellation mid-backoff;
`-race` with concurrent calls sharing a client.

### Phase 6 — Client and endpoints
`client.go`, `options.go`, `systemone.go`, `models.go`. Config resolution, option application,
the three call methods, per-call overrides.

*Tests:* config precedence matrix (default / env / option, including whitespace-only env);
model override; `Extra` shallow-merge incl. overriding `state`/`model`/`questions` and a `null`
value; per-call retry/timeout/header overrides don't leak into the next call; `SystemOneAs[T]`
against a user struct; `ResponseMeta` population when `T` embeds it; supplied `*http.Client`
left unmutated; `var _ SystemOner = (*Client)(nil)` compile-time assertion that plain
`SystemOne` stays interface-satisfiable.

### Phase 7 — Docs and examples
`doc.go` package overview, doc comments on every exported identifier, `example_test.go` with
runnable `Example` functions (`go test` compiles and runs them — the replacement for sybil),
`examples/basic` and `examples/concurrent`, `README.md`, `CHANGELOG.md`.

### Phase 8 — Integration and release
`//go:build integration` tests mirroring `test_integration.py`, skipped without
`TYPESAFE_API_KEY`. `govulncheck`, `go vet`, lint clean. Tag `v0.1.0`.

---

## 7. Testing strategy

- **`httptest.Server`** replaces `httpx.MockTransport`. Handlers assert on the inbound request
  and script the response sequence.
- **Table-driven everywhere**, with `t.Parallel()` where the case is independent.
- **Fake clock** injected into `internal/transport` for retry timing — the suite must not sleep.
- **`-race` on every run**; a dedicated concurrency test fans out N calls on one client.
- **Fuzz** `parseRetryAfter` and the error-message extractor; both parse untrusted server input.
- **Golden JSON** for request encoding (`testdata/*.json`), so a wire-format regression is a diff.
- **Examples are tests.** `Example` functions with `// Output:` are compiled and executed.
- **Coverage target 90%+** on the root package; transport-error paths make 100% impractical.
- No mocking framework, no assertion library — `net/http/httptest` and `t.Errorf`.

---

## 8. CI and release

`.github/workflows/ci.yml`, on push and PR:

1. `go build ./...`
2. `go vet ./...`
3. `golangci-lint run` (errcheck, govet, staticcheck, revive, gosec, misspell)
4. `go test ./... -race -covermode=atomic -coverprofile=coverage.out`
5. `govulncheck ./...`
6. `gofmt -l .` must be empty

Matrix: `ubuntu-latest`, `macos-latest`, `windows-latest` on Go 1.27.

**Release:** no publish step — Go modules are served from the proxy on tag. Tag `vX.Y.Z`, keep
`CHANGELOG.md` in the Keep-a-Changelog shape the Python `docs/changelog.md` already uses. Stay on
`v0.x` until the API settles; document that `internal/` carries no compatibility promise.

---

## 9. Non-goals (v0.1.0)

Streaming (the API has no streaming endpoint), a CLI, OpenAPI codegen (two endpoints and seven
schemas do not justify a generator), middleware/interceptor hooks (`WithHTTPClient` covers it),
automatic pagination (`/v1/models` is unpaginated), OpenTelemetry (add once the shape is stable;
`WithHTTPClient` lets callers wrap the transport today).

---

## 10. Risks and open questions

1. **Go 1.27 minimum.** `SystemOneAs[T]` as a *method* forces it. A brand-new toolchain floor is
   aggressive for a public SDK. *Escape hatch:* move it to a package-level
   `func SystemOneAs[T any](ctx, *Client, SystemOneRequest, ...CallOption) (*T, error)` and the
   floor drops to 1.21. The API shape is otherwise identical. Decide before tagging v0.1.0.
2. **`State any` / `Instructions any`.** Correct — this is a JSON passthrough boundary and the
   API accepts string, object or array — but it gives up compile-time checking at exactly the
   place callers are most likely to make a mistake. Mitigated by doc comments and examples.
   A sealed `Content` union was considered and rejected as ceremony without payoff.
3. **Required-field validation is hand-written.** Neither `encoding/json` nor `json/v2` has a
   `required` tag, so §5.7's presence checks are manual and must be extended whenever an answer
   type gains a field. Guarded by the field-path test table.
4. **`RetryPolicy` zero value disables retries.** A caller writing
   `WithRetry(RetryPolicy{MaxRetries: 5})` gets no backoff. Documented on the type and in
   `DefaultRetryPolicy`'s comment; a merge-with-defaults alternative was rejected because it
   would make `BackoffInitial: 0` unexpressible.
5. **No `answers` lifting in `SystemOneAs[T]`** (divergence 7). If it turns out to be the dominant
   use, revisit with an explicit opt-in rather than reflection-by-default.
6. **Env var namespace.** `TYPESAFE_*` is kept for interop with the Python SDK. If the service
   rebrands around `jev`, this is a breaking change — cheap to make while on `v0.x`.
