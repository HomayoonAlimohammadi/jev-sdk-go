# Working on jev-sdk-go

Instructions for coding agents changing this repository. To *use* the SDK from
another project, read [`skills/jev-sdk-go/SKILL.md`](skills/jev-sdk-go/SKILL.md)
instead; it installs into that project with
`npx skills add HomayoonAlimohammadi/jev-sdk-go --skill jev-sdk-go`.

## Layout

| Path | Role |
|---|---|
| `*.go` (package `jev`) | The public API and the domain: questions, answers, validation, errors, retry policy |
| `internal/transport` | HTTP mechanics: sending, retries, body limits, redaction and logging. Knows nothing of questions or answers |
| `internal/fakeapi` | An in-process stand-in for the API, for tests only |
| `examples/` | One runnable program per use case, each tested against `internal/fakeapi` |
| `testdata/` | Response fixtures from the API schema's examples, the golden request, and live captures when recorded |
| `skills/jev-sdk-go` | The installable skill for SDK consumers |
| `.claude/skills` | Procedures for maintainers: `verify`, `api-change`, `release` |

## Rules

- **Standard library only.** The module has no dependencies; keep it that way.
- **Go 1.24 is the floor.** No language feature or standard-library symbol newer than the `go` line in `go.mod`; `go vet` reports them.
- **Every exported identifier is contract.** Before v1, breaking changes go under Changed in `CHANGELOG.md`.
- **Decoding is single-pass and strict.** Required answer fields are pointers in `answerWire`, each kind lists the fields it uses in `fieldError`, and `fieldTarget` types the masked-error recheck. Read `.claude/skills/api-change/SKILL.md` before touching any of it.
- **Tests** are table-driven, and call `t.Parallel()` unless they touch process-wide state such as `http.DefaultTransport` or environment variables. A bug fix starts with a test that fails.
- **Examples** follow one shape: `run(ctx, out, opts ...jev.ClientOption)`, with options applied last so the test can point the program at the fake.
- **Comments** explain why, not how.

## Procedures

These live in `.claude/skills`, where Claude Code loads them. Any agent can read
them as plain Markdown:

- [`verify`](.claude/skills/verify/SKILL.md): the checks to run before calling a change done.
- [`api-change`](.claude/skills/api-change/SKILL.md): adapting to a change in the API.
- [`release`](.claude/skills/release/SKILL.md): preparing and publishing a version.

Never commit, push, tag or publish without the maintainer's explicit approval.
