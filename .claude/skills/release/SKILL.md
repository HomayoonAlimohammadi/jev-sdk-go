---
name: release
description: Use when asked to prepare, cut, tag, publish or bump a version of the jev-sdk-go module, or to check whether it is ready to release.
---

# Release a version

A Go module version is a git tag, and it is permanent once the module proxy has fetched it: deleting or moving the tag breaks every user's checksums. A bad release is fixed by a new version plus a `retract` directive in `go.mod`, never by re-tagging.

**Approval to prepare a release is not approval to commit, push, tag or publish it.** Each of those waits for the maintainer's explicit go-ahead.

## Prepare (no approval needed)

1. Run the verify skill's full gate.
2. Changelog: rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, add a fresh empty `## [Unreleased]` above it, check every entry sits under the right heading, and add link references at the bottom. For a first release, "Changed" and "Fixed" relative to states never released mean nothing to users; fold them into Added.
3. Nothing that should not ship is tracked: the module zip is every tracked file at the tag.
4. API compatibility, on a committed tree (it refuses uncommitted changes):
   - First release: `go run golang.org/x/exp/cmd/gorelease@latest -version=vX.Y.Z`
   - Later ones: add `-base=vPREVIOUS`. Before v1 a breaking change needs a minor bump; from v1, a new major version and module path suffix `/vN`.
   Run it in a scratch clone if the working tree is dirty.
5. Draft the release notes from the changelog section.

## Publish (each step only after explicit approval)

1. Commit, following the repository's signing and sign-off conventions (`git log --show-signature -3`).
2. `git push origin main`, then wait for CI to pass on that exact commit: `gh run watch`.
3. Tag the commit CI passed, signed and annotated: `git tag -s vX.Y.Z -m vX.Y.Z <sha>`.
4. Push that one tag, never `--tags`: `git push origin vX.Y.Z`.
5. Make the version permanent and visible: `GOPROXY=https://proxy.golang.org go list -m github.com/HomayoonAlimohammadi/jev-sdk-go@vX.Y.Z`.
6. `gh release create vX.Y.Z --verify-tag --notes-file <notes>`.

## After

Smoke-test from a scratch module: `go get github.com/HomayoonAlimohammadi/jev-sdk-go@vX.Y.Z && go build ./...`.

## v0 or v1

Stay on v0 until the API has had real use, including calls against the live API, and a release or two without breaking changes. v1 freezes the API.
