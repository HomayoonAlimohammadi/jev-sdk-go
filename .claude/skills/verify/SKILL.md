---
name: verify
description: Use when a change to the jev-sdk-go repository is about to be called done, verified, ready to commit or ready for review, or when CI fails on one platform or Go version while passing locally.
---

# Verify a change

CI tests Go 1.27 on Linux, macOS and Windows and Go 1.24 (the `go.mod` floor) on Linux, and runs a pinned linter and vulnerability scan. A local check that covers less than that proves less than it seems.

## The gate

Run from the repository root. Every line must be clean.

```sh
gofmt -l .                                          # prints nothing
go vet ./... && go vet -tags=integration ./...      # the tag covers integration_test.go
for tc in go1.24.13 go1.27.1; do GOTOOLCHAIN=$tc go test ./... -race -count=1 || break; done
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

- `GOTOOLCHAIN=goX.Y.Z` downloads that toolchain on first use; nothing to install. Use the newest patch of each release: `go list -m -versions golang.org/toolchain`.
- The linter and scanner run through `go run` at the versions pinned in `.github/workflows/ci.yml`. They need no local install, unlike `make lint`. Keep the pins here and in CI identical.
- `./...` includes every program under `examples/`, each tested against the in-process fake in `internal/fakeapi`.
- For a change to decoding or to the request path, also compare `go test -run '^$' -bench . -benchmem .` before and after. Allocation guards (`TestValidateQuestionsAllocatesNothing`, `TestDisabledLoggingAllocatesNothing`) fail on regressions.

## Traps this repository has hit

| Symptom | Cause | Fix |
|---|---|---|
| A test passes alone but fails in the full suite | It depends on `http.DefaultTransport`'s idle pool, which every `httptest.Server.Close` clears | Such a test must not call `t.Parallel()` |
| The golden request test fails only on Windows | CRLF line endings from the Windows checkout | `matchesGolden` normalizes line endings; do not regenerate goldens to compensate |
| `govulncheck` panics inside `x/tools` | The pinned version predates the Go toolchain | Raise the pin, in CI and here together |
| A test passes on 1.27 and fails on 1.24 | `encoding/json` behaviour differs between them (for example, map-value error paths) | Assert what every supported version guarantees; see `TestMistypedMapValueIsReported` |

## Not verification
- Passing `-update` or `-record` to make a failing test pass: both rewrite `testdata/` and hide the diff.
- A green run on one toolchain.
- A green CI run on a different commit from the one being shipped.
