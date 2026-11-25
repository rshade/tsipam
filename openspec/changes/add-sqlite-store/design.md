# Design: SQLite registry backend

## Context

See `proposal.md` for motivation. This change builds on:

- `refactor-registry-store`: `registry.Store` (`View`/`Update` with
  `TxOptions{CreateIfMissing, Require}`), the typed `Tx` mutations,
  `registry.Ledger` holding every rule, `registry.ErrNotFound`,
  `registry.CommitError`, and `storetest.Run` with an in-memory reference
  store. The contract's eight rules (not found, serialization, atomicity,
  read-your-writes, scoped validation, canonical keys, fail-closed deletes,
  commit errors) are what this backend must meet.
- `add-registry-export-import`: the `tsipam registry` command group and
  `registry import`, which is the migration path from HuJSON to SQLite.

Current CLI wiring: `addRegistryFlags` (`cmd/tsipam/siteid.go`) defines
`--registry-dir` (default `.`) and `--tailnet`, and every registry command
reaches `ipamsvc` through `cmd/tsipam/target.go`, which builds the service
over a `hujsonstore` and serves it on `internal/loopback`. After execution,
`registerKnownIdentityInventory` (`cmd/tsipam/main.go`) registers the
registry directory, file path, and every raw identity value from the file for
the free-text error sweep. The existing lockfile waits at most 5 seconds.

`modernc.org/sqlite` v1.57.0 is in the local module cache. It is pure Go
(transpiled), supports linux, darwin, and windows on amd64, and accepts
`_txlock=immediate` and repeated `_pragma` DSN parameters. Its dependency
tree is BSD-3-Clause, MIT, and Apache-2.0, all within the
`mise run license-check` allowlist.

## Goals / Non-Goals

**Goals:**

- A second `Store` that passes `storetest.Run` unchanged.
- Backend selection that leaves every existing invocation byte-identical.
- An audit record whose actor `add-serve-tsnet` can fill without changing
  the schema or the `Store` contract again.

**Non-Goals:**

- Postgres, replicas, or more than one writer process.
- Converting between backends at runtime. Migration is
  `registry export` followed by `registry import` (owned by
  `add-registry-export-import`).
- Expiring reservations, history pruning, or history export.
- Running tsipam on a network filesystem. This is documented as unsupported
  rather than detected.

## Decisions

### Package and connection layout

New package `internal/registry/sqlitestore`, which imports `registry` only.
`Open(path string, logger diagnostics.Logger) (*Store, error)` does not touch
the filesystem. Files are created or opened lazily per transaction, so a
`View` on a missing file can return `ErrNotFound` without creating anything.

Each `Store` holds two `*sql.DB` handles on the same file:

- **Writer:** `SetMaxOpenConns(1)`, DSN
  `_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)`.
  `Update` runs as `BEGIN IMMEDIATE`, so the write lock is taken before the
  read-decide-write span starts. That gives contract rule 2 across processes.
- **Reader pool:** the same pragmas plus `query_only(1)`. `View` runs in a
  deferred read transaction, which gives a consistent WAL snapshot.

`busy_timeout(5000)` matches the lockfile's 5-second wait. `SQLITE_BUSY`
maps to the same "timed out after 5s waiting for a concurrent tsipam to
finish" error, so exit codes and messages agree across backends.
`synchronous(FULL)` costs little at this write rate and keeps the last
reservation durable through a power loss.

*Alternative:* one pool with `SetMaxOpenConns(1)` for everything. It is
simpler, but a long `plan` read would block writers in the same process. Two
handles cost nothing.

### Creation and absence

- `Update` with `CreateIfMissing`: if the file is missing, create it with
  `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0o600)` before the driver
  opens it, so SQLite never creates it with umask defaults. SQLite creates
  `-wal` and `-shm` with the main file's mode. Then migrate, and insert the
  `tailnets` row if it is absent.
- `View`, and `Update` without `CreateIfMissing`: `os.Stat` first. A
  missing file, or a missing `tailnets` row, returns an error wrapping
  `registry.ErrNotFound` that says "no registry for tailnet %q in the
  selected sqlite database". The path is not included (see sanitization).
- Existing file permissions are never changed.

### Schema (version 1)

```sql
CREATE TABLE tailnets (
  id INTEGER PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE site_ids (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  site_id INTEGER NOT NULL CHECK (site_id BETWEEN 0 AND 65535),
  site TEXT NOT NULL CHECK (site <> ''),
  owner TEXT, description TEXT, ticket TEXT, reserved_at TEXT,
  PRIMARY KEY (tailnet_id, site_id)
) WITHOUT ROWID;

CREATE TABLE subnet_pools (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  cidr TEXT NOT NULL,
  family INTEGER NOT NULL CHECK (family IN (4, 6)),
  range_start BLOB NOT NULL CHECK (length(range_start) = 16),
  range_end BLOB NOT NULL CHECK (length(range_end) = 16),
  site TEXT NOT NULL CHECK (site <> ''),
  owner TEXT, description TEXT, ticket TEXT, reserved_at TEXT,
  PRIMARY KEY (tailnet_id, cidr),
  CHECK (range_start <= range_end)
) WITHOUT ROWID;
CREATE INDEX subnet_pools_range ON subnet_pools (tailnet_id, family, range_start);

CREATE TABLE service_addresses (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  address TEXT NOT NULL,
  service TEXT NOT NULL CHECK (service LIKE 'svc:_%'),
  owner TEXT, description TEXT, ticket TEXT, reserved_at TEXT,
  PRIMARY KEY (tailnet_id, address)
) WITHOUT ROWID;

CREATE TABLE udp_ports (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  site TEXT NOT NULL CHECK (site <> ''),
  port INTEGER NOT NULL CHECK (port BETWEEN 1024 AND 65535),
  device TEXT NOT NULL CHECK (device <> ''),
  PRIMARY KEY (tailnet_id, site, port)
) WITHOUT ROWID;

CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  at TEXT NOT NULL,                -- RFC 3339 UTC
  action TEXT NOT NULL CHECK (action IN ('reserve','allocate','release','import')),
  kind TEXT NOT NULL CHECK (kind IN ('site_id','subnet_pool','service_address','udp_port')),
  key TEXT NOT NULL,
  site TEXT,                       -- UDP ports only
  before_value TEXT,               -- JSON, NULL when absent
  after_value TEXT,                -- JSON, NULL when absent
  actor TEXT                       -- NULL for local commands
);
CREATE INDEX events_by_tailnet ON events (tailnet_id, id DESC);
CREATE INDEX events_by_key ON events (tailnet_id, kind, key, id DESC);
```

Addresses are stored as 16 bytes, with IPv4 in its IPv4-mapped form, so
within one `family` a byte-wise BLOB comparison is a numeric comparison.

A cross-site overlap trigger fires `BEFORE INSERT` and
`BEFORE UPDATE OF site, range_start, range_end, family` on `subnet_pools`. It
raises `ABORT` when another row in the same tailnet and family has
`range_start <= NEW.range_end AND NEW.range_start <= range_end` and a
different site. Events get `BEFORE UPDATE` and `BEFORE DELETE` triggers that
raise `ABORT`, making the table append-only at the storage layer.

`reserved_at` stays RFC 3339 UTC text, exactly as the file stores it, so no
timestamp format conversion can change output. Metadata columns are `NULL`
when unset, mapped to `""` at the `registry.Entry` boundary.

*Why the Ledger stays primary:* typed errors such as
`SubnetPoolCollisionError` (exit 1, HTTP 409) come from the Ledger's checks
inside the same `BEGIN IMMEDIATE` span. The trigger only catches a Ledger
bug or an out-of-band writer. A trigger abort reaching tsipam surfaces as an
exit-2 integrity error, never as a silent success.

### Reading rows defensively

Because other programs can write the file, `Snapshot` accessors re-run each
row's key and value through the `registry` normalizers and recompute each
pool's range from its `cidr`. Any mismatch becomes that map's scoped error,
which is exactly how `hujsonstore` treats an invalid map: `Require` fails
closed and other maps stay usable. `Delete*` on an invalid map returns an
error (contract rule 7). The SQLite backend has no repair path.

### Migrations

SQL files are embedded as `migrations/0001_init.sql` and onward, and the
version is tracked with `PRAGMA user_version`. On each transaction's first
open, a writer connection compares `user_version` with the latest embedded
version:

- **Equal:** proceed.
- **Lower:** apply the missing files inside one `BEGIN IMMEDIATE`, then set
  `user_version`.
- **Higher:** return an error saying the database needs a newer tsipam,
  without writing anything.

A `View` against an old database therefore takes the write lock once to
upgrade it, which matches the spec's "upgrade before the command proceeds."

*Alternative:* a migrations library such as goose or golang-migrate. That is
a new dependency for at most a handful of files, and `user_version` is
atomic with the schema change.

### Audit events: action from options, actor from context

The `Store` contract grows two small additions in `registry`:

- `TxOptions.Action` (`reserve`, `allocate`, `release`, `import`). The
  Ledger sets it on every `Update`.
- `registry.WithActor(ctx, actor string)` and `registry.ActorFrom(ctx)`. The
  local CLI never sets an actor. `add-serve-tsnet` sets it from `WhoIs` in
  its HTTP middleware, so it reaches the store through the existing context
  path: goa handler → `ipamsvc` → `Ledger` → `Store`.

`sqlitestore`'s `Put*` and `Delete*` read the row's prior value in the same
transaction and insert one `events` row. Its `at` comes from the Ledger's
injectable `now`, carried in `TxOptions.Now` so tests are deterministic.
`before_value` and `after_value` hold a JSON object of the entry (target plus
non-empty metadata). Because the Ledger only issues a `Put` when something
changed, "one event per changed reservation" holds without the store
comparing values. `hujsonstore` ignores `Action`, `Now`, and the actor.

*Alternative:* `Tx.Record(Event)` called explicitly by the Ledger. That
keeps the store dumber, but every Ledger method would need a matching call,
and forgetting one silently loses audit data. Recording at the mutation is
structural.

### Rehearsal

`Store` rule 9 (from `adopt-ax-go-runtime`) applies: `Update` with `DryRun`
validates, runs `fn`, discards everything, and creates nothing. A rolled-back
`BEGIN IMMEDIATE` does not meet it. It takes the write lock, can create the
database and its `-wal` and `-shm` files, and fails in a read-only directory. A
rehearsal therefore uses a read path: it stats the file first, supplies an empty
registry when the file or the `tailnets` row is absent and `CreateIfMissing` is
set, loads the four maps, and runs `fn` on an in-memory transaction. It never
migrates: a database with an older schema rehearses through the same queries
when they still apply, and otherwise fails with the error that asks for a real
command first. It records no event.

An ordinary read-only WAL connection creates `-wal` and `-shm` and cannot open
the database in a read-only directory, so the read path itself is an open
question below.

### Runtime contracts

`registry history` is `read-only` in `leafContracts` and joins the pinned MCP
tool set. Its `events` are read back from the database, so none of its fields
varies between identical runs. Output comparisons between the backends mask the
fields `__schema` publishes and the error object's `trace_id`.

### Reading history

There is a new optional interface in `registry`:

```go
type HistoryReader interface {
    History(ctx context.Context, tailnet string, filter HistoryFilter) ([]Event, error)
}
```

`Ledger.History` type-asserts the store and returns
`registry.ErrHistoryUnsupported` when the assertion fails. `ipamsvc` maps that
error to a new goa error, `unsupported`, which yields exit 2. The goa design
gains `list_history` (`GET /tailnet/{tailnet}/ipam/history` with `kind`,
`key`, `since`, and `limit` query attributes), with `gen/` regenerated.
`storetest` gains history cases that run when a store implements
`HistoryReader`. The in-memory store implements it, so the cases have two
implementations.

### Backend selection

New package `internal/registry/backend`:

- `Parse(raw string) (Selector, error)` splits on the first `://`, accepts
  only `file` and `sqlite`, takes the remainder verbatim as a path, and
  rejects an empty path.
- `Open(sel Selector, logger) (registry.Store, error)` returns
  `hujsonstore.New` or `sqlitestore.Open`.

`net/url` is deliberately not used. It would turn `file://./x` into a host
and mangle Windows paths.

In `cmd/tsipam`, `addRegistryFlags` adds `--registry`. A resolver applies
the precedence: explicit `--registry` or `--registry-dir` (both explicit is a
usage error), then `TSIPAM_REGISTRY`, then `file://.`. It uses
`Flags().Changed` so the `--registry-dir` default does not count as explicit.
`registryTarget` in `target.go` holds the opened `registry.Store` instead of a
directory, and the sanitizer setup reads `IdentitySource` from that store.

### Sanitization

- The CLI registers the raw `--registry` value and the parsed path with the
  free-text sweep, plus the base name for `sqlite://`, or the registry file's
  path and name for `file://`, as today. `internal/registry/backend` returns
  these values, and `target.go` passes them to the sanitizer setup.
- `registerKnownIdentityInventory` switches from calling
  `hujsonstore.RawEntries` directly to a new optional
  `registry.IdentitySource` interface, `IdentityValues(ctx, tailnet)`, which
  returns sites, CIDRs, addresses, services, devices, owners, and actors on a
  best-effort basis. `hujsonstore` implements it over its raw readers.
  `sqlitestore` implements it with read-only queries that tolerate invalid
  rows.
- Verbose diagnostics from `sqlitestore` carry counts and timings only,
  never paths.
- History output sanitizes through the same field kinds as the list
  commands, plus `kindOwner` for actors.

`add-serve-tsnet` can expose `IdentitySource` over the API for remote mode.

## Risks / Trade-offs

- [Binary size] The transpiled SQLite adds roughly 5–8 MB to every release
  archive. → Accepted. It is a single static binary and avoids cgo across
  three operating systems.
- [Network filesystems] SQLite locking is unreliable on NFS, SMB, and EFS. →
  Documented as unsupported in the how-to. Detection is not attempted.
- [Out-of-band writes] Other programs can bypass the Ledger. → Triggers and
  `CHECK` constraints block integrity violations, and defensive reads fail
  closed per map.
- [Lock semantics differ] The file lock covers one tailnet's file; the
  SQLite write lock covers the whole database, so all tailnets serialize. →
  Accepted at this write rate. It is also what makes the `tailnets` insert
  safe.
- [Migration on read] A `list` against an old database takes the write
  lock. → It happens once per schema version and is bounded by the same
  5-second wait.
- [Contract growth] `Action`, `Now`, actor context, `HistoryReader`, and
  `IdentitySource` extend the refactor's interface. → All are optional or
  ignored by `hujsonstore`, so the file backend's bytes are unaffected.

## Migration Plan

This is additive and opt-in. Operators adopt it with:

```bash
tsipam registry export --registry-dir . --output snapshot.hujson
tsipam registry import snapshot.hujson --registry sqlite://./tsipam.db
```

Import records one `import` event per entry. Rollback means exporting back
to HuJSON and unsetting `TSIPAM_REGISTRY`. No existing file changes.

## Open Questions

- How does a rehearsal read a WAL database without creating `-wal` and `-shm`?
  Opening it with `immutable=1` works in a read-only directory, but ignores
  committed pages that are still in the WAL. Copying the database and its WAL to
  a private temporary directory sees them, but races a checkpoint. If neither is
  acceptable, this change modifies "Dry run rehearses without writing" in
  `mutation-safety`. Decide before task 2.3, and record it here.
- Should history accept `--action` or `--actor` filters? This is additive
  and can wait for `add-serve-tsnet`, when actors exist.
