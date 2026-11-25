# registry-backends Specification

## Purpose

Defines how an operator selects where registry state is stored, the guarantees
every storage backend shares, and the storage, concurrency, integrity, and
file-safety requirements of the embedded SQLite backend.

## Requirements

### Requirement: Select the registry backend by URL

Every registry-backed command SHALL accept `--registry <url>`. Supported
schemes are `file://` and `sqlite://`. Everything after `scheme://` SHALL be
taken verbatim as a filesystem path, relative or absolute: a directory of
`<tailnet>.ipam.hujson` files for `file://`, or one database file for
`sqlite://`. With no selector the backend SHALL be `file://.`.

#### Scenario: Default backend is unchanged

- **WHEN** `tsipam site-id reserve 7 hq-vpc` runs with no `--registry`,
  no `--registry-dir`, and no `TSIPAM_REGISTRY`
- **THEN** the reservation is written to `./local.ipam.hujson`, byte for byte
  as before this change

#### Scenario: SQLite backend selected by flag

- **WHEN** `tsipam site-id reserve 7 hq-vpc --registry sqlite://./tsipam.db`
  runs
- **THEN** the reservation is stored in `./tsipam.db`, no `.ipam.hujson` file
  is created, and a following
  `tsipam site-id list --registry sqlite://./tsipam.db` lists it

### Requirement: Resolve backend selectors in a fixed precedence

An explicit `--registry` or `--registry-dir` flag SHALL take precedence over
the `TSIPAM_REGISTRY` environment variable, which SHALL take precedence over
the default. `--registry-dir <dir>` SHALL behave exactly as
`--registry file://<dir>`. Supplying both `--registry` and `--registry-dir`
on one command SHALL be a usage error.

#### Scenario: Environment selects the backend

- **WHEN** `TSIPAM_REGISTRY=sqlite:///var/lib/tsipam/tsipam.db` is set and
  `tsipam subnet list` runs with no registry flag
- **THEN** pools are listed from that database

#### Scenario: Explicit flag overrides the environment

- **WHEN** `TSIPAM_REGISTRY=sqlite://./tsipam.db` is set and
  `tsipam subnet list --registry-dir ./registries` runs
- **THEN** pools are listed from `./registries/local.ipam.hujson`

#### Scenario: Conflicting flags are refused

- **WHEN** `tsipam subnet list --registry sqlite://./tsipam.db
  --registry-dir .` runs
- **THEN** the command exits 2, writes nothing to stdout, and reads no
  registry

### Requirement: Reject an invalid registry URL before touching storage

A registry URL with an unknown scheme, no `://` separator, or an empty path
SHALL be rejected as invalid input with exit 2 and nothing on stdout, before
any file or database is opened or created.

#### Scenario: Unknown scheme

- **WHEN** `tsipam site-id reserve 7 hq-vpc --registry postgres://db.example.com/ipam`
  runs
- **THEN** the command exits 2, stderr names the supported schemes, and no
  file is created

#### Scenario: Empty path

- **WHEN** `tsipam site-id list --registry sqlite://` runs
- **THEN** the command exits 2 with nothing on stdout

### Requirement: Backends share command behavior

For the same sequence of commands and the same `--tailnet` key, every backend
SHALL produce the same results, `changed` values, collision refusals, exit
codes, and JSON envelope fields. Behavior defined only for the HuJSON file
(comments, raw key spellings, unknown top-level members, malformed-map
repair) SHALL NOT apply to other backends.

#### Scenario: Collision is refused identically

- **GIVEN** site ID `7` reserved for `hq-vpc` in a SQLite registry
- **WHEN** `tsipam site-id reserve 7 branch-office --registry sqlite://./tsipam.db`
  runs
- **THEN** the command exits 1 with the same refusal as the file backend and
  the stored reservation is unchanged

#### Scenario: Idempotent repeat on SQLite

- **GIVEN** site ID `7` reserved for `hq-vpc` in a SQLite registry
- **WHEN** the same reserve runs again with no metadata
- **THEN** it exits 0 with `changed=false` and JSON `status` `no_changes`

### Requirement: SQLite registry creation and absence

Reserve and allocate SHALL create the SQLite database file and the selected
tailnet's registry when absent. Every other registry-backed command SHALL
treat a missing database file or a tailnet with no registry in it as an
unreadable registry: exit 2, nothing on stdout, and nothing created.

#### Scenario: Reserve creates the database

- **WHEN** `tsipam site-id reserve 7 hq-vpc --registry sqlite://./new.db`
  runs and `./new.db` does not exist
- **THEN** the command exits 0 and `./new.db` exists

#### Scenario: List does not create the database

- **WHEN** `tsipam site-id list --registry sqlite://./absent.db` runs and
  `./absent.db` does not exist
- **THEN** the command exits 2, writes nothing to stdout, and `./absent.db`
  still does not exist

#### Scenario: Tailnets are isolated within one database

- **GIVEN** a database holding a registry for tailnet `production` only
- **WHEN** `tsipam site-id list --tailnet staging --registry sqlite://./tsipam.db`
  runs
- **THEN** the command exits 2 and reports no registry for that tailnet

### Requirement: SQLite writes serialize across processes

Concurrent mutating commands against one SQLite database SHALL serialize
their read-decide-write spans, so no reservation is lost and no allocation is
handed out twice. A command that cannot obtain the write lock within 5
seconds SHALL fail with exit 2 and write nothing.

#### Scenario: Concurrent allocations receive distinct blocks

- **WHEN** ten `tsipam subnet allocate 10.40.0.0/16 --size 24 site-N
  --registry sqlite://./tsipam.db` commands run concurrently
- **THEN** all ten exit 0 and the database holds ten distinct,
  non-overlapping `/24` pools

### Requirement: SQLite enforces registry integrity at the storage layer

The SQLite database SHALL itself refuse a site ID outside 0-65535, a UDP port
outside 1024-65535, a duplicate key within a map, and a subnet pool that
overlaps a same-family pool held by a different site, so that writes made
outside tsipam cannot store a state tsipam would refuse.

#### Scenario: An out-of-band overlapping insert is rejected

- **GIVEN** `10.20.0.0/16` reserved for `hq-vpc` in a SQLite registry
- **WHEN** another program inserts `10.20.5.0/24` for `branch-office` into
  the same tailnet's subnet pools directly
- **THEN** the database rejects the insert and `tsipam subnet list` still
  shows only `10.20.0.0/16`

### Requirement: SQLite schema versions are checked on open

tsipam SHALL upgrade a database written by an older schema version in place
before the command proceeds, inside one transaction. A database written by a
newer schema version SHALL be refused with exit 2 and an error asking for a
newer tsipam, and one another program created SHALL be refused with exit 2,
whatever version it records. A refused database and its companions SHALL be
left unchanged.

#### Scenario: Newer schema is refused

- **GIVEN** a database whose recorded schema version is higher than this
  tsipam supports
- **WHEN** any registry command runs against it
- **THEN** the command exits 2, stderr says a newer tsipam is required, and
  the database file is unchanged

#### Scenario: Another program's database is refused

- **GIVEN** another program's database with a schema version tsipam also uses
- **WHEN** a reserve runs against it
- **THEN** the command exits 2, and the database file, its journal mode, and its
  write-ahead log are unchanged

### Requirement: A SQLite rehearsal sees committed state and touches no file

A reserve, allocate, or release with `--dry-run` on a SQLite registry SHALL
decide from every change committed before it reads, including changes SQLite has
not yet copied out of the write-ahead log, and SHALL NOT see uncommitted ones. It
SHALL NOT wait for the write lock, record an event, upgrade the schema, or
create, change, or remove the database or its `-wal` and `-shm` companions. When
the database needs recovery after an interrupted write, it SHALL exit 2 and
change nothing.

#### Scenario: Rehearsing against an open database

- **GIVEN** another process holds `./tsipam.db` open and committed ID 7 for
  `hq-vpc`, and the change is still in the write-ahead log
- **WHEN** `tsipam site-id reserve 7 branch --dry-run --registry sqlite://./tsipam.db`
  runs
- **THEN** it exits 1 with `error_code` `registry_collision` and the directory is
  unchanged

#### Scenario: Rehearsing an older schema

- **GIVEN** a database whose schema is older than this `tsipam` knows
- **WHEN** a reserve runs with `--dry-run`
- **THEN** it reports the outcome a real run would have, and the database is not
  upgraded

#### Scenario: Rehearsing in a read-only directory

- **GIVEN** a readable database in a directory the caller cannot write
- **WHEN** a reserve runs with `--dry-run`
- **THEN** it reports the outcome a real run would have

### Requirement: SQLite database files are private to their owner

On POSIX systems, a database file and its write-ahead-log companions created
by tsipam SHALL be readable and writable by the owner only (mode `0600`).
tsipam SHALL NOT loosen the permissions of an existing database.

#### Scenario: New database permissions

- **WHEN** a reserve creates `./tsipam.db` on Linux
- **THEN** `./tsipam.db` has mode `0600`

### Requirement: Database location stays out of diagnostics and sanitized output

Verbose diagnostics SHALL NOT include the database path or registry URL. When
sanitization is active, the database path, the registry URL, and every
identity value stored in a SQLite registry SHALL be redacted from output and
error text exactly as the equivalent file-backed values are.

#### Scenario: Error text hides the database path

- **GIVEN** `TAILSCALE_SANITIZE=identity`
- **WHEN** `tsipam site-id list --registry sqlite:///srv/acme-ipam/tsipam.db`
  fails because the database is missing
- **THEN** stderr does not contain `/srv/acme-ipam/tsipam.db` or `acme-ipam`

#### Scenario: Verbose output omits the URL

- **WHEN** `tsipam site-id reserve 7 hq-vpc --verbose --registry sqlite://./tsipam.db`
  runs
- **THEN** no stderr diagnostic line contains `tsipam.db`
