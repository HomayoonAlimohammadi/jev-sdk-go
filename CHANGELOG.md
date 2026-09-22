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
- `APIError`, `TransportError` and `ResponseError`, with status-category
  sentinels matched through `errors.Is` and dotted field paths for unusable
  response bodies.
- `RetryPolicy` with exponential backoff, jitter, `Retry-After` support and a
  per-call time budget, configurable per client or per call.
- `slog` support with credential redaction, off by default.
