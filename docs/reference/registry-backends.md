# Registry backends

A registry is stored either as HuJSON files in a directory or in a SQLite
database. Commands, results, and exit codes are the same on both.

## Select a backend

| URL | Storage |
| --- | --- |
| `file://<directory>` | One [`<tailnet>.ipam.hujson`](registry-format.md) file per routing key |
| `sqlite://<path>` | One database file that holds every routing key |

Everything after `://` is the path, taken as written: relative or absolute,
with no percent-decoding. `sqlite://./tsipam.db` and `sqlite://C:\data\tsipam.db`
are both valid.

The first of these that is set selects the backend:

1. `--registry <url>` or `--registry-dir <directory>`. Giving both is an error.
2. The `TSIPAM_REGISTRY` environment variable, which holds a URL.
3. `file://.`

`--registry-dir <directory>` is the same as `--registry file://<directory>`.

A URL with another scheme, no `://`, or an empty path exits 2 before any file
is opened or created.

## What differs

| | `file://` | `sqlite://` |
| --- | --- | --- |
| Comments, raw key spellings, unknown top-level keys | Preserved | Not applicable |
| History | In version control | [`registry history`](#history) |
| Write lock | One lock file per routing key | One lock for the whole database |
| Repair of a malformed map on release | Yes | No; the release fails |
| Storage | Any filesystem | Local disk only |

On both, a concurrent write waits up to 5 seconds for the lock, then exits 2
without writing.

On both, `--dry-run` on a reserve, allocate, or release takes no lock, works in a
directory you cannot write, and creates, changes, and removes no file. On SQLite it reads the database and its
`-wal` file into memory and runs there, so it sees every committed change and
leaves no event, upgrade, `-wal`, or `-shm` behind. It exits 2 when a crash left
the database to be recovered, or when writers never pause for 5 seconds; a
command that is not a dry run does not have those limits.

## SQLite storage

Reserve and allocate create the database file and the routing key's registry
in it. Every other command exits 2 when either is missing, and creates nothing.

On Linux and macOS a new database is readable and writable by its owner only
(mode `0600`), as are its `-wal` and `-shm` companions. The permissions of an
existing database are never changed. The companions are removed when the last
command using the database exits.

The database refuses a site ID outside `0`–`65535`, a UDP port outside
`1024`–`65535`, a duplicate key, and a pool that overlaps another site's pool,
whichever program writes it. A stored value that is invalid in some other
way blocks the commands for its map, and no others.

The schema is versioned. An older database is upgraded in place by the first
command that opens it and is not a dry run. A database written by a newer `tsipam`, or by another
program, is refused and left unchanged.

## History

In a SQLite registry every reserve, allocate, and release that changes something
records one event, in the same transaction as the change. A command that changes
nothing, or fails, records none. Events are never edited or deleted, and they
outlive the reservations they describe.

An event holds the time, the action, the kind and key of the reservation, its
value before and after, and an actor. A local command has no actor: `tsipam` does
not trust the operating-system user name or the environment for it.

```bash
tsipam registry history --registry sqlite://./tsipam.db
tsipam registry history --kind subnet_pool --since 2026-01-01T00:00:00Z \
  --registry sqlite://./tsipam.db --format json
```

On a `file://` registry the command exits 2 with `validation_error`.

See [Run a SQLite registry](../how-to/run-a-sqlite-registry.md) for operating
one, and [JSON envelope](json-envelope.md#events) for the event fields.
