# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is below `v1.0.0` the public API may change between minor
releases. Nothing under `internal/` carries a compatibility promise.

## [Unreleased]

### Added

- Initial Go client for the TypeSafe AI API, ported from the Python SDK.
- `Client.SystemOne`, `Client.SystemOneAs[T]` and `Client.ListModels`, all
  context-aware and safe for concurrent use.
- `Noul`, `Choice`, `Score` and `RawQuestion` question types, with client-side
  validation before a request is sent.
- `Labels` and `Levels`, which build `Choice` and `Score` criteria from plain
  strings.
- A 1 MiB cap on response bodies, configurable with `WithMaxResponseBytes`, so
  a runaway server cannot exhaust memory.
- Client-side validation that `State` encodes to a string, object or array, as
  the API requires.
- A check that every question comes back answered, and answered in kind, so a
  missing answer fails loudly instead of reading as a zero-valued one.
- `WithBodyLogging`, off by default, so request and response bodies no longer
  reach debug logs unless asked for.

- Typed questions and answers: `ChoiceOf[T]` and `ScoreOf[L]` take labels and
  levels of your own types, and `Ask` returns a typed `Key` whose `Answer`
  reads the answer back in them, refusing a choice the question never offered
  or a level outside its rubric. `Choice` and `Score` are now
  `ChoiceOf[string]` and `ScoreOf[int]`, so existing code is unchanged.
- `ChoiceAnswerOf.Ranked`, `ScoreAnswerOf.Level` and
  `ScoreAnswerOf.Description`.
- `LabelsOf`, the typed form of `Labels`.
- `UnknownAnswer`: an answer of a kind this version does not model is kept
  with its payload instead of being dropped.
- `RetryPolicy.MaxRetryAfter`, a minute by default: a server asking for a
  longer wait is not retried, and its request is returned in
  `APIError.RetryAfter`.
- `APIError.Attempts`.
- Validation of the API's limits before sending: 255 choices, 1 to 10
  non-null score levels, a `Noul` that asks something, and non-empty question
  names.
- Benchmarks, fixtures assembled from the API schema's examples under
  `testdata/`, a golden request encoding, and an integration test that records
  live responses for the unit tests to decode.

### Changed

- Each answer is decoded once rather than twice, validation no longer sorts
  or allocates, disabled logging no longer builds log records, and the base
  URL and protected headers are prepared once per client rather than per call:
  about 22% fewer allocations and 9% less time per call.

### Security

- The SDK's HTTP client refuses to follow redirects, which could otherwise hand
  the `Authorization` header to the same host over plaintext http, or any
  caller-set header to another origin entirely.
- Log records use a sanitized URL, so credentials in the base URL no longer
  reach log output.
- Header redaction matches by substring, covering caller-set names such as
  `X-Signature` and `Ocp-Apim-Subscription-Key`.
- Base URLs are restricted to https and http and rejected if they carry a query
  or fragment, which would otherwise swallow the endpoint path and send every
  request to the host root. A plaintext http host other than loopback warns.
- The response size cap no longer overflows at `math.MaxInt64`, which emptied
  every response body.
- CI actions are pinned to commit SHAs and tool versions are fixed.
- `APIError`, `TransportError` and `ResponseError`, with status-category
  sentinels matched through `errors.Is` and dotted field paths for unusable
  response bodies.
- `RetryPolicy` with exponential backoff, jitter, `Retry-After` support and a
  per-call time budget, configurable per client or per call.
- `slog` support with credential redaction, off by default.
