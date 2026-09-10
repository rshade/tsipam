# Tasks: Reconcile Pre-Rollout Specs

## 1. Canonical spec sync

- [x] 1.1 Sync this change's `calc` and `udpport` deltas using the module's
  `openspec-sync-specs` workflow. Verify all mapped source requirements are
  represented and `openspec validate --specs` passes without new capability
  directories.
- [x] 1.2 Verify the merged IPv6 `calc info` delta remains compatible and the
  two canonical specs preserve requirements outside the selected deltas;
  confirm the sync is idempotent.

## 2. Archive the merged source changes

- [x] 2.1 Verify PRs #300 and #305 are merged and their tasks are complete,
  then archive `add-tail-ipam-calc-info` and
  `add-udp-port-allocation-registry` with `--skip-specs`. Compare archived
  file bytes against the original trees and verify the main-spec directory
  set remains unchanged.

## 3. Verification and review

- [x] 3.1 Run module build, tests, and lint; root `make test lint cross lint-md`;
  OpenSpec validation; and diff whitespace checks. Verify no runtime, generated,
  fixture, or unrelated change artifacts were modified.
- [x] 3.2 Review against issue #329 and the requirement mapping with the
  Standards, Spec, and Scout reviews; resolve confirmed findings and verify
  the final delta matches the synchronized specs.
