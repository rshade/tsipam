# Proposal: Registry CIDR Aggregation

## Why

Operators can aggregate supplied CIDRs but must copy subnet reservations out
of the registry first. Issue #254 adds a read-only way to summarize the
declared allocation footprint using the existing registry API.

## What Changes

- Add `calc aggregate --from-registry` with the shared `--registry-dir` and
  `--tailnet` selection flags, retaining their established defaults.
- Reject positional CIDRs combined with registry mode; retain the existing
  positional mode and its output contract.
- Read subnet reservations through the generated `ListSubnetPools` client
  and aggregate them with the existing CIDR implementation.
- Return an empty successful result for an empty registry; surface unreadable
  or malformed registries as errors without writing registry state.
- Sanitize registry-derived supernets and their tailnet routing key while
  preserving unredacted arithmetic over explicitly supplied CIDRs.
- Document the mode and expose its flags and sanitization behavior in schema.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `calc`: Registry-backed aggregation, source selection, empty/error handling,
  and the distinction between stateless and registry-derived output.
- `schema`: Discovery of aggregate registry flags and the corresponding
  sanitization contract.

## Impact

Changes are confined to the `tail-ipam` CLI, output handling, registry identity
inventory, tests, README, schema fixture, and OpenSpec artifacts. No new service methods, generated
API code, dependencies, registry mutations, or live-device reads are needed.
IPv4 registry scenarios are covered here; IPv6 and mixed-registry acceptance
coverage remains issue #255, using the same family-agnostic plumbing.
