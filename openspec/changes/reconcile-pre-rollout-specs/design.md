# Design: Reconcile Pre-Rollout Specs

## Context

See `proposal.md` for motivation. PR #300 merged the IPv4 block-facts change
as `00273dd`; PR #305 merged UDP-port reservations as `1a7b90b`. Both preceded
the backfill in PR #316 (`c321343`). Their original artifacts describe the
features as first shipped, while the canonical specs must describe current
behavior.

The later IPv6 block-facts implementation also merged in PR #327. Its active
`add-ipv6-calc-info/specs/calc/spec.md` already targets `calc`, so it supplies
the current family semantics without introducing another capability name.

## Goals / Non-Goals

**Goals:** Preserve the requirements from both sources, make their canonical
homes explicit, and archive the two historical changes without adding any
main-spec directories.

**Non-Goals:** Runtime changes, editing generated output or fixtures, changing
other active or archived changes, or renaming existing capability directories.

## Decisions

### Keep the current command-group names

`calc` and `udpport` remain authoritative. They are already used by the
backfill and newer work. Splitting out `calc-info` or replacing `udpport`
would require migrating current references and leave overlapping contracts.
The more detailed requirements belong within the existing specs.

### Merge meaning, preserving the original records

Use this change's deltas and the `openspec-sync-specs` workflow to update
the canonical specs. Existing requirement headings and scenarios survive;
details are added to the corresponding requirement or a distinct concern.
Preserve the original change artifacts byte-for-byte when archiving them.

The following maps every requirement from the two pre-rollout deltas to its
canonical destination. Names in the destination column are requirement names
within `calc` or `udpport`, not additional capabilities.

| Original `calc-info` requirement | Canonical `calc` destination |
| --- | --- |
| IPv4 block facts | Reporting block facts for a CIDR; IPv4 usable ranges include point-to-point and host routes; Calc info accepts one CIDR and rejects malformed input |
| RFC 3021 point-to-point and host-route blocks | IPv4 usable ranges include point-to-point and host routes |
| IPv6 input is rejected with a follow-up pointer | Superseded by the merged IPv6 behavior in Reporting block facts for a CIDR |
| IPv4-mapped input is normalized | Calc info normalizes IPv4-mapped input |
| Output contract | Calc info renders block facts in the versioned envelope; existing count and sanitization requirements |
| Invalid input is a usage error | Calc info accepts one CIDR and rejects malformed input |
| Agent discoverability | Calc info is discoverable through schema |

| Original `udp-port-reservations` requirement | Canonical `udpport` destination |
| --- | --- |
| UDP-port reservation input normalization | Port and device-name normalization |
| Explicit per-site reservation and collision semantics | Port reservation is scoped per site, reusable across sites |
| Nested comment-preserving registry persistence | Comment-preserving nested registry persistence |
| Idempotent per-site release | Idempotent per-site release |
| Deterministic and optionally filtered listing | Existing site-filter requirement; Deterministic listing sorted by site then numeric port |
| API-first command path and discoverability | API-first command path and discoverability |
| Identity-safe and stream-safe output | Identity-safe and stream-safe output |

The UDP backfill's metadata exclusion, valid-UTF-8 rule, required-site rule,
and verified release-repair requirement remain intact. The richer persistence
rule explicitly accommodates release repair instead of claiming all invalid
maps always prevent release.

### Resolve historical differences using shipped behavior

`calc info` now supports IPv6, so the old rejection clause is historical.
Reuse the complete block-facts requirement from the merged IPv6 delta. This
retains its legacy scenario identifier and explanatory comment, which preserve
OpenSpec's scenario identity while the scenario body asserts success.
Put additional IPv4 details in separate requirements so a later sync of the
unchanged IPv6 delta cannot remove them or drop newly added scenarios.

The original phrase "or take any flags" means no command-specific flags:
the original design explicitly allows shared `--format` and `--sanitize`.
The reconciled requirement states that distinction.

UDP-port diagnostics distinguish malformed decimal syntax from numeric input
outside 1024-65535. Preserve rejection without writes for both cases, but
require a range diagnostic only for out-of-range decimal input.

Evidence comes from `cmd/tail-ipam/calc.go`, `calc_test.go`, `output.go`, and
`schema.go`, plus `policycore/ipcalc/info.go`. UDP behavior is exercised by
`internal/registry/udpport_test.go`, `udpport_load_test.go`,
`udpport_hujson_test.go`, `internal/ipamsvc/udpport_test.go`, and the CLI's
`udpport_test.go` and `udpport_transport_test.go`. Existing tests cover the
scenarios; this documentation change requires no new runtime tests.

### Archive only after verifying the semantic merge

After syncing and validating this reconciliation, archive
`add-tail-ipam-calc-info` and `add-udp-port-allocation-registry` with
`openspec archive <name> -y --skip-specs`. Their original deltas have been
accounted for under different canonical names, so replaying those old paths
would recreate the defect. This flag skips redundant promotion, not the
requirement inventory or spec validation. Do not use `--no-validate`.

The original changes' completed tasks and merged PRs authorize archival.
This reconciliation's own change remains active until its PR merges. Its
main-spec deltas are already synced; assess that state again before its
later archive.

## Risks / Trade-offs

- Lost detail during deduplication: use the requirement mapping and existing
  test cases to verify both source contracts, not just matching headings.
- Obsolete IPv6 rejection: use the merged follow-up's complete requirement
  and verify that its delta remains compatible with the reconciled main spec.
- Accidental duplicate directories: compare the main-spec directory set
  before and after sync/archive; it must remain the same eight capabilities.
- Historical artifacts use old names: retain them as dated history and keep
  this mapping beside the reconciliation deltas to explain the canonical homes.

## Migration Plan

Sync the two reconciliation deltas, validate all main specs, archive the two
merged source changes, and confirm each archive is byte-identical to its
original tree. Run the required repository checks and review the diff before
publishing the PR. Reverting this documentation-only commit restores the prior
specs and active change directories without changing runtime behavior.
