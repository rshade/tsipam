# Tasks: Registry store abstraction

## 1. Store contract in `internal/registry`

- [x] 1.1 Add `store.go` with `MapKind`, `TxOptions`, `Store`, `Snapshot`,
      `Tx`, `Counts`, `ErrNotFound`, `CommitError`, and the `Allocations`
      struct exactly as in design.md, with doc comments stating the eight
      contract rules; verify `go build ./internal/registry/` and
      `go vet ./internal/registry/` pass with no other file changed
- [x] 1.2 Add `registry.StoredStateError` (exported form of
      `storedStateError`) for backends that canonicalize stored keys, and
      verify the existing `storedStateError` callers still compile and
      `go test ./internal/registry/` passes unchanged

## 2. In-memory reference store and contract conformance

- [x] 2.1 Create `internal/registry/storetest` with `NewMemStore()`, a
      map-backed `registry.Store` under one mutex with buffered transactions
      (copy on begin, swap on success), and verify it satisfies the interface
      through a compile-time assertion
- [x] 2.2 Add `storetest.Run(t, newStore)` with contract cases: not-found
      without `CreateIfMissing`, create-if-missing, rollback when `fn`
      errors after a `Put`, read-your-writes inside `Update`, absent delete
      returns `(false, nil)`, `CommitError` passthrough, and 10 concurrent
      `Update` calls incrementing one entry each with no lost write; verify
      `go test -race ./internal/registry/storetest/` passes against
      `NewMemStore`

## 3. HuJSON backend `internal/registry/hujsonstore`

- [x] 3.1 Move the file mechanics into `hujsonstore`: `Load`/`loadBytes`, the
      private document type (raw keys, presence flags, scoped errors),
      `decodeMapMember`, `registryMemberIndex`, `duplicateEntryKey`, the
      `canonicalize*` functions, the file JSON shapes of `Entry` and
      `ServiceEntry` (`entryJSON`, `serviceEntryJSON` with their
      Marshal/Unmarshal), `withRegistryLock`, `loadOrCreate`,
      `identityFreePathError`, and the UDP-port loaders; verify
      `go build ./internal/registry/hujsonstore/` passes
- [x] 3.2 Implement `hujsonstore.New(dir string, logger diagnostics.Logger)`,
      `FileName(tailnet)`, and `Path(dir, tailnet)`, plus `View` and
      `Update` with `Require` checked before `fn` in today's
      `loadOrCreate`/`mapError` order and the same `registry loaded` /
      `registry created` / lock events; verify with a test that reads the
      emitted debug events for a reserve and a release and compares them with
      a capture taken from `main` before the change
- [x] 3.3 Implement the buffered `Tx`: each `Put*` builds its op with the
      moved `reservePatchOp`/`reserveUDPPortPatchOp`, applies
      `hujsonedit.PatchBytes` to the pending bytes, and re-decodes; each
      `Delete*` targets the recorded raw spelling and folds in
      `repairMalformedMap`/`repairMalformedUDPPort`; commit writes once via
      `hujsonedit.WriteRaw` only when the bytes changed and returns
      `*registry.CommitError` on failure; verify `storetest.Run` passes
      against `hujsonstore` in a temp dir
- [x] 3.4 Move the not-found message logic from `ipamsvc.registryError` into
      `hujsonstore` so errors match both `registry.ErrNotFound` and
      `os.ErrNotExist` with identical text; verify with tests for a missing
      directory and a missing file that assert the exact strings
- [x] 3.5 Move `RawEntries` and `RawUDPPorts` into `hujsonstore`, and move
      `serviceaddress_load_test.go`, `udpport_load_test.go`, and
      `udpport_hujson_test.go` into the package; verify they pass with only
      import and package-name edits

## 4. Ledger and caller rewiring

- [x] 4.1 Add `registry.Ledger` with `NewLedger(store, logger)`, an
      injectable `now`, and one method per current `...WithLogger` function
      plus `LoadForPlanning`, each carrying the rule logic and debug events
      unchanged and wrapping `CommitError` with today's operation prefix;
      verify `go build ./internal/registry/...` passes
- [x] 4.2 Rewrite the file-level registry tests (`registry_test.go`,
      `subnetpool_test.go`, `subnetallocate_test.go`, `serviceaddress_test.go`,
      `udpport_test.go`, `planning_test.go`) mechanically into
      `hujsonstore` external tests that call
      `registry.NewLedger(hujsonstore.New(dir, nil), nil)`; verify the
      top-level test and subtest count from `go test -json` matches the
      pre-change count and every test passes
- [x] 4.3 Change `planner.Compare` to take `registry.Allocations` and update
      `planner_test.go`; verify `go test ./internal/planner/` passes with
      `allocations.report.golden.json` unchanged
- [x] 4.4 Replace `ipamsvc.Service.RegistryDir` with a `*registry.Ledger`,
      drop `registryPath`/`registryError`, and have `loopback.RunWithLogger`
      and `cmd/tsipam/planning.go` build
      `registry.NewLedger(hujsonstore.New(dir, logger), logger)`; verify
      `go test ./internal/ipamsvc/ ./internal/loopback/` passes
- [x] 4.5 Switch `cmd/tsipam/main.go`'s sanitizer setup to
      `hujsonstore.FileName`, `hujsonstore.Path`, `hujsonstore.RawEntries`,
      and `hujsonstore.RawUDPPorts`; verify `go test ./cmd/...` passes with
      no edits to any `cmd/tsipam/*_test.go` or `cmd/tsipam/testdata/` file
- [x] 4.6 Delete the path-based `...WithLogger` functions, the exported
      `Registry` type, `mapAccessor`, and any helper left without callers;
      verify `go build ./...`, `go vet ./...`, and `mise run lint-go`
      report no unused code

## 5. Rule cases into the conformance suite

- [x] 5.1 Move rule-level cases from the `hujsonstore` tests into
      `storetest.Run` as table-driven cases (collision per map, cross-site
      overlap refused, same-site overlap allowed, idempotent repeat, metadata
      merge, `reservedAt` stamping with a fixed clock, ticket replay,
      next-free with live taken and exhaustion, UDP per-site scoping, list
      ordering), leaving byte, comment, raw-spelling, presence, BOM,
      near-miss key, repair, and lockfile cases in `hujsonstore`; verify
      `go test -race ./internal/registry/...` passes against both stores
- [x] 5.2 Record a case accounting table (each removed test name and its new
      home) in the PR description, and verify no case is listed as dropped

## 6. Documentation

- [x] 6.1 Update the `internal/` package list in `AGENTS.md` to describe
      `registry` (rules and the `Store` contract), `registry/hujsonstore`, and
      `registry/storetest`, and update the package doc comments; verify
      `mise run lint-md` passes
- [x] 6.2 Confirm `docs/reference/registry-format.md` and
      `docs/explanation/registry-and-live-state.md` still describe the
      behavior accurately with no edits needed; verify with
      `git diff --stat docs/` showing no changes

## 7. Integration verification

- [x] 7.1 Run `mise run test lint cross` and `mise run smoke`, and verify all
      pass
- [x] 7.2 Verify `git diff --stat main -- gen/ design/ smoke/ cmd/tsipam/testdata/ internal/planner/testdata/ internal/policycheck/testdata/`
      is empty
- [x] 7.3 For each `fmt.Errorf`/`errors.New` format string in
      `internal/registry` on `main`, grep the new tree and verify it exists
      exactly once, or that its replacement is documented in the PR
      description; also verify each `diagnostics.Debug` event name on
      `main` still exists

## Workflow follow-up

- Commit as `refactor(registry): ...` (no release-please entry, since there
  is no user-visible change).
- Archive the change once the PR merges, then apply
  `add-registry-export-import` and `add-sqlite-store`.
