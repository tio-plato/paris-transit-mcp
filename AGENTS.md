# Agent instructions

paris-transit-mcp is a read-only MCP server for Île-de-France public transport on Île-de-France Mobilités' PRIM
data, written in Go. See README.md for what it does.

## Agent git policy

- Target branch: `main`.
- Setup: `nix develop -c true`.
- Checks: `nix flake check`. It builds the package, which runs every test, and runs golangci-lint.
- Landing: push the commit SHA to `main` (`git push origin <sha>:main`).
- Shipping: there is no release step.
- Commit messages follow Conventional Commits.

## No personal information

- The repository holds no personal information: no PRIM token, home or work address, coordinates of a place
  someone lives or goes to, regular line or travel time. This covers code, tests, fixtures, the README, tool
  descriptions and commit messages, for the whole history.
- Fixtures in `testdata/` are PRIM responses to queries about public places only: stations, and public squares
  such as Châtelet or the Forum des Halles. Store response bodies only, never request headers.
- Examples in the README and tool descriptions use stations and public places too.

## PRIM

- Every PRIM request goes through the rate limiters in `internal/prim`: no request bypasses them. No automatic
  retry: a retry spends quota, and the client can call the tool again.
- The token is sent only in the `apiKey` header. It never appears in an error, a log line or a tool result.
- Tests talk only to the loopback fake server. A live request to PRIM spends the token owner's daily quota.

## Error handling

From [go-react-practice's go-error-handling.md](https://github.com/oott123/skills/blob/cf758dcbb59fa322c24c16b74143479f5b101337/go-react-practice/go-error-handling.md):

1. Wrap an error from another package or an interface method with `fmt.Errorf("<what this layer was doing>: %w",
   err)`. The text is a lowercase verb phrase (`read request log: %w`), without `failed to`, `error` or
   `unable to`, without trailing punctuation, and does not repeat what the cause already says.
2. Functions in the same package may pass errors through unchanged.
3. An error a function creates itself includes its key arguments.
4. Use `%w` for each error one `fmt.Errorf` references; collect parallel errors with `errors.Join`.
5. Errors are printed once, at the outermost layer: `main` writes them to stderr, and the tool handlers turn them
   into MCP tool errors. No layer both logs and returns the same error.
6. stdout is the MCP channel: anything else goes to stderr.
