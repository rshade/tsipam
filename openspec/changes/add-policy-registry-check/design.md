# Policy comparison design

## Context

See [proposal.md](proposal.md) for motivation. The existing `plan`/`check` command compares registry and live
snapshots through a generated goa service. Registry list operations already validate subnet pools independently.
`tail-acl/internal/aclpolicy` is private to that module. Shared selector expansion does not implement ordered
ipset operations, so it cannot establish a complete static comparison.

## Goals / Non-Goals

Keep policy parsing and classification pure and fixture-testable. Preserve the existing local HTTP transport,
registry selection, envelope, sanitization, and error conventions. Do not assess grant reachability, ACL validity
outside the relevant definitions, live advertisement, allocation owners, site IDs, services, or UDP ports.

## Decisions

### Ownership and transport

Put `check-policy` in `tail-ipam`: it owns the registry and allocation comparisons. Putting it in `tailnet-audit`
would add registry dependencies to an inventory-oriented tool. Do not import another module's internal parser
or introduce a shared policy framework for a single consumer. Add a goa method accepting policy text in a POST
body; the CLI reads the local policy once and the service reads subnet pools once. Paths never select files on a
remote service, and POST performs analysis only. The CLI does not require API credentials.

### Static scope

Follow the documented [ipset operations](https://tailscale.com/docs/features/tailnet-policy-file/ip-sets):
literal addresses, CIDRs, ranges, `host:` references, nested `ipset:` references, and ordered add/remove.
Reject `autogroup:internet` and unknown selectors instead of approximating their address space.
Parse HuJSON with duplicate checks for relevant objects, including case-insensitive top-level names, while
allowing unrelated policy fields. Report errors by definition/member position without echoing policy content.
Reject invalid shapes, missing references, cycles, scoped addresses, and mixed explicit/implicit operations when
removals occur. Host values resolve to literal addresses or CIDRs only. Bound nested expansion depth and materialized
prefix count, returning an incomplete-input error when exceeded.

### Matching and evidence

Keep effective prefixes for each addition separately. Subtract subsequent removals using the pinned
`go4.org/netipx` range algebra, then sort and deduplicate exact prefixes. Never merge adjacent authored CIDRs:
doing so could manufacture an unreserved supernet. Nested sets inherit their effective prefixes, and ranges and
subtracted prefixes use an exact CIDR cover without enumerating addresses.

An authored CIDR needs one reservation containing its whole prefix. Partial overlaps and a union of smaller
reservations do not satisfy that check. A pool is referenced by any overlapping effective CIDR, even if the CIDR
also extends outside that pool. Thus a broad authored CIDR can drift while the pool it covers remains referenced.
Use the existing `addrspace.Overlaps` for overlap evidence and prefix containment with mapped-address equivalence.
Preserve native IPv6 overlap as well as IPv4-mapped equivalence, matching existing address-space primitives.

Emit `policy_comparison` with `drift_count`, `authored` rows (`ipset`, `cidr`, `state`, `pools`) and `reserved` rows
(`cidr`, `site`, `state`, `ipsets`). Rows include matches as well as drift; arrays are always present.
Sort definitions lexically, prefixes by address then length, and evidence lexically. Duplicate memberships within
one ipset produce one row. Classifications are `matched`, `authored_but_unreserved`, and `reserved_but_unreferenced`.
Redact all ipset names, CIDRs, site labels, and registry aliases at the existing output boundary.

## Risks / Trade-offs

- Unsupported selectors prevent a complete check → exit 2 before any report; document the static scope in help.
- Sets can grow through subtraction or nesting → bound expansion and reuse resolved sets; no address enumeration.
- Case-ambiguous or duplicate definitions could change meaning → reject them rather than silently choosing a value.
- Allocation plans may intentionally differ → exit 1 and retain deterministic evidence for operator review.

## Migration Plan

This is additive with no storage migration. Regenerate committed goa output and the command schema golden.
Archive the OpenSpec change in a follow-up PR after merge.
