# Examples

Runnable programs, one per use case, each showing a different part of the SDK.
Run one with an API key in the environment:

```sh
TYPESAFE_API_KEY=... go run ./examples/triage
```

`openrouter` reads `OPENROUTER_API_KEY` instead, and `gateway` also reads
`GATEWAY_URL` and `GATEWAY_KEY`.

| Example | Use case | Shows |
|---|---|---|
| [`basic`](basic) | Quickstart | `ListModels`, the three question kinds, reading the answer maps, `Ranked`, `Description` |
| [`triage`](triage) | Routing support tickets | `Ask` and typed keys, `ChoiceOf`/`ScoreOf` with your own enums, confidence gating, the runner-up for human review |
| [`moderation`](moderation) | Enforcing content policy | A struct as the state, `NoulCriteria` with structured descriptions, thresholds kept in code as policy |
| [`rerank`](rerank) | Search relevance | Many questions over one state in a single call, structured instructions, ranking by `Score` and bucketing by `Level` |
| [`batch`](batch) | Classifying at volume | One client shared across goroutines, bounded concurrency, results in input order, failures per item |
| [`config`](config) | Questions defined in data | `RawQuestion` from JSON, one type switch over any answer kind, `UnknownAnswer`, `Raw` |
| [`decode`](decode) | Straight into your own struct | `SystemOneAs`, generic answer types as struct fields, an embedded `ResponseMeta` |
| [`resilience`](resilience) | Retries and failure handling | A `RetryPolicy` built from the default, per-attempt and per-call timeouts, the `errors.Is`/`errors.As` taxonomy |
| [`observability`](observability) | Logs and metrics | `WithLogger`, a metering `RoundTripper`, the retry-count header, request IDs, where `otelhttp` plugs in |
| [`openrouter`](openrouter) | OpenRouter as the provider | Base URL and key, OpenRouter's extra response fields, why `ListModels` is unavailable there |
| [`gateway`](gateway) | Behind your own gateway | A path-prefixed base URL, gateway and per-call headers, an HTTP client tuned for one busy host |

## Tested without a key

Each example has a test that runs it against `internal/fakeapi`, an
in-process stand-in for the API that answers any question in the documented
wire format. It can also answer the way OpenRouter does, and inject failures
to exercise retries. So

```sh
go test ./examples/...
```

checks every example end to end, with no network and no key. The fake's
answers are deterministic but not meaningful: it exercises the programs' logic,
not the model's judgement.
