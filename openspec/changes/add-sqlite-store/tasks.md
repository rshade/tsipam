# Tasks: SQLite registry backend

## 1. Contract additions in `internal/registry`

- [ ] 1.1 Add `TxOptions.Action` and `TxOptions.Now`, `WithActor`/`ActorFrom`,
      the `Event` and `HistoryFilter` types, the optional `HistoryReader` and
      `IdentitySource` interfaces, and `ErrHistoryUnsupported`; have every
      `Ledger` mutation set `Action` and `Now`; verify
      `go test ./internal/registry/...` passes with `hujsonstore` output
      byte-identical (existing golden and file tests unchanged)
- [ ] 1.2 Add `Ledger.History` and `Ledger.IdentityValues`, returning
      `ErrHistoryUnsupported` or empty identities when the store lacks the
      interface; verify with unit tests against `hujsonstore`
- [ ] 1.3 Implement `HistoryReader` on `storetest.NewMemStore()` and add
      history cases to `storetest.Run` (one event per changed reservation,
      none for no-op or refusal, rollback discards the event, before/after
      values, actor from context, newest-first ordering, `kind`/`key`/
      `since`/`limit` filters), run only for stores implementing the
      interface; verify `go test -race ./internal/registry/storetest/` passes
- [ ] 1.4 Implement `IdentitySource` on `hujsonstore` over `RawEntries` and
      `RawUDPPorts`, and switch `registerKnownIdentityInventory` to it; verify
      `go test ./cmd/...` passes with no edits to existing sanitizer tests

## 2. `internal/registry/sqlitestore`

- [ ] 2.1 Add `modernc.org/sqlite` to `go.mod`; verify `mise run cross`,
      `mise run license-check`, and `mise run govulncheck` pass, and record
      the release binary size before and after in the PR description
- [ ] 2.2 Add `migrations/0001_init.sql` (schema, overlap triggers on insert
      and update, append-only triggers on `events`, indexes) embedded with
      `embed.FS`, plus the `PRAGMA user_version` migrator; verify tests for a
      fresh database, an upgrade from a version-0 file, and refusal of a
      newer `user_version` with the file bytes unchanged
- [ ] 2.3 Implement `Open`, the writer and reader handles with their DSN
      pragmas, lazy `0600` creation with `O_EXCL`, `ErrNotFound` for a
      missing file or tailnet without creating anything, and mapping
      `SQLITE_BUSY` to the 5-second lock-timeout error; verify with tests
      that check file and `-wal` modes on POSIX and that `View` on a missing
      path leaves no file
- [ ] 2.4 Implement `Snapshot` with defensive reads (re-normalize keys and
      values, recompute pool ranges from `cidr`, scoped per-map errors) and
      the typed `Tx` mutations, writing one `events` row per `Put*`/`Delete*`
      with prior value, `Action`, `Now`, and actor; verify `storetest.Run`,
      including history cases, passes against `sqlitestore` with `-race`
- [ ] 2.5 Add storage-integrity tests that insert directly through
      `database/sql`: a cross-site overlapping pool, an out-of-range site ID
      or port, an event `UPDATE`/`DELETE`, and a corrupted `cidr`/range pair
      (scoped map error on read, `Delete` fails closed); verify each is
      rejected or fails closed
- [ ] 2.6 Implement `IdentitySource` and `HistoryReader` on `sqlitestore`;
      verify unit tests cover identity collection from a database containing
      one invalid row
- [ ] 2.7 Add a concurrency test that starts ten goroutines, each with its own
      `Open` on one file, each running `Ledger.AllocateSubnetPool`; verify
      ten distinct, non-overlapping blocks and ten `allocate` events

## 3. Backend selection

- [ ] 3.1 Add `internal/registry/backend` with `Parse` and `Open`; verify
      table tests for `file://.`, `file://./regs`, `sqlite:///abs/x.db`,
      `sqlite://C:\data\x.db`, unknown scheme, missing `://`, and empty path
- [ ] 3.2 Add `--registry` to `addRegistryFlags` and the resolver
      (explicit flag, then `TSIPAM_REGISTRY`, then `file://.`; both explicit
      flags is a usage error; `--registry-dir` default is not explicit), and
      pass the opened `Store` through `loopback.RunWithLogger`; verify CLI
      tests for each precedence scenario in
      `specs/registry-backends/spec.md` and that every existing `cmd/` test
      and golden passes unchanged
- [ ] 3.3 Register the raw URL, parsed path, and base name with the
      sanitizer, and keep paths out of `sqlitestore` diagnostics; verify the
      two sanitization scenarios in `specs/registry-backends/spec.md` with
      CLI tests
- [ ] 3.4 Add CLI tests running each registry command family (site-id,
      subnet including allocate, service, udp-port, plan, check-policy, calc
      aggregate, calc next-free) against `sqlite://`, comparing JSON envelopes
      with the same sequence on `file://`; verify identical output apart from
      backend-specific fields (none expected)
- [ ] 3.5 Document `--registry`, `TSIPAM_REGISTRY`, the schemes, and
      precedence in `docs/reference/registry-format.md` (renamed sections or
      a new `docs/reference/registry-backends.md` linked from it), and add
      the SQLite missing-database and newer-schema cases to
      `docs/reference/exit-codes.md`; verify `mise run lint-md` passes

## 4. `registry history`

- [ ] 4.1 Add `list_history` to `design/design.go` (path
      `GET /tailnet/{tailnet}/ipam/history`, query `kind`, `key`, `since`,
      `limit`; result `events`; errors `invalid_input` and `unsupported`) and
      regenerate with `mise run gen`; verify `mise run gen-check` and
      `go build ./...` pass
- [ ] 4.2 Implement `ipamsvc.ListHistory`, mapping `ErrHistoryUnsupported`
      to `unsupported`; verify `go test ./internal/ipamsvc/` covers both
      backends
- [ ] 4.3 Add `tsipam registry history` under the `registry` group (create
      the group if `add-registry-export-import` has not landed it) with human
      tab-separated output, the `events` JSON array, flag validation, and
      sanitization; verify CLI tests for every scenario in
      `specs/registry-history/spec.md`
- [ ] 4.4 Document the `events` array in `docs/reference/json-envelope.md`
      and the history command in the CLI reference; verify
      `mise run lint-md` passes

## 5. Schema catalog

- [ ] 5.1 Make the `registry` flag and `registry history` appear in
      `tsipam -schema`, regenerate `cmd/tsipam/testdata/schema.golden.json`
      with `-update`, and inspect that the diff adds only those entries;
      verify both scenarios in `specs/schema/spec.md`

## 6. Operator documentation and smoke

- [ ] 6.1 Write `docs/how-to/run-a-sqlite-registry.md`: selecting the
      backend, migrating with `registry export`/`import`, backups
      (`sqlite3 .backup`, `VACUUM INTO`, or Litestream with WAL), local block
      storage only (no NFS, SMB, or EFS), and a single writer process; link
      it from `docs/README.md`; verify `mise run lint-md` and Vale-clean prose
- [ ] 6.2 Add a non-live smoke case that reserves, lists, releases, and reads
      history against `sqlite://` in a temp dir using the release binary;
      verify `mise run smoke` passes

## 7. Integration verification

- [ ] 7.1 Run `mise run test lint cross smoke`; verify all pass
- [ ] 7.2 Verify that with `TSIPAM_REGISTRY` unset, the full `cmd/` and
      `smoke/` suites pass with no golden or fixture changes other than
      `schema.golden.json`

## Workflow follow-up

- Commit as `feat(registry): add sqlite backend and registry history`.
- Archive after merge, then continue with `add-serve-tsnet`.
