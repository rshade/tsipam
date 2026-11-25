# Plan Check Specification Delta

## MODIFIED Requirements

### Requirement: Compare subnet pools using full route containment

An IPv4 or IPv6 pool SHALL be `matched` when it contains at least one entire
advertised route, including an exact match. A pool with no contained live
route SHALL be `reserved_but_unadvertised`. A live route contained in no
reserved pool SHALL be `unreserved_but_live`, including a route that extends
outside a smaller reservation. Comparison SHALL remain within each address
family, normalize IPv4-mapped routes consistently with calc, and compare
4via6 routes as their advertised IPv6 prefixes without implicitly mapping
them to an IPv4 pool. A 4via6 route whose site ID a space maps SHALL instead
be attributed to that space: it MAY match a default-space IPv6 pool that
contains it, and SHALL NOT be `unreserved_but_live` in the default space. A
pool with usage `external` SHALL follow the external-pool requirement. Site
labels SHALL NOT be matched against device names.
Repeated routes from multiple routers SHALL be grouped deterministically.

#### Scenario: A child route uses its reserved pool

- **WHEN** `10.0.0.0/16` is reserved and a router advertises `10.0.1.0/24`
- **THEN** the pool is matched and that route is not unreserved

#### Scenario: A broader advertisement is not covered by a smaller pool

- **WHEN** only `10.0.1.0/24` is reserved and a router advertises `10.0.0.0/16`
- **THEN** the pool is reserved-but-unadvertised and the route is unreserved-but-live

#### Scenario: An unmapped site ID keeps today's behavior

- **WHEN** no space maps site ID 7 and a router advertises a via route for it
  that no IPv6 pool contains
- **THEN** the via route is one `unreserved_but_live` row keyed by its IPv6
  prefix

### Requirement: Deterministic report and CI verdict

Both commands SHALL emit the versioned envelope with `comparison` containing
`live_tailnet`, `drift_count`, `entries`, and `excluded` allocation kinds.
Each entry SHALL contain `kind`, `state`, `key`, optional `expected`, and
`observed` and `routes` arrays. Kinds SHALL be `site_id`, `subnet_pool`, and
`service_address`; `excluded` SHALL contain `udp_port`. `observed` SHALL hold
device names for route/site entries and service names for address entries.
Entries SHALL order by kind (site IDs, pools, services), then space (default
first, then by name), then numeric site ID or network address and prefix
length; evidence SHALL be sorted and deduplicated.
Every entry that is neither `matched` nor `external` SHALL contribute one to
`drift_count`. An entry in a named space SHALL also contain `space`, and one with
4via6 evidence SHALL contain `mappings`; both fields SHALL be absent otherwise.
Empty arrays SHALL be `[]`. Human output SHALL identify every row and conclude with the
drift count and exclusion. Envelope status SHALL be `ok` or `drift` in both
commands. A completed `plan` SHALL exit 0. `check` SHALL exit 1 on any drift
and 0 otherwise. Collection/usage errors SHALL exit 2. Results SHALL be
written before the check failure is returned. The check failure SHALL be an
error object on stderr with `error_code` `drift_detected` and the drift count
in its `context`.

#### Scenario: Planned but unused allocation blocks check

- **WHEN** a registry reserves ID 8 with no corresponding live route
- **THEN** both commands print that drift, plan exits 0, and check exits 1

#### Scenario: An empty comparison is explicitly clean

- **WHEN** the three registry maps and both live lists are empty
- **THEN** both commands exit 0 with zero drift and an empty entries array
- **AND** UDP-port exclusion remains visible

#### Scenario: The verdict is machine-readable

- **WHEN** `check --format json` finds two drifting rows
- **THEN** stdout holds the report, the error object's `context.drift_count` is
  2, and the command exits 1

#### Scenario: External space does not fail a gate

- **WHEN** the only reservation is an external pool that no route overlaps
- **THEN** one row has state `external`, the drift count is 0, and `check` exits 0

## ADDED Requirements

### Requirement: Compare mapped spaces through their site ID

`plan` and `check` SHALL also read named spaces from the same registry snapshot.
For each via route whose site ID maps a space, the embedded IPv4 prefix SHALL be
compared with that space's IPv4 routed pools by the containment rules of the
default space. A contained prefix SHALL match its pool, and one that no pool
contains SHALL be `unreserved_but_live` in that space, keyed by the IPv4 prefix.

#### Scenario: Two sites reuse one range

- **WHEN** site IDs 3 and 4 map `Boston` and `Chicago`, both hold
  `10.0.0.0/24`, and only the via route for site ID 3 is advertised
- **THEN** the `Boston` row is `matched` and the `Chicago` row is
  `reserved_but_unadvertised`

#### Scenario: Evidence names the mapping

- **WHEN** the `Boston` pool matches
- **THEN** `routes` holds `fd7a:115c:a1e0:b1a:0:3:a00:0/120` and `mappings` holds one
  entry labelled `4via6:3:Boston:10.0.0.0/24`

#### Scenario: A via route outside the plan

- **WHEN** site ID 3 maps `Boston` and a router advertises the via route for site
  ID 3 and `10.7.0.0/24`, which no `Boston` pool contains
- **THEN** one row in space `Boston` is `unreserved_but_live` with key
  `10.7.0.0/24`

### Requirement: Classify pools not expected on the tailnet

A pool with usage `external`, a pool in a space compared with no live state, and an
IPv6 pool in a space compared only through via routes SHALL be `external`, unless
the external-pool conflict requirement applies.

#### Scenario: An on-prem space

- **WHEN** `Plant` is not mapped and holds `10.0.0.0/8`
- **THEN** its row is `external` and contributes no drift

### Requirement: A route into external space is a conflict

A pool with usage `external` that overlaps a live prefix compared with its space
SHALL be `conflict`, with the evidence for that prefix, and the prefix SHALL NOT also
be `unreserved_but_live`. For the tailnet the live prefixes are advertised routes. A
default route (`0.0.0.0/0`, `::/0`, or a via route with an embedded `/0`) SHALL NOT
cause a conflict.

#### Scenario: A router advertises on-prem space

- **WHEN** `10.50.0.0/16` is external and a router advertises `10.50.3.0/24`
- **THEN** one row is `conflict`, the drift count is 1, and `check` exits 1

#### Scenario: A broader route also conflicts

- **WHEN** `10.50.0.0/16` is external and a router advertises `10.0.0.0/8`
- **THEN** the external pool's row is `conflict`

#### Scenario: An exit node is not a conflict

- **WHEN** `10.50.0.0/16` is external and a device advertises `0.0.0.0/0`
- **THEN** the external pool's row is `external`
