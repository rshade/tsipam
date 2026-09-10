# Reconcile Pre-Rollout Capability Specs

## Why

The merged `calc info` and UDP-port changes predate the OpenSpec backfill and
target `calc-info` and `udp-port-reservations`, while the current specs use
`calc` and `udpport`. Archiving those deltas unchanged would create competing
specs and leave their more detailed requirements disconnected.

Issue: #329.

## What Changes

- Keep `calc` and `udpport` as the canonical capability names and merge the
  original changes' still-applicable requirements into them.
- Preserve exact IPv4 edge cases, mapped input, output, error, and schema
  contracts, plus UDP persistence, release, transport, and identity rules.
- Reconcile `calc info` with the already-merged IPv6 follow-up so archiving
  the original IPv4 change cannot restore its obsolete IPv6 rejection.
- Archive `add-tail-ipam-calc-info` and `add-udp-port-allocation-registry`
  after the semantic sync, using `--skip-specs` to preserve their historical
  artifacts without replaying obsolete capability names.
- Record requirement provenance and archive verification in this change.

## Capabilities

### New Capabilities

None. The existing capability-directory set must remain unchanged.

### Modified Capabilities

- `calc`: consolidate the detailed `calc info` contract under the current
  command-group spec, reflecting shipped IPv4 and IPv6 behavior.
- `udpport`: consolidate the detailed per-site reservation contract while
  retaining the backfill's release-repair and metadata exclusions.

## Impact

Only `tail-ipam/openspec/` changes. Existing Go code, generated APIs, CLI
output, dependencies, and fixtures retain their current behavior. The two
original changes are eligible for archive because PRs #300 and #305 merged;
this reconciliation change remains active until its own PR merges. Other
active changes and previously archived changes are outside this cleanup.
