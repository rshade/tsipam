# Repository Guidelines

## Project Structure & Module Organization

`github.com/rshade/tsipam` is a single Go module that ships one binary,
`cmd/tsipam`. Treat its commands, flags, exit codes, JSON envelope, and the
`<tailnet>.ipam.hujson` registry format as compatibility contracts.

- `internal/` holds the private packages: `registry` (allocation rules and the
  `Store` contract), `registry/hujsonstore` (the HuJSON file backend),
  `registry/storetest` (the in-memory reference store and conformance suite),
  `ipamsvc` (the goa service every command runs through), `ipamhttp` (its HTTP
  handler), `ipamconn` (its generated client), `loopback` (a private Unix
  socket that serves a handler for one command), `planner` and `policycheck`
  (pure comparisons against live state), `devices` (tailnet inventory), and
  `diagnostics`.
- Client and server code stay separate. Commands reach server packages only
  through `cmd/tsipam/target.go`. `cmd/tsipam/boundary_test.go` enforces this,
  lists the few exceptions, and gives each package a role; add new packages
  there.
- `design/` is the goa DSL; `gen/` is its generated code. Never edit `gen/` by
  hand: change `design/`, run `mise run gen`, and commit both.
- `smoke/` is the release smoke suite behind the `smoke` build tag.
- `docs/` holds the guides. Tests sit beside the code as `*_test.go`; fixtures
  belong in `testdata/`.

Policy parsing, sanitization, and address arithmetic come from
`github.com/rshade/tsacl` (`pkg/policycore/...` and `pkg/ipcalc`).

## Build, Test, and Development Commands

Tool versions and tasks live in `mise.toml`; CI runs the same tasks.

```bash
mise install          # install the pinned toolchain
mise run build        # go build ./...
mise run test         # go test ./...
mise run vet          # go vet, including -tags smoke
mise run cross        # compile for linux, darwin, windows
mise run smoke        # release smoke suite against a source build
mise run gen          # regenerate gen/ from design/
mise run lint         # every check
```

`mise run lint` covers golangci-lint, gofmt, `go mod tidy`, the Go version pin,
the `gen/` drift check, markdownlint, actionlint, commitlint, govulncheck, the
dependency license allowlist, and both secret scanners. Run a single test with
`go test -run TestName ./path/to/pkg`.

## Coding Style & Naming Conventions

Format Go with `gofmt -s`/`goimports` and follow standard Go naming. The
`.golangci.yml` enforces strict shadowing and parallel tests; every test should
call `t.Parallel()`. Any `//nolint:<linter>` must include a reason. CLI results
belong on stdout and diagnostics on stderr; keep JSON parse-clean. Follow
[`docs/reference/ax-conventions.md`](docs/reference/ax-conventions.md).

## Testing Guidelines

Use Go's `testing` package and descriptive `TestXxx` names. Add regression tests
for behavior changes and run `mise run test lint cross` before review. Golden
files are byte-exact: regenerate with `go test ./... -update`, then inspect every
changed fixture. Preserve authentic bytes under `testdata/`.

The smoke suite asserts the README's examples byte for byte, so a README change
and its smoke case move together. `TSIPAM_SMOKE_LIVE=1` gates its credentialed,
read-only cases against a real, shared tailnet. They never run in ordinary CI;
the `Live verify` workflow compiles them on PRs and runs them only on manual
dispatch from `main`, after approval. See
[`docs/how-to/release-smoke.md`](docs/how-to/release-smoke.md).

## Secret Scanning

CI runs gitleaks (patterns, including a Tailscale key rule and a rule for any
MagicDNS name other than `example.ts.net`) and trufflehog (verified live
credentials) as required checks. An advisory trufflehog job lists unverified
findings by detector and location only. Run `mise run install-hooks` once per
clone: it writes a fixed pre-commit hook under `.git` that runs `gitleaks` on the
staged changes (not a mise task, so a checked-out branch cannot change what
runs), and refuses if `core.hooksPath` is set. A commit that stages a secret
is blocked locally.
Actions are pinned by commit SHA; Renovate keeps the pins current.

## Spec-Driven Changes (OpenSpec)

A change that adds or alters a capability, CLI surface, or machine-readable
output goes through OpenSpec (`openspec/`) before implementation. A contained
fix does not. Specs describe behavior that exists; archive a change once its PR
merges.

The pinned `npm:@fission-ai/openspec` CLI generates files under `.agents/`,
`.claude/` and `.github/`. The `OpenSpec files` workflow runs `openspec update`
on every PR and fails if it changes anything, so after a Renovate bump run
`mise exec npm:@fission-ai/openspec -- openspec update` and commit the result.

## Commit & Pull Request Guidelines

Use Conventional Commits; commitlint checks every PR commit. release-please
reads them to cut `vX.Y.Z` releases, so a breaking change needs `feat!:` or a
`BREAKING CHANGE:` footer.

## Security

Never commit credentials, real tailnet names, or real email addresses. Use
`example.com` and `example.ts.net` in fixtures. See
[`SECURITY.md`](SECURITY.md).
