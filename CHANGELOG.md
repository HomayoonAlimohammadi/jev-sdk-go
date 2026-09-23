# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is below `v1.0.0` the public API may change between minor
releases. Nothing under `internal/` carries a compatibility promise.

## [Unreleased]

## [0.1.0] - 2026-09-23

### Added

- A Go client for the TypeSafe AI API, ported from the Python SDK. It needs
  Go 1.24 or later and has no dependencies outside the standard library.
- `Client.SystemOne`, `SystemOneAs[T]` and `Client.ListModels`, all
  context-aware and safe for concurrent use.
- `Noul`, `Choice`, `Score` and `RawQuestion` question types, validated
  against the API's limits before a request is sent: 1 to 255 choices, 1 to
  10 non-null score levels, a `Noul` that asks something, non-empty question
  names, and a `State` that encodes to a string, object or array.
- `Labels`, `LabelsOf` and `Levels`, which build criteria from plain values.
- Typed questions and answers: `ChoiceOf[T]` and `ScoreOf[L]` take labels and
  levels of your own types, and `Ask` returns a typed `Key` whose `Answer`
  reads the answer back in them, refusing a choice the question never offered
  or a level outside its rubric. `Choice` and `Score` are aliases for
  `ChoiceOf[string]` and `ScoreOf[int]`.
- `ChoiceAnswerOf.Ranked`, `ScoreAnswerOf.Level` and
  `ScoreAnswerOf.Description`.
- `UnknownAnswer`, which keeps an answer of a kind this version does not model
  together with its payload.
- A check that every question comes back answered, and answered in kind, so a
  missing answer fails instead of reading as a zero value.
- `APIError`, `TransportError` and `ResponseError`, with status-category
  sentinels matched through `errors.Is`, `APIError.Attempts`, and dotted
  field paths for unusable response bodies. A configuration value an option
  cannot use is reported as `ErrInvalidOption`.
- `RetryPolicy` with exponential backoff, jitter, `Retry-After` support and a
  per-call time budget, configurable per client or per call. A server asking
  for a wait longer than `MaxRetryAfter`, a minute by default, is not retried,
  and its request is returned in `APIError.RetryAfter`.
- `slog` logging, off by default, with credentials redacted from headers and
  URLs. Request and response bodies are logged only with `WithBodyLogging`.
- A connection pool of the client's own, sized for concurrent use of one host.
- A 1 MiB cap on response bodies, configurable with `WithMaxResponseBytes`.
- Hardening of the HTTP path: redirects are refused, so credentials never
  follow one; base URLs must be https or http without a query or fragment;
  and a plaintext http host other than loopback logs a warning.
- Documentation for using the SDK through OpenRouter and through gateways of
  your own.
- Eleven runnable examples under `examples/`, each tested against an
  in-process fake of the API.
- A skill for coding agents that use the SDK, under `skills/jev-sdk-go`.

[Unreleased]: https://github.com/HomayoonAlimohammadi/jev-sdk-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/HomayoonAlimohammadi/jev-sdk-go/releases/tag/v0.1.0
