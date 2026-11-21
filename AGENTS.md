# Agent instructions

MCP server for Paris public transport on PRIM data, in Go. See README.md.

## Agent git policy

- Target branch: `main`.
- Setup: `nix develop -c true`.
- Checks: `nix flake check` (build with all tests, golangci-lint).
- Landing: `git push origin <sha>:main`.
- Releases: cut one when the user asks, from the card that asked. Tag a commit already on `origin/main` whose `ci`
  run there passed: `git tag -s vX.Y.Z -m vX.Y.Z <sha>`, then `git push origin vX.Y.Z`. CI checks it, builds a draft
  release, smoke-tests it on every platform and publishes it. Don't create the release on GitHub yourself: CI
  uploads into a release that already exists, skipping the draft and the smoke test.
- Never move or delete a pushed tag: the Go module proxy and checksum database keep the first content they saw,
  so `go install` fails for anyone who got the old one. If a release is broken, fix it and release the next
  version; delete a draft that failed its smoke test.
- Versions follow SemVer: `feat` bumps the minor version, only `fix` the patch. Before 1.0, breaking changes bump
  the minor version too.
- Conventional Commits.

## Writing

- Keep comments, docs and messages short and plain. Don't explain what the code already shows; comment only the
  why that isn't obvious, like a PRIM quirk.

## No personal information

- No token, home or work address, places someone goes to, regular lines or travel times anywhere in the repo,
  history included.
- `testdata/` holds PRIM responses for public places only: stations, squares, and the public "12 rue Victor Hugo"
  example. Response bodies only, no headers. Examples in docs use stations and public places.

## PRIM

- All requests go through the rate limiters in `internal/prim`. No retries.
- The token only goes in the `apiKey` header, never in errors, logs or results.
- Tests use the local fake server. Live requests use up the token owner's quota.

## Errors

Following [go-error-handling.md](https://github.com/oott123/skills/blob/cf758dcbb59fa322c24c16b74143479f5b101337/go-react-practice/go-error-handling.md):

- Wrap errors from other packages: `fmt.Errorf("read request log: %w", err)`. Lowercase, no "failed to".
- Errors you create include the key arguments.
- Report an error once, at the top: stderr in `main`, a tool error in the handlers. stdout is the MCP channel.
