# Agent instructions

MCP server for Paris public transport on PRIM data, in Go. See README.md.

## Agent git policy

- Target branch: `main`.
- Setup: `nix develop -c true`.
- Checks: `nix flake check` (build with all tests, golangci-lint).
- Landing: `git push origin <sha>:main`. No release step.
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
