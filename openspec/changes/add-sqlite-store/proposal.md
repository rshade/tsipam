# Proposal: SQLite registry backend

## Why

Several people and CI jobs need to reserve address space concurrently, and
the planned `tsipam serve` deployment needs a store that keeps an audit trail
without depending on git history. A single-node SQLite database covers that
workload: registries are small, writes are human- or CI-paced, and tsipam is
not in the tailnet's data path. `refactor-registry-store` gives the rules a
`Store` seam, so a second backend is an addition rather than a fork.

## What Changes

- Add an embedded SQLite backend (`internal/registry/sqlitestore`) that
  implements the `registry.Store` contract and passes the shared `storetest`
  conformance suite. It uses the pure-Go `modernc.org/sqlite` driver, so
  builds stay cgo-free on every released OS.
- Add backend selection: a `--registry <url>` flag on every registry-backed
  command and a `TSIPAM_REGISTRY` environment variable. Supported schemes are
  `file://<dir>` (the default, `file://.`) and `sqlite://<path>`.
  `--registry-dir` stays as an alias for `--registry file://<dir>`.
- Record one append-only audit event per registry mutation in the SQLite
  backend, with a nullable actor that a later server change fills in.
- Add `tsipam registry history` to list those events, with human and JSON
  output and sanitization. On the file backend it fails with a clear
  "requires the sqlite backend" error and exit 2.
- Enforce integrity in the database as a backstop to the rules: primary keys
  per map, `CHECK` ranges for site IDs and ports, and a trigger that refuses a
  subnet pool overlapping a pool held by a different site.
- Extend sanitization so database paths and identity values read from SQLite
  are redacted exactly as file-backed values are today.
- Document the backend, its single-writer and local-disk requirements, and
  migration from a HuJSON registry through `registry import`.

No existing behavior changes when `--registry` and `TSIPAM_REGISTRY` are
unset: the file backend stays the default and its bytes, errors, and exit
codes are unchanged.

## Capabilities

### New Capabilities

- `registry-backends`: Selecting a registry storage backend by URL, the
  guarantees every backend shares, and the SQLite backend's storage, locking,
  integrity, and file-safety requirements.
- `registry-history`: The append-only audit record of registry mutations and
  the `registry history` command that reads it.

### Modified Capabilities

- `schema`: Registry-backed commands also list the `registry` flag, and the
  catalog includes `registry history`.

## Impact

- **Code:** new `internal/registry/sqlitestore`; backend selection in
  `cmd/tsipam` (flag parsing, `TSIPAM_REGISTRY`, sanitizer registration);
  `cmd/tsipam/target.go` builds the service from a selected `Store`; a new
  history method in `design/` with regenerated `gen/`.
- **Dependencies:** `modernc.org/sqlite` and its `modernc.org/*` runtime
  modules (BSD-3-Clause, already in the license allowlist).
- **Depends on:** `refactor-registry-store` (the `Store` contract and
  conformance suite), `refactor-client-server-boundary` (`target.go`), and
  `add-registry-export-import` (the `registry` command group and `import`, used
  for migration).
- **Enables:** `add-serve-tsnet`, which runs the SQLite backend behind a
  Tailscale Service and fills the audit actor from `WhoIs`.
- **Out of scope:** Postgres, multiple writers or replicas, expiring
  reservations, and `--server`.
