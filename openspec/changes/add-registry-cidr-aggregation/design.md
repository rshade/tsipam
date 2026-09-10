# Design: Registry CIDR Aggregation

## Context

See proposal.md for motivation. `calc.go` already contains both the pure
aggregate path and `listSubnetPoolReservations`, which calls the generated
HTTP client for `calc next-free`. Registry commands share selection flags
with `.` as directory default and flag/environment/`local` tailnet resolution.
The output envelope already exposes `supernets`, but currently treats every
supernet as stateless data exempt from sanitization.

## Goals / Non-Goals

**Goals:** Compose the existing read and aggregation paths while preserving
positional behavior, read-only guarantees, and output provenance.

**Non-Goals:** New service methods, registry decoding in the command, live
inventory collection, changes to `policycore`, or IPv6 registry acceptance
coverage assigned to issue #255.

## Decisions

### Select the source before running the calculation

Add a boolean `--from-registry` and the shared registry flags. With it enabled,
reject every positional CIDR before opening the transport. Without it, require
one or more positional CIDRs and reject explicitly supplied registry flags
instead of silently ignoring them. Explicit `--from-registry=false` uses the
positional path.

Interpret the issue's required registry selection through the existing shared
flags and their defaults, consistent with `subnet list` and `calc next-free`.
Requiring both flags to be spelled explicitly would add a different selection
rule for this command and prevent the established environment fallback.

### Reuse transport and arithmetic

Keep the aggregate command and its helpers together in a focused
`calc_aggregate.go` file. Registry mode calls `loopback.RunWithLogger` and
`listSubnetPoolReservations`, then feeds the returned CIDRs into the same
parse/normalize/aggregate helper as positional mode. No family filter or new
merge algorithm is introduced; the IPv6-capable library is already shipped.

An empty successful API response becomes a non-nil empty supernet slice.
Missing files and malformed maps remain API errors, never empty successes.
Tests use the real local transport and synthetic registry files, with a device
collector that fails the test if invoked.

### Carry source provenance to the output boundary

Keep `command: calc aggregate` and the existing `supernets` field. Registry
results include the resolved tailnet and an unexported provenance flag on the
envelope so sanitization can redact derived supernets. Positional results keep
their current envelope and exemptions. A new public payload field or schema
version would be unnecessary because the result shape remains compatible.

Register derived supernets before sweeping messages, and preserve provenance
when rebuilding sanitized output. Existing error sanitization inventories
registry identities only in registry mode: merely adding registry flags to
the command must not cause a positional failure to read local registry files.
Inventory raw and trimmed stored CIDRs, canonical diagnostic suggestions,
and reservation owners, including escaped invalid owner values, so malformed
entries remain diagnosable without exposing those identities.

Update README and schema prose to distinguish the two sources. The current
main specs overstate the blanket calc exemption; the deltas scope it to
stateless modes and retain all existing scenarios.

## Risks / Trade-offs

- Registry output accidentally inherits the stateless exemption: assert
  human and JSON redaction under identity and strict levels, including
  tailnet-scoped deterministic placeholders and safe failure diagnostics.
- Empty input becomes a usage error in registry mode: test empty and absent
  subnet maps separately from missing or malformed registry files.
- Argument validation changes positional semantics: retain existing calc
  tests and add both flag spellings, explicit false mode, and source-conflict
  cases before I/O.
- Schema fixture changes obscure regressions: regenerate it deliberately and
  inspect the exact command/flag/prose diff.

## Migration Plan

Ship the additive flag with documentation and tests. No registry migration is
needed. Reverting the change removes the new mode without changing stored
state. Archive this OpenSpec change only after its implementation PR merges.
