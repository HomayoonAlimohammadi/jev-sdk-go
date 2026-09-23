---
name: api-change
description: Use when TypeSafe's System One API adds, removes or changes a response field, an answer or question kind, a validation limit or an endpoint, and the jev-sdk-go SDK must follow it.
---

# Follow an API change

Answers are decoded in a single strict pass, so a new field reaches more places than the struct that exposes it. Most misses fail silently: a zero where a value belongs, not an error.

## Decide first

- **Required or optional?** A required field rejects every response from a backend that does not send it yet, OpenRouter and gateways included. Model it as optional (a pointer, nil when absent) until every route sends it.
- `SystemOneAs[T]` validates nothing, so a missing field there is a silent zero. Mention that in the field's doc comment if it matters.

## A new answer field

1. **`answer.go`**
   - The public answer type (`ChoiceAnswerOf` and so on): add the field with a `json` tag. `SystemOneAs` decodes straight into these types.
   - `answerWire`: add it as a pointer, so absent and zero differ.
   - `(*answerWire).decode`: add its name to that kind's `fieldError(...)` list, a missing-field case if it is required, and the assignment. Without the name in the list, a mistyped value is accepted as a zero.
   - `fieldTarget`: map the name to its Go type. The default is `float64`, so a string field left out makes the masked-error recheck reject valid responses.
2. **`typed.go`**: the kind's `decodeAnswer` rebuilds the answer field by field. Copy the new field, or `Key.Answer` silently drops it.
3. **`internal/fakeapi`**: emit the field, or every example test fails.
4. **Every test body carrying an answer of that kind** (`systemOneBody`, `typedBody`, `benchBody`, the fixture tests): every answer is decoded, not only those asked about.
5. **`testdata/responses/`**: take values from the OpenAPI schema's example. Re-record `testdata/live` with the integration test's `-record` flag when an API key is available.
6. **Tests**: the field missing, `null`, of the wrong type, and masked (an irrelevant mistyped field before it). The masked case is what guards `fieldTarget`.
7. **Docs**: the README, `doc.go`, `skills/jev-sdk-go/SKILL.md` and the changelog.

## Other changes

- **A new answer kind**: `isModeledKind`, a `decode` case, a type implementing `Answer`, `partition`, a typed `decodeAnswer`, and the fake. Until then it arrives as `UnknownAnswer`, so nothing breaks.
- **A new or changed limit**: the constants in `question.go`, with their source (documented or observed), and validation tests at the boundary.
- **Anything exported** is contract. Before v1, record it under Changed; from v1 on, a breaking change needs a new major version.

Finish with the verify skill.
