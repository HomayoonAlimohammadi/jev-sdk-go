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
