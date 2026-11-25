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
      `RawUDPPorts`, and switch `registerKnownIdentityInventory` to it with
      the store that `target.go` opens; verify
      `go test ./cmd/...` passes with no edits to existing sanitizer tests

## 2. `internal/registry/sqlitestore`

- [ ] 2.1 Add `modernc.org/sqlite` to `go.mod` and
      `internal/registry/sqlitestore`, which imports it, with the server role
      in `cmd/tsipam/boundary_test.go`; verify the boundary tests,
      `mise run cross`, `mise run license-check`, and `mise run govulncheck`
      pass, and record the release binary size before and after in the PR
      description
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
- [ ] 2.5 Decide the rehearsal read path (see the open question), record it in
      design.md, and implement rule 9 with it; verify the rehearsal cases in
      `storetest.Run` pass, and tests show an unchanged directory listing and
      bytes for a present, absent, older-schema, and read-only database, and no
      `events` row
- [ ] 2.6 Add storage-integrity tests that insert directly through
      `database/sql`: a cross-site overlapping pool, an out-of-range site ID
      or port, an event `UPDATE`/`DELETE`, and a corrupted `cidr`/range pair
      (scoped map error on read, `Delete` fails closed); verify each is
      rejected or fails closed
- [ ] 2.7 Implement `IdentitySource` and `HistoryReader` on `sqlitestore`;
      verify unit tests cover identity collection from a database containing
      one invalid row
- [ ] 2.8 Add a concurrency test that starts ten goroutines, each with its own
      `Open` on one file, each running `Ledger.AllocateSubnetPool`; verify
      ten distinct, non-overlapping blocks and ten `allocate` events

## 3. Backend selection

- [ ] 3.1 Add `internal/registry/backend` with `Parse` and `Open`, and the
      server role in the boundary test; verify the boundary tests pass and
      table tests for `file://.`, `file://./regs`, `sqlite:///abs/x.db`,
      `sqlite://C:\data\x.db`, unknown scheme, missing `://`, and empty path
- [ ] 3.2 Add `--registry` to `addRegistryFlags` and the resolver (explicit
      flag, then `TSIPAM_REGISTRY`, then `file://.`; both explicit flags is a
      usage error; `--registry-dir` default is not explicit), and hold the
      opened `Store` in `target.go`'s `registryTarget`, with
      `internal/registry/backend` replacing `hujsonstore` in that file's entry
      in the boundary import test; verify CLI tests for each precedence
      scenario in `specs/registry-backends/spec.md` and that every existing
      `cmd/` test and golden passes unchanged
- [ ] 3.3 Register with the sanitizer the raw URL, the parsed path, and, for
      `sqlite://`, the path's base name or, for `file://`, the registry file's
      path and name as today; `internal/registry/backend` returns them and
      `target.go` passes them on. Keep paths out of `sqlitestore` diagnostics;
      verify `main.go` no longer imports `hujsonstore` or any other server
      package, its entry is gone from the boundary import
      test, and the two sanitization scenarios in
      `specs/registry-backends/spec.md` with CLI tests
- [ ] 3.4 Add CLI tests running each registry command family (site-id,
      subnet including allocate, service, udp-port, plan, check-policy, calc
      aggregate, calc next-free) against `sqlite://`, comparing JSON envelopes
      with the same sequence on `file://`, each with and without `--dry-run`;
      verify identical output once the fields `__schema` publishes and the error
      object's `trace_id` are masked
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
      sanitization. Declare it `read-only` in `leafContracts` and add it to the
      pinned MCP tool set. Verify CLI tests for every scenario in
      `specs/registry-history/spec.md`, and the leaf-contract and tool-set tests
- [ ] 4.4 Document the `events` array in `docs/reference/json-envelope.md`
      and the history command in the CLI reference; verify
      `mise run lint-md` passes

## 5. Schema catalog

- [ ] 5.1 Make the `registry` flag and `registry history` appear in
      `tsipam -schema`, regenerate the three schema goldens in
      `cmd/tsipam/testdata/` with `-update`, and inspect that the diffs add only
      those entries;
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
      `smoke/` suites pass with no golden or fixture changes other than the
      three schema goldens

## Workflow follow-up

- Commit as `feat(registry): add sqlite backend and registry history`.
- Archive after merge, then continue with `add-serve-tsnet`.
