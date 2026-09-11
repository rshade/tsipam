# IPv6 next-free allocation

## Why

Issue #257 extends the existing IPv4 next-free command to IPv6 now that its
registry and search prerequisites have landed. The shared search already
handles both families, but the CLI rejects native IPv6 pools.

## What Changes

- Accept IPv6 pools and target lengths through `/128`.
- Preserve read-only collection, family-specific exclusions, ordered bare
  CIDRs, JSON output, sanitization, and successful exhaustion behavior.
- Document IPv6 examples and verify realistic pool sizes and address edges.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `calc`: extend next-free allocation and exhaustion to IPv6.

## Impact

Changes are limited to the CLI, its tests, help/schema fixture, and README.
The matching `policycore/openspec/changes/add-ipv6-next-free/` change adds
tests and benchmarks for the existing search contract. No algorithm, service
API, dependency, registry format, or JSON field changes are needed.
