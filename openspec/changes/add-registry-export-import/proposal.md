# Proposal: Registry export and import

## Why

A second storage backend (`add-sqlite-store`) and a shared server
(`add-serve-tsnet`) need a lossless way to move reservations between stores
and to produce a reviewable HuJSON snapshot from a store that is not a file.
Teams that run `check-policy` offline in CI also need a canonical registry
document they can commit, wherever the reservations actually live.

**Prerequisite:** `refactor-registry-store`. This change builds on its
`Store` contract (atomic `Update`, read-your-writes) and its `Ledger` rules.

## What Changes

- Add `tsipam registry export`: writes the selected tailnet's reservations as
  canonical, deterministic HuJSON to stdout or `--output <file>`. The output
  loads with the existing registry loader and round-trips byte for byte.
- Add `tsipam registry import <file|->`: applies a HuJSON registry document
  to the selected registry in one atomic transaction, running the same
  collision, overlap, and normalization rules as `reserve`.
- By default import requires an empty target registry. `--merge` adds to a
  non-empty one and refuses the whole import on any conflict. Import never
  deletes reservations.
- `--plan` reports what import would do (per-entry `add`, `unchanged`,
  `update`, or `conflict`) without writing, and exits 1 when the import
  would be refused.
- Import reports the HuJSON-only content it does not carry over: comments
  and non-canonical key spellings are reported and dropped. Unknown
  top-level keys refuse the import unless `--drop-unknown` is given.
- Both commands use the existing `--registry-dir`/`--tailnet` selection,
  the JSON envelope, exit codes, and sanitization, and go through the goa
  service like every registry command. The service exchanges structured
  reservations, and the CLI renders and parses HuJSON, so the commands work
  unchanged once `--registry` (`add-sqlite-store`) and `--server`
  (`add-serve-tsnet`) exist.
- Extend `tsipam -schema` and the reference docs to cover both commands.

## Capabilities

### New Capabilities

- `registry-transfer`: exporting a registry as a canonical HuJSON document
  and importing one atomically into the selected registry.

### Modified Capabilities

- `schema`: the catalog gains `registry export` and `registry import`
  with their flags, and the exit-code descriptions cover import refusals.
- `error-envelope`: `import_refused` joins the verdict codes that exit 1, and a
  refused plan leaves its report on stdout.
- `mutation-safety`, `command-inputs`: `registry import` joins their command
  lists.

## Impact

- **CLI:** new `registry` command group in `cmd/tsipam`.
- **Service:** two new goa methods, `export_registry` and
  `import_registry`, in `design/`, with `gen/` regenerated.
- **Code:** `registry.Ledger` gains export and atomic bulk-import
  operations. `hujsonstore` gains canonical document rendering and
  source-document parsing that reports dropped content.
- **Docs:** `docs/reference/json-envelope.md`, `exit-codes.md`,
  `registry-format.md`, plus a how-to for moving a registry between stores.
- **Smoke:** README examples for export and import, with smoke cases.
- **Dependencies:** none added.
