# Run a SQLite registry

Use a SQLite registry when several people or CI jobs on one machine reserve
address space at the same time, or when you need a record of who changed what
without depending on git history. Keep the default HuJSON files when you review
the registry in pull requests.

## Select the database

Set the backend once for a shell or a job:

```bash
export TSIPAM_REGISTRY=sqlite:///var/lib/tsipam/tsipam.db
tsipam subnet reserve 10.20.0.0/16 hq-vpc --ticket OPS-1 --tailnet production
tsipam subnet list --tailnet production
```

The first reserve creates the database, but not its directory. One database
holds every `--tailnet` routing key.

A SQLite registry starts empty. No command moves an existing HuJSON registry
into it yet, so reserve again anything you need to carry over.

An explicit flag overrides the variable, so a file registry stays reachable:

```bash
tsipam subnet list --registry-dir ./registries --tailnet production
```

## Keep it on a local disk

Put the database on local block storage. Do not put it on NFS, SMB, EFS, or
another network filesystem: their locking is unreliable, and two writers can
corrupt the file. `tsipam` does not detect this.

Run `tsipam` on the machine that holds the disk. Commands on that machine take
turns writing, and one that waits more than 5 seconds exits 2 without writing.

## Back it up

The database keeps recent writes in a `-wal` file beside it, so copying only the
database file while a command runs can lose them. Use one of these:

```bash
sqlite3 /var/lib/tsipam/tsipam.db ".backup '/backup/tsipam.db'"
sqlite3 /var/lib/tsipam/tsipam.db "VACUUM INTO '/backup/tsipam.db'"
```

Both are safe while `tsipam` runs. `litestream` (<https://litestream.io>) also
works, because the database uses write-ahead logging.

Keep backups as private as the database: they hold the same identities, and
their history.

## See what changed

```bash
tsipam registry history --tailnet production
tsipam registry history --key 10.20.0.0/16 --tailnet production
tsipam registry history --since 2026-01-01T00:00:00Z --limit 500 --format json \
  --tailnet production
```

`--key` takes the key as `list` prints it. Add `--sanitize identity` before the
output leaves the team.

## When a command refuses the database

- `no registry for tailnet "…"`: the database is missing, or holds no registry
  for that `--tailnet`. A reserve creates it; check the path and the key first.
- `a newer tsipam is required`: a newer version wrote the database. Upgrade.
- `tsipam did not create`: the path names another program's database.
- `timed out after 5s`: another command held the write lock. Try again.
- `holds an interrupted write`: a dry run found what a crash left behind. Run a
  command that is not a dry run, such as `tsipam subnet list`, to recover it.
- `kept changing`: a dry run found no pause between writers. Try again.
- `cannot determine whether … is reserved`: another program stored a value that
  `tsipam` refuses. The message names it; correct it with `sqlite3`, or restore a
  backup.
