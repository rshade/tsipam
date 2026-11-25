# Design: Address spaces beyond the tailnet

## Context

See `proposal.md` for motivation. What shapes the approach:

- A pool is keyed by CIDR alone: in the HuJSON map, in `registry.Store`
  (`SubnetPools`, `PutSubnetPool`), and in the planned SQLite primary key
  `(tailnet_id, cidr)`. One registry cannot hold a CIDR twice.
- `planner.compareSubnetPools` reports every pool without a route as
  `reserved_but_unadvertised`, and `policycheck.Compare` reports every
  unreferenced pool as drift. Verified: one on-prem pool makes `check-policy`
  exit 1.
- `routeInventory` decodes only the site ID of a via route. `tsacl` v0.1.0 has
  no exported decoder for the embedded IPv4 prefix.
- Site names are free-form labels that decide collisions. They are not
  identifiers, and `siteIDs` values use the same labels.
- Verified against the working build: rewriting an entry drops fields that this
  version does not know (`usage` on a pool, `space` on a site ID), while an
  unknown top-level map survives byte for byte.

The lines between the tools, set by the owner on 2026-11-25:

| Tool | Role |
| --- | --- |
| `tsacl` | ACL tool: authors and checks policy |
| `tsacl/pkg/policycore` | Typed policy library |
| `tsacl/pkg/ipcalc` | Calculator library, no state |
| `tsipam` | Calculates and stores; the only one that does both |

### What a policy can and cannot tell us

No large 4via6 policy was available locally (the largest fixture has two via
prefixes), so a synthetic one was read: 24 sites drawing on 3 ranges, 108 via
prefixes of which 48 are distinct, in `autoApprovers`, `ipsets`, and `grants`.

- The site ID is hexadecimal. All 24 read differently in decimal, and several
  hex spellings are plausible decimals: `14` is 20, `31` is 49.
- The policy holds no site name, so only a separate record can supply one.
- One `/16` appears at 21 sites, so the IPv4 range identifies nothing.
- `check-policy` reads only `ipsets` and `hosts`, so 19 of the 48 distinct
  prefixes are invisible to it.

These are properties of the encoding and of current code. The counts describe
the sample, not real tailnets.

## Goals / Non-Goals

**Goals:**

- One implementation of overlap, allocation, and comparison rules, with the
  space as a parameter.
- Byte-identical behavior for registries that use none of this.
- A model that `add-live-sources` can extend without another format change.

**Non-Goals:**

- Reading any network other than the tailnet (`add-live-sources`).
- Importing existing sites from a policy or live state. The later change must
  read the whole policy, and copying live state into the registry still needs
  its own decision under `CONTEXT.md`.
- Widening `check-policy` beyond `ipsets`.
- Writing to any network, DNS, or policy.
- Spaces for Service addresses and UDP ports. Both are tailnet concepts.
- Renaming `--tailnet`, `{tailnet}` paths, or `<tailnet>.ipam.hujson`. They are
  contracts and already accept an alias.
- Nested spaces, or address translation between spaces.
- A `calc via` command. `tailscale debug via` converts both ways, and `space map`
  and `subnet list` cover the registry's needs.

## Decisions

### D1. A space is a named container; the default is implicit

`addressSpaces` is a new top-level map. `subnetPools` keeps its meaning and
place as the `tailnet` space, so no registry needs migrating.

```hujson
"addressSpaces": {
  "Boston": {
    "siteID": 3,
    "owner": "netops",
    "subnetPools": {"10.0.0.0/24": "boston-hq"},
  },
  "Plant": {
    "subnetPools": {"10.0.0.0/8": "plant"},
  },
},
```

*Alternative:* prefix pool keys (`"Boston:10.0.0.0/24"`) in `subnetPools`. Old
binaries would reject the whole map.

*Alternative:* a registry alias per space, which works today. It cannot relate a
site ID to a range, and it splits one plan over many files.

### D2. The 4via6 mapping lives on the space

`siteID` sits inside the space rather than as `space` on the site-ID entry,
because an older binary that re-reserves a site ID would silently drop the
field. It also keeps `siteIDs` parsing untouched.

The cost is a cross-map reference. `space map` requires the ID to be reserved.
`site-id release` refuses a mapped ID, but only when `addressSpaces` is valid, so
that the per-map isolation contract holds. A dangling mapping can then arise only
from a hand edit or a malformed map, and `plan` and `check`, which need both maps,
fail closed on it.

One ID per space and one space per ID. High-availability routers share an ID, so
this does not limit them. Allowing several IDs later is additive.

### D3. `usage` is on the pool; reachability is derived

Whether the tailnet should carry a pool depends on two facts: its space (default,
mapped, or neither) and its `usage`. Only `usage: "external"` is stored, so an
unmapped space needs no flag on each pool. An enum, rather than a boolean, leaves
room for values such as a migration hold.

An older binary that rewrites an external pool drops `usage`. The result is drift
in `check`, not silent loss of protection: the pool still blocks overlaps.

| Space | Family | `routed` | `external` |
| --- | --- | --- | --- |
| default | both | compared with routes | `external` or `conflict` |
| mapped | IPv4 | compared with decoded via routes | `external` or `conflict` |
| mapped | IPv6 | `external` | `external` |
| unmapped | both | `external` | `external` |

### D4. The store gains a space argument, not parallel methods

```go
SubnetPools(space string) (map[string]PoolEntry, error)
PutSubnetPool(space, cidr string, entry PoolEntry) error
DeleteSubnetPool(space, cidr string) (bool, error)
AddressSpaces() (map[string]SpaceEntry, error)
PutAddressSpace(name string, entry SpaceEntry) error
DeleteAddressSpace(name string) (bool, error)
```

`""` is the default space. `PoolEntry` embeds `Entry` and adds `Usage`;
`SpaceEntry` holds metadata and an optional site ID. `Entry` stays as it is for
site IDs, so neither type carries a field that does not apply to it.

`MapAddressSpaces` joins `MapKind` and covers the map with every pool in it.
`MapSubnetPools` still means the default space only, which is what keeps a
malformed `addressSpaces` from blocking default-space commands. Whole-map scoping
is coarser than per-space, and simpler to state and test.

`DeleteAddressSpace` in a store removes the entry as told. Refusing a space in use
is a `Ledger` rule, like every other rule.

### D5. Decoding belongs to `addrspace`, attribution to `tsipam`

`tsacl/pkg/policycore/addrspace` gains two pure functions (rshade/tsacl#87):

```go
MapVia(siteID uint32, v4 netip.Prefix) (netip.Prefix, bool)
UnmapVia(prefix netip.Prefix) (siteID uint32, v4 netip.Prefix, ok bool)
```

`UnmapVia` accepts what `addrspace.SiteID` accepts. `tsipam` adds the stateful half,
`internal/addrplan`: given the spaces, it attributes a via prefix to a space and
formats the label. `planner`, `policycheck`, and `Ledger` all use it, so the rule
exists once.

*Alternative:* put the codec in `ipcalc`, since it is arithmetic. The `ipcalc` spec
excludes Tailscale-specific concepts, while `addrspace` is policy-agnostic, owns the
via range and `SiteID`, and is tested against Tailscale's own encoder.

*Alternative:* decode locally until the release. `CONTEXT.md` forbids
reimplementing address logic, so the release comes first (task 1.2).

### D6. Labels are output, never keys

`4via6:<id>:<space>:<cidr>` is derived when rendering. Space names exclude `:` and
`/`, so the label parses unambiguously. The ID is always decimal.

In JSON, pools and comparison rows carry `space` and, where 4via6 applies, a
`mappings` array of `{label, site_id, cidr, route}`. Under sanitization the label is
rebuilt from already-sanitized parts instead of being treated as one opaque
identity, so it still lines up with the `cidr` and `space` fields.

### D7. Comparison order and attribution

1. Decode each live route. If its site ID maps a space, attribute it there.
2. Compare the default space with all routes as today, except that an attributed
   route never becomes `unreserved_but_live` there. It may still match an IPv6
   pool someone reserved for the via prefix, so adopting a mapping never turns a
   matched row into drift.
3. Compare each mapped space with its decoded prefixes, using the same function
   with the space's pools and routes.
4. Apply the external rules.

Default routes never conflict with external space: an exit node advertises
`0.0.0.0/0` without claiming the ranges inside it. Any other broader route does
conflict, because it does attract that traffic.

### D8. Allocation in a space

`Ledger.AllocateSubnetPool` takes the space. Inside the transaction it reads the
space, and then:

- default: uses `live_taken` as now;
- mapped: decodes `live_taken` and keeps the prefixes with the space's site ID;
- unmapped: ignores `live_taken`.

The CLI must decide whether to read devices before the call, so it lists spaces
first. A space can be mapped in between. The payload therefore gains a `live_read`
boolean, because an empty `live_taken` is indistinguishable from an omitted one on
the wire. The ledger refuses a mapped space when `live_read` is false.
`add-serve-tsnet` moves this read to the server and can drop the flag there.

### D9. API and errors

`space` is an optional attribute on the four subnet methods (query for `GET` and
`DELETE`, body for `POST`). New methods: `list_spaces`, `reserve_space`,
`release_space`, `map_space`, `unmap_space` under `/tailnet/{tailnet}/ipam/spaces`.

| Condition | goa error | Exit | `error_code` |
| --- | --- | --- | --- |
| Unknown space, bad name or usage, unreserved site ID | `invalid_input` | 2 | `validation_error` |
| Case variant, ID or space already mapped | `collision` (409) | 1 | `registry_collision` |
| Space or site ID in use | `collision` (409) | 1 | `registry_collision` |

`adopt-ax-go-runtime` fixes the codes that exit 1, so a key in use reuses the
collision code instead of adding one. The message names the dependents, which is
what distinguishes the remedy. `release_site_id` gains the `collision` error.

### D10. Where the scope is stated

`CONTEXT.md` is the authority. Its identity section says tsipam plans a network's
address space, Tailscale first. The hard no becomes "never writes to a network it
plans", the tool table above is added, and the verification questions follow.
`ROADMAP.md` gains, in the owner's order: cloud VPCs, DNS and DHCP, routers and
firewalls, IPAM systems. Parked: importing existing sites, and plugins with
writes. "On-prem CIDR planning" leaves Parked, because this change is its first
slice.

## Risks / Trade-offs

- [The label resembles a Tailscale identifier but is tsipam's own] → Document it as
  tsipam notation, and always print the real via route beside it.
- [An old binary edits a new registry] → D2 and D3 limit the damage to visible
  drift. `docs/reference/registry-format.md` says to upgrade every writer before
  using `usage`.
- [A policy pastes `:14:` and the operator maps `14`] → `space map` takes the prefix,
  echoes the decimal ID, and requires it to be reserved.
- [Four planned changes assume one space] → Sequence this first, and revise them
  (task 6.2) before anyone applies them.
- [`adopt-ax-go-runtime` is mid-apply and shares files] → Start from its merged
  result. The `plan-check` delta here already includes its verdict text.
- [Attribution relies on the registry being right] → A match still means only that
  presence agrees. Labels do not prove which physical site a router serves, as
  today.

## Migration Plan

1. Release `tsacl` with the codec.
2. Merge after `adopt-ax-go-runtime` and before `add-registry-export-import`.
3. No registry migration. Upgrade all writers before using spaces or `usage`.
4. Rollback: an older binary keeps `addressSpaces` intact and ignores it, so mapped
   via routes return to IPv6 rows and external pools become drift.
