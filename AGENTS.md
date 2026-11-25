# Repository Guidelines

## Project Structure & Module Organization

`github.com/rshade/tsipam` is a single Go module that ships one binary,
`cmd/tsipam`. Treat its commands, flags, exit codes, JSON envelope, and the
`<tailnet>.ipam.hujson` registry format as compatibility contracts.

- `internal/` holds the private packages: `registry` (allocation rules and the
  `Store` contract), `registry/hujsonstore` (the HuJSON file backend),
  `registry/sqlitestore` (the SQLite backend and its `migrations/`),
  `registry/backend` (picks one from a registry URL),
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
- `cmd/tsipam` runs on `github.com/rshade/ax-go`, which no other package may
  import (`runtime_import_test.go`). `runtime.go` builds the tree and calls
  `ax.Execute`; `errors.go` is the one place that decides a failure's code, exit
  status, and redaction, from types and never from message text.
- A new leaf command needs a row in `leafContracts` (`contracts.go`), which sets
  its side-effect class, and with it rehearsal and approval. Its required inputs
  go through `bindInputs` (`inputs.go`), so each has a flag. Tests then ask for a
  dry-run case if it writes, an input case, and a decision in `pinnedTools`
  (`mcp_test.go`) on whether it is an MCP tool.
- `design/` is the goa DSL; `gen/` is its generated code. Never edit `gen/` by
  hand: change `design/`, run `mise run gen`, and commit both.
- Error text can reach stderr, a span, and an audit line, so it goes through the
  boundary first. Spans and logs hold no URL and no identity.
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
for behavior changes and run `mise run test ::: lint ::: cross` before review. Golden
files are byte-exact: regenerate with
`go test ./cmd/tsipam ./internal/planner ./internal/policycheck -update`, then
inspect every changed fixture; one command change can touch all three schema
goldens.
Preserve authentic bytes under `testdata/`. A test that reads human output asks
for it (`--format human` or `AGENT_MODE=human`), because a buffer is not a
terminal. `ax.Execute` replaces process-wide OpenTelemetry globals, so a test of
span output must not be parallel.

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

## Dependency Updates

Renovate opens one PR per dependency and cannot run project commands. Two
bumps need a local follow-up commit before CI passes:

- goa (`goa.design/goa/v3` in `go.mod` and the generator in `mise.toml`,
  grouped in `renovate.json`): run `mise run gen`.
- `npm:@fission-ai/openspec`: run `openspec update`, as above.

`commitlint` applies to Renovate's commits too, so `renovate.json` shortens
dependency names to keep subjects within 64 characters.

## Commit & Pull Request Guidelines

Use Conventional Commits; commitlint checks every PR commit. release-please
reads them to cut `vX.Y.Z` releases, so a breaking change needs `feat!:` or a
`BREAKING CHANGE:` footer.

If you edit in one checkout and commit that work from another worktree, stash the
originals once the commits contain them (`git stash push -u -- <paths>`), and
name the checkout and the stash in your report. Stash only the paths you
committed, because another session may share the checkout. Originals left behind
follow the next branch switch, and committing them reverts later fixes.

## Security

Never commit credentials, real tailnet names, or real email addresses. Use
`example.com` and `example.ts.net` in fixtures. See
[`SECURITY.md`](SECURITY.md).
