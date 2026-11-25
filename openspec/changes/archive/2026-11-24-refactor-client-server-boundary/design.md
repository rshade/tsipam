# Design: Separate client code from server code

## Context

See `proposal.md` for motivation. The current shape:

- `loopback.RunWithLogger` takes a registry directory, builds a
  `hujsonstore`, a `Ledger`, and an `ipamsvc.Service`, and calls
  `RunWithService`. That function builds the endpoints, mux, server, listener,
  and client.
- Seven call sites in `cmd/tsipam` use `RunWithLogger`. `planning.go` builds
  its own `ipamsvc.Service` with a live collector and uses `RunWithService`.
- `output.go` and `policy_check_output.go` use `policycheck.Report` as the
  envelope type, and `planning_output.go` uses two `planner` constants.
- `main.go` reads the registry file through `hujsonstore` to seed the
  sanitizer.

A throwaway prototype of this design built, passed every test under
`cmd/` and `internal/` and the offline smoke cases, and left golden files
unchanged. The live and release-artifact smoke cases did not run. Its only
lint findings were formatting. The import test caught a seeded violation in
each direction and a stale exception.

## Goals / Non-Goals

**Goals:**

- The handler and the client are each built in exactly one place.
- A command cannot reach server code except through `target.go`.
- Later changes add a target or a middleware without moving code again.

**Non-Goals:**

- `tsipam serve`, `--server`, or any remote transport.
- Moving live reads to the server. `subnet allocate` and `calc next-free`
  still read devices in the client; `add-serve-tsnet` changes that for remote
  mode.
- Moving sanitizer seeding behind the store. `add-sqlite-store` does that.
- Rewriting `exitCodeFor`. Its registry error types come from the shared
  `registry` package, and `adopt-ax-go-runtime` replaces the function.
- A second binary. Local mode needs the server code in the CLI, so a client
  build would be a separate product decision.

## Decisions

### D1. Package roles

| Role | Packages |
| --- | --- |
| Shared | `gen/ipam`, `internal/diagnostics`, `internal/registry` (domain types, normalizers, typed errors) |
| Client | `gen/http/ipam/client`, `gen/http/cli/tsipam`, `internal/ipamconn`, `internal/loopback` |
| Server | `gen/http/ipam/server`, `internal/ipamhttp`, `internal/ipamsvc`, `internal/registry/hujsonstore`, `internal/registry/storetest`, `internal/planner`, `internal/policycheck`, `internal/devices` |
| Assembly | `cmd/tsipam/target.go` |

Client packages import only other client packages, `gen/ipam`, and
`internal/diagnostics`. Server packages import no client package.

`internal/registry` stays whole. The CLI uses its normalizers to register
sanitizer values. It also holds `Ledger`, but a `Ledger` needs a `Store`, and
only `target.go` may import a backend, so no second rule is needed.

*Alternative:* split `registry` into domain and rules packages. Rejected: it
moves most of the package to enforce something the import rule already does.

### D2. `internal/ipamhttp`

```go
func NewHandler(svc ipam.Service) http.Handler
```

It wraps the endpoints, muxer, server, and mount that `loopback` builds now,
with the same encoders and the same nil error handler and formatter. It takes
the generated interface, so it does not import `ipamsvc`.

It has no middleware parameter yet. `adopt-ax-go-runtime` adds its span
middleware inside, so local mode and `serve` both get it. `add-serve-tsnet`
adds the parameter its authorization needs.

### D3. `internal/ipamconn`

```go
func NewClient(scheme, host string, doer goahttp.Doer) *ipamclient.Client
```

It fixes the request encoder, response decoder, and body handling, so the
Unix-socket and HTTPS clients cannot differ. The name avoids `ipamclient`,
which ten files already use as the alias for the generated package.

### D4. `internal/loopback`

```go
func Run(ctx context.Context, handler http.Handler, logger diagnostics.Logger,
    fn func(context.Context, *ipamclient.Client) error) error
```

It replaces `RunWithLogger` and `RunWithService`. The socket directory,
timeouts, shutdown order, and diagnostic events stay as they are.

*Alternative:* keep `RunWithLogger` as a wrapper. Rejected: it would keep the
storage import in a client package.

### D5. `cmd/tsipam/target.go`

`registryTarget` holds the registry directory, the logger, and an optional
live collector. `run(ctx, fn)` builds the service and the handler and calls
`loopback.Run`. The default live collector, with its `TAILSCALE_TAILNET`
override, moves here from `planning.go`. Command code calls
`state.registryTarget(registryDir).run(...)`.

Later changes extend this file: `add-sqlite-store` replaces the directory
with a `Store`, and `add-serve-tsnet` adds a server URL as the other kind of
target. `add-serve-tsnet` already planned a file with this name and purpose.

The order of checks in `plan` and `check` does not change: the sanitizer and
the `TAILSCALE_TAILNET` check still run before the target is built.

### D6. CLI output types

`policyComparisonOutput`, `policyAuthoredOutput`, and `policyReservedOutput`
replace the `policycheck` types with the same field names and JSON tags. This
follows `comparisonOutput`, which `plan` already uses.

The sanitization guard in `policy_check_test.go` checks the `policycheck`
types today. It must move to the new types in the same commit. Left as it is,
it would still pass while guarding a type the output no longer uses. The
`internal/policycheck` goldens have the same gap for JSON field names, so
`TestPolicyCheckOfflineContract` compares the exact `policy_comparison` bytes.

The two `planner` constants become CLI constants. A test asserts that they
equal `planner`'s, because a mismatch would silently change which values
sanitization redacts.

### D7. The import test

`cmd/tsipam/boundary_test.go` parses the imports of non-test files with
`go/parser` and checks three things:

1. Each file in `cmd/tsipam` imports only the shared packages and the
   generated client, plus the entries listed for that file.
2. Each package under `internal/` and `gen/` imports only what its role
   allows. A package with no role fails, so a new package needs a decision.
3. Every listed entry is still used.

The initial list:

| File | Listed imports | Until |
| --- | --- | --- |
| `target.go` | `devices`, `ipamhttp`, `ipamsvc`, `loopback`, `registry/hujsonstore` | Permanent; later changes swap the backend |
| `devices.go` | `devices` | Permanent: `devices list` reads the Tailscale API from the caller's machine |
| `main.go` | `registry/hujsonstore` | `add-sqlite-store` moves sanitizer seeding to `IdentitySource` |

Test files may import anything, because they build fixtures.

*Alternative:* `depguard` in `.golangci.yml`. Rejected: it cannot detect a
stale exception, and the repository avoids lint-config edits for this.

## Points of contact with in-flight changes

| Change | Contact |
| --- | --- |
| `adopt-ax-go-runtime` | Span middleware and `traceparent` extraction go in `ipamhttp`; the injecting round tripper goes in `ipamconn`. |
| `add-registry-export-import` | Its commands parse documents with `hujsonstore`, so they need an entry in the import test. |
| `add-sqlite-store` | Its opened `Store` replaces the directory in `target.go`, `internal/registry/backend` replaces `hujsonstore` in that file's entry, and it removes the `main.go` entry. |
| `add-serve-tsnet` | Extends `target.go`, reuses `ipamhttp` and `ipamconn`, and gives `internal/server` and `internal/remote` roles. |
| `add-server-observability` | Its new packages (`redact`, `usage`, `drift`, `webui`) need roles; `redact` and `usage` are shared. |

## Risks / Trade-offs

- [A refactor changes behavior that tests do not cover] → Golden files and the
  smoke suite stay untouched, and a task compares verbose events and output
  with `main`.
- [The import test becomes friction] → It names the file and the import, and
  the list sits beside it. The friction is the point for server imports.
- [Two very small packages] → Accepted. Each holds one decision that must not
  differ between local and remote mode, and neither can import the other side.
- [Other plans go stale] → The contact table, and a task that checks them.
