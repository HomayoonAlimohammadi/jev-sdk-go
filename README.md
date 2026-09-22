# jev-sdk-go

A Go client for the [TypeSafe AI](https://typesafe.ai) API.

TypeSafe answers structured questions about a piece of content. You name each
question; the answers come back under those names, with the probabilities
behind them.

Zero dependencies — standard library only.

## Install

```
go get github.com/HomayoonAlimohammadi/jev-sdk-go
```

Requires Go 1.27.

## Quickstart

Set `TYPESAFE_API_KEY` in your environment.

```go
package main

import (
	"context"
	"fmt"
	"log"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

func main() {
	client, err := jev.New()
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.SystemOne(context.Background(), jev.SystemOneRequest{
		State: "I was charged twice. Please fix this ASAP.",
		Questions: map[string]jev.Question{
			"category": jev.Choice{
				Instructions: "What is this ticket about?",
				Criteria:     jev.Labels("billing", "technical", "other"),
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Choices["category"].Choice)
}
```

## Questions

| Kind | Asks | Answers with |
|---|---|---|
| `jev.Noul` | a yes/no question | a probability in `NoulAnswer.Noul` |
| `jev.Choice` | which of these labels | the label plus every label's probability |
| `jev.Score` | rate this against a rubric | an expected score, the rubric, and each level's probability |
| `jev.RawQuestion` | anything the API accepts | whatever comes back, via `resp.Raw` |

A `Choice`'s criteria maps each label to a description of when it applies.
For labels that speak for themselves, `jev.Labels` saves writing the nils:

```go
jev.Choice{
	Instructions: "What is this ticket about?",
	Criteria:     jev.Labels("billing", "technical", "other"),
}
```

Write the map out to describe a label. A description sharpens the boundary
between labels that overlap, and it need not be a string — instructions and
criteria take any JSON-marshalable value:

```go
jev.Choice{
	Instructions: "What is this ticket about?",
	Criteria: map[string]any{
		"billing":   "Payments, invoices and refunds, but not delivery complaints",
		"technical": map[string]any{"meaning": "The product misbehaved", "examples": []string{"500 error"}},
		"other":     nil,
	},
}

jev.Noul{
	Instructions: "Is this a duplicate charge?",
	Criteria: &jev.NoulCriteria{
		True:  map[string]any{"meaning": "Billed more than once", "examples": []string{"charged twice"}},
		False: "A single, expected charge",
	},
}
```

A `Score`'s criteria is an ordered rubric: a description's position is its
score, counting from zero. `jev.Levels` builds one from plain phrases.

```go
jev.Score{
	Instructions: "How urgent is this ticket?",
	Criteria:     jev.Levels("can wait", "this week", "today"),
}
```

## Answers

`SystemOne` returns every answer in `resp.Answers`, plus per-kind maps so you
reach an answer you asked for without a type assertion:

```go
resp.Nouls["billing"].Noul          // float64
resp.Choices["tone"].Choice         // string
resp.Choices["tone"].Probabilities  // map[string]float64
resp.Scores["urgency"].Score        // float64
resp.Scores["urgency"].Legend       // map[int]any
```

Every question you ask must come back answered, and answered in kind, or the
call fails with a `*jev.ResponseError` naming it. So reaching straight into
these maps is safe — a name you asked about is there. Without that check a
missing answer would surface as `NoulAnswer{}`, and a `Noul` of `0.0` reads as
a confident "no".

Answers carry the readings you would otherwise compute:

```go
tone.Ranked()         // labels from most to least probable; [1] is the runner-up
urgency.Level()       // the rubric level the score rounds to, clamped to the rubric
urgency.Description() // what the rubric says about that level
```

A `Noul` has no `Yes(threshold)` on purpose: the threshold depends on what a
wrong yes and a wrong no cost you, which is the one part of the decision the
SDK cannot know.

An answer of a kind this SDK version does not model arrives as a
`jev.UnknownAnswer` carrying its `Type` and `Raw` payload, so a `RawQuestion`
works end to end and one unfamiliar answer never costs you the others.

### Typed answers in your own types

`ChoiceOf[T]` and `ScoreOf[L]` take labels and levels of your own types.
Register each question with `jev.Ask`, which returns a typed handle; the answer
comes back as your type, so the compiler checks the `switch` over it:

```go
type Team string

const (
	Billing   Team = "billing"
	Technical Team = "technical"
)

type Severity int

const (
	Cosmetic Severity = iota // a description's position is its level,
	Degraded                 // so iota lines up with the rubric
	Blocking
)

req := jev.SystemOneRequest{State: ticket}
team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{Criteria: jev.LabelsOf(Billing, Technical)})
severity := jev.Ask(&req, "severity", jev.ScoreOf[Severity]{Criteria: jev.Levels("cosmetic", "degraded", "blocking")})

resp, err := client.SystemOne(ctx, req)
// ...
t, err := team.Answer(resp)     // jev.ChoiceAnswerOf[Team]: t.Choice is a Team
s, err := severity.Answer(resp) // jev.ScoreAnswerOf[Severity]: s.Level() is a Severity
```

`Key.Answer` also checks the answer against the question: a choice the
question never offered, or a level outside its rubric, fails with a
`*jev.ResponseError` rather than yielding a value that is none of your
constants. `Choice` and `Score` are simply `ChoiceOf[string]` and
`ScoreOf[int]`, so they work with `Ask` too, and everything else is unchanged.

To decode into a type of your own, use `SystemOneAs`. The body is decoded as
the API sends it, so the struct mirrors the wire shape:

```go
type ticket struct {
	jev.ResponseMeta // optional: filled in with the request ID and headers

	Answers struct {
		Billing jev.NoulAnswer   `json:"billing"`
		Tone    jev.ChoiceAnswer `json:"tone"`
	} `json:"answers"`
}

answers, err := client.SystemOneAs[ticket](ctx, req)
```

The generic answer types carry JSON tags, so `ChoiceAnswerOf[Team]` and
`ScoreAnswerOf[Severity]` work as fields here too.

`SystemOneAs` skips the answered-in-kind check, decoding whatever arrived, so
it is also the way to accept a deliberately partial response.

The untouched body is always on `resp.Raw`.

## Errors

```go
var apiErr *jev.APIError

switch {
case errors.Is(err, jev.ErrRateLimited):
	errors.As(err, &apiErr)
	time.Sleep(apiErr.RetryAfter)
case errors.Is(err, jev.ErrUnauthorized):
	// check TYPESAFE_API_KEY
}
```

Three error types, each recoverable with `errors.As`:

- `*jev.APIError` — an unsuccessful HTTP response. Carries the status, body,
  headers, request ID and the server's requested `RetryAfter`. `errors.Is`
  matches it against `ErrBadRequest`, `ErrUnauthorized`, `ErrForbidden`,
  `ErrNotFound`, `ErrUnprocessable`, `ErrRateLimited` and `ErrServer`.
- `*jev.TransportError` — the request never reached the API. `Timeout()`
  reports whether it ran out of time.
- `*jev.ResponseError` — the response body was missing data the SDK needs.
  `Field` names it, such as `answers.tone.confidence`.

## Configuration

Explicit options beat the environment, which beats the default. An empty or
whitespace-only environment value is ignored.

| Option | Environment | Default |
|---|---|---|
| `WithAPIKey` | `TYPESAFE_API_KEY` | — (required) |
| `WithBaseURL` | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `WithModel` | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |
| `WithTimeout` | — | `10s` per attempt |
| `WithRetry` | — | `DefaultRetryPolicy()` |
| `WithHeader` | — | none |
| `WithHTTPClient` | — | `&http.Client{}` |
| `WithMaxResponseBytes` | — | 1 MiB |
| `WithBodyLogging` | — | off |
| `WithLogger` | — | logs nothing |

Requests are checked before they leave, so a request the API would refuse
fails without a round trip:

- `State` must encode to a string, object or array.
- Every question needs a non-empty name.
- A `Choice` needs 1 to 255 labels.
- A `Score` needs 1 to 10 levels, none of them null.
- A `Noul` must ask something: non-empty instructions, or a described outcome.

The caps are documented. The one-level floor and the `Noul` rule follow the
service's reported behavior as of 2026-09-20, which departs from the
documentation's two-level minimum; they are not yet confirmed by this SDK's
own live tests.

Response bodies are read under a 1 MiB cap, so a runaway server cannot exhaust
memory; raise or remove it with `WithMaxResponseBytes`.

## Retries

By default a call is retried twice on 408, 429 and 5xx responses and on
transport failures, with exponential backoff from 500ms to 5s, honoring the
server's `Retry-After`, inside a 30 second budget.

```go
policy := jev.DefaultRetryPolicy()
policy.MaxRetries = 4
policy.Budget = time.Minute

client, err := jev.New(jev.WithRetry(policy))

// Or replace it for one call. RetryPolicy{} disables retries.
resp, err := client.SystemOne(ctx, req, jev.WithCallRetry(jev.RetryPolicy{}))
```

`RetryPolicy`'s zero value performs no retries, so build from
`DefaultRetryPolicy()` rather than a partial literal.

A server that asks for a longer wait than `MaxRetryAfter` (a minute by
default) is taken at its word: the call returns at once with the request in
`APIError.RetryAfter`, for you to schedule, rather than parking a goroutine or
retrying a server that asked you to back off. `APIError.Attempts` says how many
attempts were made.

## Concurrency

A `*jev.Client` is safe for concurrent use and pools connections. Create one,
share it, and choose the parallelism yourself:

```go
var wg sync.WaitGroup
for _, ticket := range tickets {
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := client.SystemOne(ctx, request(ticket))
		// ...
	}()
}
wg.Wait()
```

## Logging

The SDK logs nothing until you give it a `*slog.Logger`. URLs are stripped of
credentials and credential-bearing headers are redacted.

```go
client, err := jev.New(jev.WithLogger(slog.Default()))
```

Bodies are left out by default. The request body is the state you are
evaluating and the response is the model's reading of it, so both carry
whatever your content carries, and nothing in them is redacted. Opt in only
where that is acceptable:

```go
client, err := jev.New(jev.WithLogger(logger), jev.WithBodyLogging(true))
```

The SDK's own HTTP client refuses to follow redirects. If you supply one with
`WithHTTPClient`, set `CheckRedirect` to return `http.ErrUseLastResponse`:
`net/http` keeps an `Authorization` header across an https-to-http redirect to
the same host, and keeps every header it does not recognize across a redirect
to any host.

## Observability

The SDK has no tracing dependency of its own. Wrap the transport of the
`*http.Client` you pass to `WithHTTPClient` instead — with OpenTelemetry, for
example:

```go
httpClient := &http.Client{
	Transport: otelhttp.NewTransport(http.DefaultTransport),
	// Keep the SDK's own policy: never follow a redirect with credentials.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

client, err := jev.New(jev.WithHTTPClient(httpClient))
```

Every response, and every `*APIError` and `*ResponseError`, carries the
`x-typesafe-request-id` as `RequestID`; quote it in a support ticket.

## Performance

The SDK's own cost is about 27µs and 210 allocations per call, measured
against a local server with `go test -bench .`. A real call spends orders of
magnitude longer on the network and in the model, so this matters only for
garbage-collector pressure in very high-throughput services. Validation
allocates nothing, and with logging off the SDK builds no log records at all.

## Development

```
make test        # go test ./... -race
go test -bench . # the SDK's own per-call cost
make cover       # coverage summary
make vet lint    # go vet, golangci-lint
make integration # live API tests, needs TYPESAFE_API_KEY
```

## License

MIT
