# Proposal: Address spaces beyond the tailnet

## Why

`tsipam` should plan a whole network's address space. Tailscale comes first,
but on-prem and cloud ranges belong in the same plan. Today they cannot be
recorded: any pool that no router advertises is drift, so one on-prem
reservation makes `check` and `check-policy` exit 1. The registry also refuses
the same CIDR at two sites, although reuse across sites is the problem 4via6
exists to solve.

A policy cannot stand in for that record. It holds a site ID only as
hexadecimal inside an IPv6 string, with no site name. A synthetic 24-site
policy shows the cost: `fd7a:115c:a1e0:b1a:0:14:a00:0/120` is site 20, every ID
differs from how it reads in decimal, and `:31:` means 49 while 31 is also in
use. Today's `check-policy` calls all 36 authored prefixes drift and names no
site ID.

## What Changes

- **Scope.** `CONTEXT.md`, `README.md`, `ROADMAP.md`, and the docs describe
  `tsipam` as network-wide address planning that starts with Tailscale. The
  hard no on writes widens from the tailnet to every network it plans.
  Writes elsewhere wait for a plugin boundary and their own decision. The docs
  also record which tool owns what: `tsacl` is the ACL tool, its
  `pkg/policycore` is the typed policy library, its `pkg/ipcalc` only
  calculates, and `tsipam` alone both calculates and stores. A policy holds a
  site ID only inside an IPv6 string, so the mapping lives in the registry.
- **Named address spaces.** `tsipam space reserve|release|list` manages spaces
  inside one registry. The same CIDR may be reserved once per space. Existing
  pools stay in the default space, named `tailnet`.
- **4via6 mapping.** `tsipam space map <space> <site-id>` records which site ID
  reaches a space, and `space unmap` removes it. A pool in a mapped space is
  shown as `4via6:3:Boston:10.0.0.0/24`, with its via route. `space map` also
  takes a via prefix, so nobody converts hexadecimal by hand. `plan`, `check`,
  and `check-policy` match live and authored via prefixes to those pools.
- **External pools.** `subnet reserve --usage external` marks space that no
  tailnet route may use. It blocks overlaps and allocation, is not drift when
  unadvertised, and is a `conflict` when a route overlaps it.
- **`--space`** on `subnet reserve|allocate|release|list`, `calc next-free`, and
  `calc aggregate --from-registry`.
- **Comparison output** gains the `external` state and optional `space` and
  `mappings` fields.

A registry without spaces or external pools keeps its exact bytes, output, and
exit codes.

## Capabilities

### New Capabilities

- `address-spaces`: naming, storing, mapping, and releasing address spaces, and
  the pools they hold.

### Modified Capabilities

- `subnet`: overlap and allocation are decided within one space; pools have a
  usage.
- `siteid`: a site ID that a space maps cannot be released.
- `plan-check`: via routes of mapped site IDs are compared in their space, and
  external pools are classified.
- `policy-check`: the same two rules, applied to authored CIDRs.
- `calc`: `next-free` and registry aggregation select a space.
- `schema`: the catalog lists the space commands and flags.

## Impact

- **Order.** After `adopt-ax-go-runtime`, which is being applied now and
  modifies some of the same requirements. Before `add-registry-export-import`
  and `add-sqlite-store`: their document shape and primary keys assume one
  space per registry, so they are cheaper to revise as plans than to migrate.
  `add-serve-tsnet` and `add-server-observability` also need revising.
- **Cross-repo.** `tsacl`'s `addrspace` must export 4via6 encoding and decoding
  (rshade/tsacl#87), and `tsipam` needs the release. v0.1.0 has
  `addrspace.SiteID` but nothing that returns the embedded IPv4 prefix.
- **Code.** `internal/registry` and both stores, `internal/planner`,
  `internal/policycheck`, `internal/ipamsvc`, `design/` and `gen/`, and the
  `subnet`, `calc`, and new `space` commands in `cmd/tsipam`.
- **Contracts.** Additive: a top-level registry map, an optional pool field,
  optional JSON fields, one new state, five commands, and three flags.
- **Dependencies.** None added.
- **Not included.** Importing the site IDs and ranges of existing sites from
  a policy or a live tailnet. It is a recorded later item, and it must read
  the whole policy: 19 of 48 via prefixes in that sample appear only in
  `autoApprovers` or `grants`, which `check-policy` does not read.
- **Followed by** `add-live-sources`, which compares a space with networks
  other than the tailnet.
