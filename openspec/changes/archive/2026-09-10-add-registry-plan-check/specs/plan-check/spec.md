# Plan and Check Specification Delta

## Purpose

Compare declared address-space allocations with live control-plane inventory
so operators can inspect drift and gate deployments without changing state.

## ADDED Requirements

### Requirement: Compare a complete registry and live inventory read-only

`plan` and `check` SHALL accept no positional arguments and SHALL share
`--registry-dir` (default `.`) and `--tailnet` registry selection. Registry
selection SHALL resolve from the flag, then `TAILSCALE_TAILNET`, then `local`.
The live tailnet SHALL always come from required `TAILSCALE_TAILNET`, with
the same API-key/OAuth authentication as `devices list`. Both commands SHALL
read site IDs, subnet pools, and service-address reservations from one
validated registry snapshot and collect all device advertised routes and
Services before reporting results. They SHALL NOT modify the registry or
control plane. UDP-port reservations SHALL be excluded because their live
listen-port setting is unavailable, and the output SHALL disclose this.

#### Scenario: A storage alias selects the registry independently

- **WHEN** `plan --tailnet production` runs with `TAILSCALE_TAILNET=example.com`
- **THEN** it compares `production.ipam.hujson` with live `example.com`
- **AND** the result identifies both sources and leaves registry bytes unchanged

#### Scenario: Missing data cannot produce a partial clean report

- **WHEN** the registry is missing, one compared map is invalid, either live
  collection fails, or a live route/address is malformed
- **THEN** both commands exit 2 with empty stdout and a visible stderr error
- **AND** an empty Services list is valid, but a missing list is not

### Requirement: Classify site IDs by advertised 4via6 presence

Site-ID entries SHALL compare canonical numeric IDs with the IDs decoded from
advertised 4via6 routes. A reserved live ID SHALL be `matched`, a reserved
absent ID SHALL be `reserved_but_unadvertised`, and a live unreserved ID SHALL
be `unreserved_but_live`. Multiple advertisers SHALL be grouped as evidence,
not reported as a live collision. A malformed 4via6 route SHALL fail collection
validation. Site labels SHALL remain registry metadata, not asserted device
or physical-site identities.

#### Scenario: Numeric ID presence does not require matching device names

- **WHEN** ID 7 is reserved for `hq`, and `router.example.com` advertises
  `fd7a:115c:a1e0:b1a:0:7:a00::/120`
- **THEN** ID 7 is matched with that router and route as evidence

### Requirement: Compare subnet pools using full route containment

An IPv4 or IPv6 pool SHALL be `matched` when it contains at least one entire
advertised route, including an exact match. A pool with no contained live
route SHALL be `reserved_but_unadvertised`. A live route contained in no
reserved pool SHALL be `unreserved_but_live`, including a route that extends
outside a smaller reservation. Comparison SHALL remain within each address
family, normalize IPv4-mapped routes consistently with calc, and compare
4via6 routes as their advertised IPv6 prefixes without implicitly mapping
them to an IPv4 pool. Site labels SHALL NOT be matched against device names.
Repeated routes from multiple routers SHALL be grouped deterministically.

#### Scenario: A child route uses its reserved pool

- **WHEN** `10.0.0.0/16` is reserved and a router advertises `10.0.1.0/24`
- **THEN** the pool is matched and that route is not unreserved

#### Scenario: A broader advertisement is not covered by a smaller pool

- **WHEN** only `10.0.1.0/24` is reserved and a router advertises `10.0.0.0/16`
- **THEN** the pool is reserved-but-unadvertised and the route is unreserved-but-live

### Requirement: Compare service addresses with their service identities

Service-address reservations SHALL compare canonical IPv4 address and
`svc:<name>` pairs with the Services API. The same pair SHALL be `matched`;
a reserved address not live SHALL be `reserved_but_unadvertised`; a live IPv4
address not reserved SHALL be `unreserved_but_live`. An address assigned to a
different service, including multiple live owners, SHALL be `conflict` with
the expected and observed owners. IPv6 service addresses SHALL be excluded
because the registry supports only per-service IPv4 addresses.

#### Scenario: The correct address belongs to the wrong service

- **WHEN** `100.100.100.8` is reserved for `svc:dashboard` but the API assigns
  it to `svc:metrics`
- **THEN** one conflict entry identifies the address and both service names

### Requirement: Deterministic report and CI verdict

Both commands SHALL emit the versioned envelope with `comparison` containing
`live_tailnet`, `drift_count`, `entries`, and `excluded` allocation kinds.
Each entry SHALL contain `kind`, `state`, `key`, optional `expected`, and
`observed` and `routes` arrays. Kinds SHALL be `site_id`, `subnet_pool`, and
`service_address`; `excluded` SHALL contain `udp_port`. `observed` SHALL hold
device names for route/site entries and service names for address entries.
Entries SHALL order by kind (site IDs, pools, services), then numeric site ID
or network address and prefix length; evidence SHALL be sorted and deduplicated.
Every non-matched entry SHALL contribute one to `drift_count`. Empty arrays
SHALL be `[]`. Human output SHALL identify every row and conclude with the
drift count and exclusion. Envelope status SHALL be `ok` or `drift` in both
commands. A completed `plan` SHALL exit 0. `check` SHALL exit 1 on any drift
and 0 otherwise. Collection/usage errors SHALL exit 2. Results SHALL be
written before the check failure is returned.

#### Scenario: Planned but unused allocation blocks check

- **WHEN** a registry reserves ID 8 with no corresponding live route
- **THEN** both commands print that drift, plan exits 0, and check exits 1

#### Scenario: An empty comparison is explicitly clean

- **WHEN** the three registry maps and both live lists are empty
- **THEN** both commands exit 0 with zero drift and an empty entries array
- **AND** UDP-port exclusion remains visible

### Requirement: Comparison identities are sanitized

Identity and strict sanitization SHALL redact registry/live tailnets, pool
CIDRs, service addresses, expected site/service names, observed device/service
names, and evidence routes in human and JSON output. Numeric site IDs,
states, kinds, counts, and exclusions SHALL remain visible. Keyed placeholders
SHALL be scoped to the selected registry alias, consistently across repeated
runs. Diagnostics SHALL not expose credentials or unknown API response bodies.

#### Scenario: Sanitized conflict retains its meaning

- **WHEN** a service-address conflict is emitted with identity sanitization
- **THEN** its address and both service names are replaced by placeholders
- **AND** the state remains conflict and check still exits 1

### Requirement: Document authority and observability limits

Documentation SHALL identify tail-ipam as authoritative for registry-versus-live
allocation drift, and tailnet-audit issues #221/#222 as the separate live-only
route-overlap and site-ID-collision checks. Plan/check SHALL NOT duplicate
those collision findings. A match SHALL mean observed allocation presence,
not route approval, reachability, service health, or physical-site ownership.

#### Scenario: High-availability advertisers are evidence

- **WHEN** two routers advertise a reserved route or site ID
- **THEN** plan/check group both names into the matching entry without
  inventing a router-collision finding
