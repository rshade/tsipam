# Proposal: Separate client code from server code

## Why

`tsipam serve` (`add-serve-tsnet`) will run the goa service in a long-lived
process, and remote mode will run the client without it. Today both halves
live together: `internal/loopback` builds the handler, the listener, and the
client in one function and picks the storage backend, and `plan` and `check`
build the server inside a command. Without a boundary, `serve` would copy the
handler setup, remote mode would have to unpick it, and tracing middleware
from `adopt-ax-go-runtime` would exist in only one of the two.

## What Changes

- Add `internal/ipamhttp`, which builds the HTTP handler for the goa service.
  `loopback` uses it now and `serve` will use it later.
- Add `internal/ipamconn`, which builds the generated client for an address.
  `loopback` uses it now and remote mode will use it later.
- Reduce `internal/loopback` to the private Unix socket and its lifecycle. It
  no longer imports the service, the registry, or a storage backend.
- Add `cmd/tsipam/target.go`, the one place that assembles the local service.
  All eight call sites, including `plan` and `check`, go through it.
- Replace server types in CLI output code (`policycheck.Report` and the
  `planner` kind constants) with the CLI's own types and constants.
- Add an import test that fails when a command, the client packages, or the
  server packages cross the boundary. Remaining crossings are listed in the
  test, and a listed crossing that no longer exists also fails.

No user-visible behavior changes: commands, flags, exit codes, the JSON
envelope, error text, verbose diagnostic events, and registry bytes are
identical. `tsipam` stays one binary.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Every existing requirement describes behavior this change preserves, so
the change sets `skip_specs: true`.

## Impact

- **Code:** new `internal/ipamhttp` and `internal/ipamconn`;
  `internal/loopback`; a comment in `internal/policycheck/compare.go`; in
  `cmd/tsipam`, new `target.go` and edits to
  `calc.go`, `calc_aggregate.go`, `planning.go`, `planning_output.go`,
  `policy_check.go`, `policy_check_output.go`, `output.go`, `service.go`,
  `siteid.go`, `subnet.go`, and `udpport.go`.
- **Tests:** `internal/loopback/loopback_test.go` builds its own handler. One
  existing `cmd/tsipam` test file changes: in `policy_check_test.go`, the
  sanitization field guard moves to the new output types, and the offline
  contract test compares exact `policy_comparison` bytes, which the
  `internal/policycheck` goldens no longer pin for CLI output. Golden files
  and `smoke/` do not change.
- **API and dependencies:** `design/`, `gen/`, and `go.mod` do not change.
- **Other changes:** `adopt-ax-go-runtime`, `add-sqlite-store`, and
  `add-serve-tsnet` name code this change moves, and
  `add-registry-export-import` needs an entry in the import test. See the
  contact table in `design.md`.
