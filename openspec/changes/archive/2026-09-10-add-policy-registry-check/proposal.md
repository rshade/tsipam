# Policy and registry consistency

## Why

Authored ipset address space and reserved subnet pools can drift before anything is deployed.
Issue #288 needs an offline comparison of these two plans alongside the existing registry/live checks.

## What Changes

- Add `tail-ipam check-policy <acl-file>` with registry selection, human/JSON output, and sanitization.
- Resolve static ipset members and report authored CIDRs without a containing reservation and pools with no references.
- Resolve literal addresses, CIDRs, ranges, policy host aliases, nested ipsets, and ordered add/remove operations.
  Reject unsupported selectors and incomplete input explicitly; never produce a partial success report.
- Expose the read-only comparison through the existing generated goa transport and command schema.
- Exit 0 for consistency, 1 for drift, and 2 for invalid or unreadable input or output failures.

## Capabilities

### New Capabilities

- `policy-check`: Offline consistency between all authored ipset definitions and reserved subnet pools.

### Modified Capabilities

None. Existing commands and output fields retain their behavior.

## Impact

Implementation belongs in `tail-ipam`, which owns the registry and already offers allocation checks.
Only its module changes; `tail-acl` provides the input format and its CLI remains unchanged.
The issue's two component labels still require the repo-wide verification gates.
Promote the already pinned `go4.org/netipx` dependency to direct use for static address-set subtraction.
No live API access, policy upload, registry migration, or mutation is involved.
