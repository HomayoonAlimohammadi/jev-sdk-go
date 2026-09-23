---
name: jev-sdk-go
description: Use when writing, reviewing or debugging Go code that calls TypeSafe AI's Jev / System One API through github.com/HomayoonAlimohammadi/jev-sdk-go, directly, through OpenRouter or through a gateway.
---

# Using jev-sdk-go

System One answers typed questions about a piece of content, the *state*, with calibrated probabilities. Use it for decisions: routing, classification into a fixed set, triage, moderation, reranking. It does not generate text. For designing the questions themselves, TypeSafe's official skill is the authority (`npx skills add typesafe-ai/skills --skill typesafe-ai`); this skill covers the Go SDK.

## Setup

```sh
go get github.com/HomayoonAlimohammadi/jev-sdk-go   # Go 1.24+, no dependencies
```

Create **one** client and share it; it is safe for concurrent use and pools its connections. `jev.New()` reads `TYPESAFE_API_KEY`. `jev.WithAPIKey(key)` is validated the same way, so either works. Keep the key in server-side configuration, never in source, URLs or logs.

## Asking

Put every independent question about the same state in **one** call, and write each to stand alone. Make a second call only when an earlier answer decides what to ask next.

| Question | Asks | Checked before sending |
|---|---|---|
| `jev.Noul` | yes/no | non-empty `Instructions` or a non-null criterion |
| `jev.ChoiceOf[T]` (`jev.Choice` for strings) | one of these labels | 1 to 255 labels |
| `jev.ScoreOf[L]` (`jev.Score` for ints) | where on this ordered rubric | 1 to 10 levels, none null |
| `jev.RawQuestion` | anything the API accepts | a non-empty `"type"` |

Prefer the typed form with `jev.Ask`: answers come back in your own types, checked against what the question offered.

```go
type Team string

const (
	Billing   Team = "billing"
	Technical Team = "technical"
	Other     Team = "other"
)

// Severity's iota numbering matches the rubric: a level's position is its value.
type Severity int

const (
	Low Severity = iota
	Medium
	High
)

req := jev.SystemOneRequest{State: ticket}
team := jev.Ask(&req, "team", jev.ChoiceOf[Team]{
	Instructions: "Which team should handle this ticket?",
	Criteria: map[Team]any{
		Billing:   "Payments, invoices, refunds",
		Technical: "Errors, outages, integrations",
		Other:     "Anything else",
	},
})
severity := jev.Ask(&req, "severity", jev.ScoreOf[Severity]{
	Instructions: "How badly is the customer affected?",
	Criteria:     jev.Levels("low: nothing blocked", "medium: a workaround exists", "high: cannot work"),
})

resp, err := client.SystemOne(ctx, req)
// ...
t, err := team.Answer(resp)     // jev.ChoiceAnswerOf[Team]
s, err := severity.Answer(resp) // jev.ScoreAnswerOf[Severity]
```

- Describe labels that could overlap, and include an `other` label when the set is not closed.
- Write a rubric lowest to highest; level *i* is the *i*-th description.
- `State` is a string, a JSON object or array, or a struct. Not a bare number, boolean or nil.
- Question names are never shown to the model; they only key the answers.

## Reading answers

- `NoulAnswer.Noul` is P(yes). The threshold is policy and belongs in your code; there is deliberately no `Yes()` helper.
- `Confidence` is the API's confidence in a choice or score, **not** the chosen label's probability (`Probabilities[Choice]`). Route low-confidence answers to a fallback.
- `ChoiceAnswerOf.Ranked()` lists labels by probability. Give a human reviewer `Ranked()[1]`, the runner-up.
- `ScoreAnswerOf.Score` is an expected value between levels. Use `Level()` for the level it rounds to, and `Description()` for its text.
- `SystemOne` fails if any question comes back unanswered or answered in the wrong kind, so a successful response holds every answer asked for.

## Errors

Branch with `errors.Is` / `errors.As`, never on messages:

- `ErrRateLimited`: `*APIError` carries `RetryAfter`.
- `ErrUnauthorized` / `ErrForbidden`: configuration; do not retry.
- `*APIError`: `StatusCode`, `Message`, `RequestID`, `Attempts`.
- `*TransportError`: never reached the API; `Timeout()` tells a timeout apart.
- `*ResponseError`: unusable response; `Field` names the problem.
- `ErrInvalidQuestion`, `ErrInvalidState`, `ErrNoQuestions`: a bug in the request built. Fix the code.

Quote `RequestID` in support tickets.

## Retries and time

The default already retries 408, 429, 5xx and connection failures twice with backoff, honouring `Retry-After` up to a minute, within a 30-second budget. **Do not add a retry loop of your own**; it multiplies attempts. To change it, start from `jev.DefaultRetryPolicy()`; the zero `RetryPolicy{}` disables retries. `WithTimeout` bounds each attempt; bound the whole call with the context.

## Providers

- **OpenRouter**: `jev.WithBaseURL("https://openrouter.ai/api")` and an OpenRouter key. The default model works as-is. `ListModels` does not work there; its extra `id`, `provider` and `usage.cost` are only in `resp.Raw`.
- **A gateway of your own**: point `WithBaseURL` at it (a path prefix is fine) and add its headers with `WithHeader`. A gateway that rewrites the request into another format will not work.

## Security

- Keep body logging off (the default). Bodies are your users' content, unredacted.
- A client of your own passed with `WithHTTPClient` must set `CheckRedirect` to return `http.ErrUseLastResponse`, and raise `MaxIdleConnsPerHost` for concurrent use.
- Use `https` base URLs.

## Testing your code

Depend on a narrow interface of your own and fake it:

```go
type evaluator interface {
	SystemOne(context.Context, jev.SystemOneRequest, ...jev.CallOption) (*jev.SystemOneResponse, error)
}
```

`jev.SystemOneAs[T]` is a generic function taking the client, so it cannot sit behind that interface. To exercise the SDK itself, point `jev.WithBaseURL` at an `httptest.Server`.

## Examples

Tested programs for common use cases, OpenRouter and a gateway included: https://github.com/HomayoonAlimohammadi/jev-sdk-go/tree/main/examples
