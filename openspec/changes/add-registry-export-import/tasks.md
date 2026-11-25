# Tasks: Registry export and import

## 1. Rules: export and import planning in `internal/registry`

- [ ] 1.1 Add `RegistryContents` (four sorted reservation slices including
      UDP ports), `ImportAction`, `ImportReport` (mode, planned, changed,
      per-kind counts, entries, refusal), and `ImportRefusedError` with reasons
      `non_empty_target` and `conflict`; verify `go build ./internal/registry/`
      passes
- [ ] 1.2 Add `Ledger.Export(ctx, tailnet)`, a `View` with all four maps
      required, returning entries in list order; verify `storetest` cases for
      empty, populated, and missing registry (`ErrNotFound`) pass against every
      store
- [ ] 1.3 Implement the pure `planImport(snapshot, source, merge)` with
      `add`/`unchanged`/`update`/`conflict` classification through the existing
      collision and `validateSubnetPoolOverlap` checks against the target state
      plus earlier accepted entries; verify table tests cover each action per
      kind, cross-site overlap between a source pool and a target pool, and
      the non-empty target refusal without `--merge`
- [ ] 1.4 Implement `Ledger.PlanImport` (a `View`, where a missing registry
      counts as empty) and `Ledger.Import` (an `Update` with `CreateIfMissing`
      and `Action` `import` that refuses before any `Put` and applies metadata through
      `normalizeMetadata` and `mergeMetadataInto` without auto-stamping);
      verify `storetest` cases for: one conflict among eleven entries writes
      nothing, merge never deletes, the plan writes nothing, a missing
      `reservedAt` stays missing, and, for stores with history, one `import`
      event per changed entry and none for a plan or refusal, run against every
      store with `-race`

## 2. HuJSON format edge in `internal/registry/hujsonstore`

- [ ] 2.1 Confirm that `hujsonedit.PatchAdd` appends object members in
      insertion order, then implement `RenderDocument(contents)` by applying
      ordered adds to `{}\n` (or by building the value tree directly if the
      order check fails); verify golden tests under `testdata/` for an empty
      registry (`{}`), bare and metadata entries, IPv4 and IPv6 pools, and
      nested UDP ports, plus a test that `Load` accepts every rendered golden
- [ ] 2.2 Implement `ParseSourceDocument(bytes)` with all four maps
      required, returning `RegistryContents` and `Dropped{Comments, RawKeys,
      UnknownKeys}`; verify tests for comments anywhere, `"007"` to `"7"`, a
      `/32` service-address alias, a padded CIDR, an unknown top-level key,
      and each loader rejection (BOM, near-miss key, duplicate, invalid map)
- [ ] 2.3 Add `RawEntriesFromBytes` (shared with `RawEntries`, and covering
      UDP ports) for sanitizer pre-registration; verify `RawEntries` tests still
      pass unchanged and a new test covers the bytes form
- [ ] 2.4 Add a round-trip property test: for each fixture registry,
      `RenderDocument(Export(file))` → `Import` into an empty memory store →
      render again gives identical bytes, and the same through a SQLite store;
      verify it passes

## 3. Service contract

- [ ] 3.1 Add `RegistryContents`, `ImportReport`, `export_registry`, and
      `import_registry` (with `invalid_input` 400 and `import_refused` 409) to
      `design/`, run `mise run gen`, and verify `go build ./...` fails only
      for the two missing `ipamsvc` methods and the `gen/` diff touches only
      the new methods and types
- [ ] 3.2 Implement `ExportRegistry` and `ImportRegistry` in `internal/ipamsvc`
      over the ledger, mapping `ImportRefusedError` to `import_refused` and
      normalization failures to `invalid_input`; verify `ipamsvc` tests for
      both methods, both modes, and both errors pass, and `mise run gen-check`
      is clean

## 4. CLI: `registry export` and `registry import`

- [ ] 4.1 Add `export` to the `registry` command group (`--output`, the
      registry flags) rendering text, JSON envelope (`document`, `counts`), or
      a `0600` atomic temp-file-and-rename write. Call the service through
      `target.go`, and give each command file that imports `hujsonstore` its
      own entry in `commandExceptions`, listing only that package. Verify the
      boundary tests pass, and tests in `cmd/tsipam` for each output path, an
      envelope golden, file mode and the absence of a temp file on failure,
      and exit 2 for a missing registry and for a malformed map
- [ ] 4.2 Add `import <file|->` with `--merge`, `--plan`, and
      `--drop-unknown`: read the source, reject invalid UTF-8, pre-register
      sweep values, parse, apply the unknown-keys refusal, call the service
      through `target.go`, and render the `import` envelope with `dropped` and
      `refusal`, with the same `commandExceptions` rule as 4.1; verify the
      boundary tests pass, and tests for stdin, each refusal reason in apply
      mode (exit 1, empty stdout) and plan mode (exit 1, report on stdout),
      success (exit 0, `status: ok`), and an all-unchanged merge (`status:
      no_changes`)
- [ ] 4.3 Add a CLI test that exports a HuJSON registry, imports it with
      `--registry sqlite://`, exports that, and compares the two documents
      byte for byte; verify it passes
- [ ] 4.4 Give `import_refused` its error code in `classify`, with the verdict
      codes that exit 1, and return it after a refused plan's report; verify the
      table test in `errors_test.go` gains both rows and CLI tests assert the
      code, the exit status, and empty stdout on an applied refusal
- [ ] 4.5 Wire sanitization: placeholders applied after ordering in the
      rendered export, the import report's field kinds, and path sweep values;
      verify `error_sanitize_test.go` cases for a sanitized export, a sanitized
      import report, and a loader error that echoes a source value
- [ ] 4.6 Update `schema.go`'s exit-code 1 text and sanitization text, then
      regenerate the three schema goldens in `testdata/` with `-update`; verify
      the golden diffs add only the `registry` commands and the two text
      changes, and that the schema delta's scenarios hold
- [ ] 4.7 Declare both commands in `leafContracts` (`read-only` and `mutate`),
      take the import source through `bindInputs` as `--source`, make
      `--dry-run` on import the same run as `--plan`, and add both to the pinned
      MCP tool set; verify `import` has a dry-run case and an input case, and
      the leaf-contract, dry-run, input, and tool-set tests pass

## 5. Documentation and smoke

- [ ] 5.1 Document both commands in `docs/reference/json-envelope.md` (the
      `document`/`counts` and `import` payloads), `exit-codes.md`
      (`import_refused` at 1, source failures at 2), and `registry-format.md` (export
      is canonical, not a byte copy); verify `mise run lint-md` passes
- [ ] 5.2 Add `docs/how-to/export-and-import-a-registry.md` (snapshot for CI,
      copy between tailnets, merge, plan, `--format human` in a pipe or
      redirect, and the sanitized-backup warning), link it from
      `docs/README.md`, and replace the no-migration note in
      `docs/how-to/run-a-sqlite-registry.md` with the HuJSON-to-SQLite steps;
      verify `mise run lint-md` passes and every
      command in it runs as written against a temp registry
- [ ] 5.3 Add README examples for export and a merge import with their
      `smoke/` cases in the same commit; verify `mise run smoke` passes

## 6. Integration verification

- [ ] 6.1 Run `mise run test lint cross smoke` and verify all pass
- [ ] 6.2 Verify that no existing golden fixture other than the three schema
      goldens changed (`git diff --stat main -- '*testdata*'`)

## Workflow follow-up

- Commit with `feat:` (new commands; release-please cuts a minor version).
- Archive the change after the PR merges, syncing `registry-transfer` and the
  `schema` delta into `openspec/specs/`.
