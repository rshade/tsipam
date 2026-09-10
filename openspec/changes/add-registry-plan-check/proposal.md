# Proposal: Registry Plan and Check

## Why

Issue #238 connects the declared address-space registry to live Tailscale
inventory. Operators need to see undeclared live allocations and reservations
that are not live, and to gate deployments on the same comparison.

## What Changes

- Add read-only `plan` and `check` commands covering site IDs, IPv4/IPv6
  subnet pools, and per-service IPv4 addresses.
- Compare a validated registry snapshot against devices and Services collected
  through the existing tscli authentication path.
- Report matched, reserved-but-unadvertised, and unreserved-but-live entries;
  identify service-address ownership conflicts explicitly.
- Return exit 0 for a completed plan, exit 1 for check drift, and exit 2 for
  invalid inputs or incomplete collection, with human and JSON output.
- Preserve identity sanitization, deterministic results, and the shared
  registry-selection flags; expose the commands through help and schema.
- Document the boundary with live-only collision checks in `tailnet-audit`.

## Capabilities

### New Capabilities

- `plan-check`: Registry-versus-live comparison, result states, collection
  completeness, and CI exit semantics.

### Modified Capabilities

- `schema`: Discover plan/check flags, comparison output, and drift exit codes.

## Impact

Confined to `tail-ipam`: goa design and regenerated API, registry snapshot
validation, live inventory collection, a pure planner, CLI rendering and
sanitization, tests, README, and these artifacts. Existing commands keep their
contracts. No new dependencies, live writes, or policycore changes are needed.
UDP ports remain registry-only under the explicit ROADMAP.md scope decision.
