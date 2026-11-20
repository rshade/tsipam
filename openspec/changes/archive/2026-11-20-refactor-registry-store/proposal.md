# Proposal: Registry store abstraction

## Why

`tsipam` is moving toward a shared `tsipam serve` deployment backed by SQLite
alongside the existing HuJSON file. Today `internal/registry` interleaves the
allocation rules (normalization, collision and overlap checks, metadata merge
and `reservedAt` stamping, ticket replay, next-free selection) with HuJSON file
mechanics (lockfile, raw key spellings, member presence, comment-preserving
patches, malformed-map repair) inside each `...WithLogger(path, ...)` function.
A second backend added beside that code would have to duplicate every rule,
and the two copies would drift. Separating the rules from storage first makes
a later backend an addition rather than a fork.

## What Changes

- Introduce a backend-agnostic storage seam in `internal/registry`: a `Store`
  that runs read-only `View` and serialized read-decide-write `Update`
  transactions per tailnet routing key, exposing per-map snapshots and
  put/delete mutations.
- Move every allocation rule into one rules layer (`registry.Ledger`) that
  runs against any `Store`. Rules are written once.
- Move all HuJSON specifics into a new `internal/registry/hujsonstore`
  package: file naming, the lockfile, `{}` creation, BOM and near-miss key
  rejection, raw-spelling and presence tracking, comment-preserving patches,
  scoped per-map errors, malformed-map repair, the sanitizer's raw-entry
  readers, and the "missing directory versus missing file" error text.
- Replace the planner's dependency on the file-shaped `registry.Registry`
  struct with a plain allocation snapshot.
- Rewire `internal/ipamsvc` and `internal/loopback` to hold a `Store`
  instead of a registry directory. CLI flags, the goa design, and `gen/` do
  not change.
- Add a shared, table-driven `Store` conformance suite and run it against the
  HuJSON store and an in-memory reference store, so the contract is proven
  backend-agnostic before a SQL backend exists.

No user-visible behavior changes: commands, flags, exit codes, the JSON
envelope, verbose diagnostic events, error text, and the bytes written to
`<tailnet>.ipam.hujson` are identical. The existing golden files, contract
tests, and release smoke suite are the evidence.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Every existing requirement, including comment-preserving persistence,
locked next-free allocation, and best-effort release repair, describes
behavior this change preserves. The change sets `skip_specs: true`.

## Impact

- **Code:** `internal/registry` (split), new `internal/registry/hujsonstore`,
  new `internal/registry/storetest`, `internal/ipamsvc`, `internal/loopback`,
  `internal/planner`, and the sanitizer setup in `cmd/tsipam/main.go`.
- **Tests:** file-level tests in `internal/registry` move to `hujsonstore`;
  rule-level cases move to the conformance suite. `cmd/`, `smoke/`, and
  golden fixtures stay unchanged and must pass untouched.
- **Dependencies:** none added.
- **Follow-ups this enables:** `add-registry-export-import`,
  `add-sqlite-store`, and `add-serve-tsnet`.
