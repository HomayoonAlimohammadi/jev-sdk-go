# Integrating jev-sdk-go

A guide for coding agents adding this SDK to a Go application. It covers what
you need to write correct code on the first pass; `README.md` and the package
documentation (`go doc github.com/HomayoonAlimohammadi/jev-sdk-go`) hold the
rest.

## What it is for

TypeSafe's System One API answers typed questions about a piece of content,
the *state*, with calibrated probabilities. Use it for decisions: routing,
classification into a fixed set, triage by severity, moderation, re-ranking.
Do not use it to generate text; it answers questions, it does not write prose.

## Setup

```sh
go get github.com/HomayoonAlimohammadi/jev-sdk-go
```

Requires Go 1.24 or later. No other dependencies. The API key comes from
`TYPESAFE_API_KEY` or `jev.WithAPIKey`. Read it from server-side configuration;
never embed it in source, a URL, a log line, or anything shipped to a browser.

Create **one** client and share it. It is safe for concurrent use and pools
connections; creating one per request wastes them.

```go
client, err := jev.New() // reads TYPESAFE_API_KEY
```

## Asking

Put every independent question about the same state in **one** call; the
documentation recommends asking independent questions together. Write each
question to stand alone, never relying on another's answer. Make a second call
only when an earlier answer decides what to ask next.

| Question | Asks | Limits checked before sending |
|---|---|---|
| `jev.Noul` | yes/no | needs non-empty `Instructions` or a non-null criterion |
| `jev.Choice`, `jev.ChoiceOf[T]` | one of these labels | 1 to 255 labels |
| `jev.Score`, `jev.ScoreOf[L]` | where on this ordered rubric | 1 to 10 levels, none null |
| `jev.RawQuestion` | anything the API accepts | only a non-empty `"type"` |

Prefer the typed form. It gives the caller's own enum back and lets the
compiler check the code that acts on it:

```go
type Team string

const (
	Billing   Team = "billing"
	Technical Team = "technical"
)

type Severity int

const (
	Cosmetic Severity = iota // position in the rubric is the level,
	Degraded                 // so iota numbering lines up
	Blocking
)

req := jev.SystemOneRequest{State: ticketText}
urgent := jev.Ask(&req, "urgent", jev.Noul{Instructions: "Does this convey urgency?"})
team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{
	Instructions: "Which team should handle this?",
	Criteria:     map[Team]any{Billing: "Payments, invoices, refunds", Technical: "Bugs, outages, integrations"},
})
severity := jev.Ask(&req, "severity", jev.ScoreOf[Severity]{
	Instructions: "How severe is the issue?",
	Criteria:     jev.Levels("cosmetic", "degraded, with a workaround", "blocking"),
})

resp, err := client.SystemOne(ctx, req)
if err != nil {
	return err
}

u, err := urgent.Answer(resp)   // jev.NoulAnswer
t, err := team.Answer(resp)     // jev.ChoiceAnswerOf[Team]
s, err := severity.Answer(resp) // jev.ScoreAnswerOf[Severity]
```

Rules that are easy to get wrong:

- **Describe overlapping labels.** A label with a nil description is read by its
  name alone. Where two labels could both fit, a one-line description of each
  is what separates them. Include an `other` label when the set is not closed.
- **Order is the meaning of a score.** Level *i* is the *i*-th description.
  Write the rubric from lowest to highest.
- **`State` is a string, object or array.** A struct works; a bare number,
  boolean or nil does not.
- **Question names are yours.** They are never shown to the model; they are
  only the keys the answers come back under.

## Reading answers

- `NoulAnswer.Noul` is the probability of *yes*. The threshold is a policy
  decision that depends on the cost of each kind of mistake, so it belongs in
  the calling code, not the question. There is deliberately no `Yes()` helper.
- `ChoiceAnswerOf.Confidence` is the API's confidence in the choice. It is
  **not** the probability of the chosen label; that is
  `Probabilities[Choice]`. Gate low-confidence choices to a fallback, and use
  `Ranked()[1]` to see the runner-up.
- `ScoreAnswerOf.Score` is an expected value and may fall between levels. Use
  `Level()` for the level it rounds to and `Description()` for its rubric text.

```go
switch {
case t.Confidence < 0.5:
	return routeToHuman(t.Ranked())
case t.Choice == Technical && s.Level() == Blocking:
	return pageOnCall()
default:
	return enqueue(t.Choice)
}
```

`SystemOne` fails if any question comes back unanswered or answered in the
wrong kind, so a successful response holds every answer you asked for. Without
`Ask`, the same answers are in `resp.Nouls`, `resp.Choices` and `resp.Scores`,
keyed by question name.

## Errors

Branch with `errors.Is` and `errors.As`, never on error strings.

```go
var apiErr *jev.APIError
var transportErr *jev.TransportError
var responseErr *jev.ResponseError

switch {
case errors.Is(err, jev.ErrRateLimited):
	errors.As(err, &apiErr)
	scheduleRetry(apiErr.RetryAfter)
case errors.Is(err, jev.ErrUnauthorized), errors.Is(err, jev.ErrForbidden):
	// Configuration problem: do not retry.
case errors.As(err, &apiErr):
	log(apiErr.StatusCode, apiErr.Message, apiErr.RequestID)
case errors.As(err, &transportErr):
	// Never reached the API. transportErr.Timeout() tells a timeout apart.
case errors.As(err, &responseErr):
	// The API answered with something unusable; responseErr.Field names it.
case errors.Is(err, jev.ErrInvalidQuestion), errors.Is(err, jev.ErrInvalidState):
	// A bug in the request built by the calling code. Fix the code; do not retry.
}
```

Quote `RequestID`, present on responses and on API and response errors, in any
support ticket.

## Retries and time

The default already retries 408, 429, 5xx and connection failures twice, with
backoff and jitter, honoring `Retry-After` up to a minute, within a 30-second
budget per call. **Do not wrap calls in a retry loop of your own**; that
multiplies attempts. To change the behavior, start from
`jev.DefaultRetryPolicy()` and adjust it. The zero `RetryPolicy{}` disables
retries entirely.

`WithTimeout` bounds each attempt. Bound the whole call with the context's
deadline or `RetryPolicy.Budget`. Always pass the request's context so
cancellation propagates.

## Security boundaries

- Keep body logging off (`WithBodyLogging` defaults to off). Request bodies are
  the user's content and response bodies are the model's reading of it; neither
  is redacted.
- The SDK's HTTP client refuses redirects. If you pass your own with
  `WithHTTPClient`, set `CheckRedirect` to return `http.ErrUseLastResponse`,
  or a redirect can replay the API key to another host or over plaintext.
- Use `https` base URLs. The SDK warns, through the configured logger, about a
  plaintext `http` base URL on a non-loopback host.

## Testing code that uses the SDK

Depend on a narrow interface of your own, not on `*jev.Client`, and fake it in
unit tests:

```go
type evaluator interface {
	SystemOne(context.Context, jev.SystemOneRequest, ...jev.CallOption) (*jev.SystemOneResponse, error)
}
```

`jev.SystemOneAs[T]` is a generic function taking the client, so it cannot sit
behind that interface; use `SystemOne` behind the seam. For tests that exercise
the SDK itself, point `jev.WithBaseURL` at an `httptest.Server`.

## Checklist

- [ ] One client, created once, shared.
- [ ] Independent questions batched into one call.
- [ ] Typed `ChoiceOf`/`ScoreOf` with `Ask` where the answer drives code.
- [ ] Thresholds and fallbacks in the calling code, not the prompt.
- [ ] Low-confidence answers routed to a fallback.
- [ ] Errors branched with `errors.Is` / `errors.As`; no extra retry loop.
- [ ] Request context passed through; a deadline set on it.
- [ ] Body logging off; any custom HTTP client refuses redirects.
- [ ] The SDK behind a narrow interface for unit tests.
