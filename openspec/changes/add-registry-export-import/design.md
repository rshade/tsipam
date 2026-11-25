# Design: Registry export and import

## Context

See `proposal.md` for motivation and `specs/registry-transfer/spec.md` for
the behavior. This change assumes `refactor-registry-store` and
`add-sqlite-store` have landed:

- `registry.Store` runs `View` and `Update` transactions. `Update` is
  serialized per tailnet, atomic when `fn` fails, and gives read-your-writes.
- `registry.Ledger` owns every rule: normalizers, collision and cross-site
  overlap checks, metadata merge, and list ordering.
- `hujsonstore` owns the file format: the loader's validation rules, raw
  spellings, `RawEntries` for the sanitizer, and comment-preserving writes
  through `hujsonedit`.
- `add-sqlite-store` provides `sqlitestore`, the `--registry` resolver and
  `internal/registry/backend`, the `registry` command group, and
  `TxOptions.Action`/`Now`, from which `sqlitestore` records audit events.
- Every registry command calls the goa service through `internal/loopback`.
  `check-policy` shows the pattern for local input: the CLI reads the file,
  rejects invalid UTF-8, and sends the contents in the payload.
- The CLI pre-registers sanitizer sweep values from the selected registry's
  `registry.IdentitySource`.

## Goals / Non-Goals

**Goals:**

- The service API is format-agnostic: it exchanges structured
  reservations, so a future UI, Pulumi provider, or `--server` client can
  import and export without speaking HuJSON.
- One rule implementation decides import actions, for both `--plan` and a
  real import.
- Exported bytes depend only on the set of reservations, never on which
  store holds them or how the source file was spelled.

**Non-Goals:**

- `--replace` or any import mode that deletes reservations.
- Transferring through a remote server (`--server`, owned by
  `add-serve-tsnet`).
- Transferring several tailnets in one document. A document is one tailnet,
  matching the file format.
- Detecting sanitizer placeholders in an import source.

## Decisions

### HuJSON stays at the CLI edge; the service speaks reservations

Two new goa methods:

- `export_registry(tailnet)` returns `RegistryContents`: arrays of the
  existing `SiteIDReservation`, `SubnetPoolReservation`,
  `ServiceAddressReservation`, and `UDPPortReservation` types, in list
  order. `GET /tailnet/{tailnet}/ipam/export`.
- `import_registry(tailnet, contents, merge, plan)` returns `ImportReport`.
  `POST /tailnet/{tailnet}/ipam/import`. Errors: `invalid_input` (400) and a
  new `import_refused` (409).

The CLI renders the export document from `RegistryContents` and parses the
import source into `RegistryContents` with `hujsonstore`.

*Alternative considered:* send the HuJSON text in the payload, as
`check-policy` does. Rejected because it would make every future backend
and client depend on the file format, and the service would have to
re-implement reporting of dropped content that only matters to a HuJSON
source.

### Rendering reuses the file store's writer

`hujsonstore.RenderDocument(contents)` starts from `{}\n` and applies one
`hujsonedit.PatchAdd` per entry, in list order, through `PatchBytes`.
Entries without metadata use the bare string form, and empty maps are never
created. The output is therefore exactly what the file store would write if
the same reservations were made one by one into a fresh file: same tab
indentation and trailing commas. It also loads with the existing loader by
construction.

A task verifies that `hujson` appends added object members in insertion
order. If it does not, rendering builds the `hujson.Value` tree directly and
formats it once.

### Parsing the source reports what it drops

`hujsonstore.ParseSourceDocument(bytes)` applies the full loader with all
four maps required. It returns `RegistryContents` plus a `Dropped` value:

- `Comments`: whether any comment appears in the parsed tree's whitespace
  segments.
- `RawKeys`: each `{kind, raw, canonical}` where the on-file key differs
  from its canonical form, read from the loader's raw-key maps.
- `UnknownKeys`: top-level member names other than the four known keys.

The CLI decides the `unknown_keys` refusal before calling the service, so
no source with unknown keys ever reaches a store without `--drop-unknown`.
In `--plan` mode it still calls the service to compute the entry actions,
then sets `refusal` to `unknown_keys`.

### One planning function, run inside the transaction

`registry.planImport(current Snapshot, source RegistryContents, merge bool)
(ImportReport, error)` is pure. It walks source entries in kind order (site
IDs, subnet pools, service addresses, UDP ports), then list order. It
classifies each entry with the same checks `reserve` uses (collision, and
`validateSubnetPoolOverlap` for pools) against the current state plus the
entries already accepted earlier in the walk. That catches a source pool
overlapping a target pool held by another site, even though the source
itself validated.

- `Ledger.PlanImport` runs it in a `View`. A missing registry is treated as
  empty rather than as `ErrNotFound`.
- `Ledger.Import` runs it in an `Update` with `CreateIfMissing`, all four
  maps required, `Action` `import`, and `Now` from the ledger's clock. If the
  report has any refusal, the ledger returns
  `*registry.ImportRefusedError` before any `Put`. Otherwise it applies
  `add` and `update` entries with `Put*`. A commit failure is wrapped as
  `importing registry: ...`.

`sqlitestore` records one event per `Put*`, so a changed entry gets exactly
one `import` event, and an `unchanged` entry, a plan, or a refusal gets none.

Because the real import re-plans inside its own transaction, a plan is
advisory: a reservation made between `--plan` and the import can still
change the outcome. This is the same caveat `plan`/`check` already document
for live state.

### Metadata is copied, not stamped

Import calls `normalizeMetadata` on each source entry (so `reservedAt` is
validated and normalized to UTC), but not `resolveReservationEntry`, so no
`reservedAt` is auto-stamped. For an `update` in merge mode the existing
`mergeMetadataInto` applies: supplied fields overwrite, absent ones keep
their stored value, matching the metadata spec.

### Exit-code mapping

- An applied import that is refused arrives as goa error `import_refused`.
  `classify` in `cmd/tsipam/errors.go` gives it the new code `import_refused`,
  which joins the verdict codes that exit 1. Stdout stays empty, and the error
  object carries the refusal reason in its message and `refusal` and
  `conflict_count` in its `context`.
- `--plan` gets a normal result. When `refusal` is set, the CLI emits the
  report, then returns a typed error with the same code, as `check-policy`
  does for drift. A refused plan therefore leaves a result on stdout.
- Source read, UTF-8, and validation failures, and a missing or invalid
  target registry on export, exit 2.

### Export output handling

- Human mode without `--output` writes the rendered document as the result.
- JSON mode wraps it in the envelope with `document` and `counts`.
- A pipe or a redirect selects JSON, so a shell that wants the bare document
  passes `--format human`, or `--output` for a file. The examples and the
  `export | import -` scenario say so.
- `--output` writes through a temporary file in the destination directory
  with mode `0600`, then renames it; stdout gets the summary envelope or
  message.

The `--output` path and the import source path are registered as `kindPath`
sweep values, like `check-policy`'s policy path.

### Sanitization

Export renders the document from sanitized `RegistryContents`. Identity
fields are replaced with placeholders after ordering is fixed, so the order
of entries does not reveal the original values. Import pre-registers the
source document's values through `hujsonstore.RawEntriesFromBytes` (a bytes
variant of `RawEntries`) before validation, so loader errors that echo
source values are swept. The import report reuses the list commands' field
kinds.

A sanitized export is intended for sharing, not for restoring. The
existing stderr notice that sanitization is active makes that visible.

### Meeting the runtime contracts

`adopt-ax-go-runtime` landed first, so this change meets its contracts:

- **Side-effect class:** `registry export` is `read-only` and `registry import`
  is `mutate` in `leafContracts`. Import adds and updates, and never removes.
- **Dry run:** `--dry-run` on `import` is the same run as `--plan`, with
  `meta.dry_run` set. A pure plan needs no rehearsing transaction.
- **Inputs:** `import` takes its source by position or by `--source`, through
  `bindInputs`. `-` means stdin either way. In an MCP call there is no stdin, so
  `-` reads nothing and fails as an empty document.
- **MCP:** both commands join the pinned tool set. `import` carries the
  destructive hint.
- **Approval:** none. Import is additive.

## Risks / Trade-offs

- [Risk] An operator with `TAILSCALE_SANITIZE=identity` exported globally
  takes a backup that cannot be restored. → Mitigation: the existing stderr
  notice, plus a warning in the how-to and in `registry export --help`.
- [Risk] Large documents make one large goa payload. → Accepted. A
  registry is bounded by its namespaces and realistically holds hundreds of
  entries. No new size limit is added.
- [Risk] A plan and the import that follows can diverge. → Mitigation:
  documented. The real import re-plans inside its own serialized
  transaction, so it never applies stale decisions.
- [Trade-off] Refusing unknown top-level keys adds a flag most users never
  need. It prevents silently losing data written by a newer `tsipam`
  version, which the format explicitly allows.

## Migration Plan

This change is additive: two new commands and two new service methods. It also
gives existing HuJSON registries their way into SQLite, and back:

```bash
tsipam registry export --registry-dir . --output snapshot.hujson
tsipam registry import snapshot.hujson --registry sqlite://./tsipam.db
```

No existing output changes, except that the schema catalog and exit-code
description gain entries. Rollback is a revert.

## Open Questions

- Should `registry import` later accept an export envelope (`--format
  json` output) directly as a source? This is deferrable, because it would
  be an additive input form.
