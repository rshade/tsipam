# Plan Check Specification Delta

## MODIFIED Requirements

### Requirement: Compare a complete registry and live inventory read-only

`plan` and `check` SHALL accept no positional arguments and SHALL share
`--registry-dir` (default `.`) and `--tailnet` registry selection. Registry
selection SHALL resolve from the flag, then `TAILSCALE_TAILNET`, then `local`.
The live tailnet SHALL always come from `TAILSCALE_TAILNET`, with
the same API-key/OAuth authentication as `devices list`; both SHALL be required
unless `--only` leaves the tailnet out. Both commands SHALL
read site IDs, subnet pools, and service-address reservations from one
validated registry snapshot and, unless `--only` leaves the tailnet out, collect
all device advertised routes and Services before reporting results. They SHALL NOT modify the registry or
control plane or any source. UDP-port reservations SHALL be excluded because their live
listen-port setting is unavailable, and the output SHALL disclose this.

#### Scenario: A storage alias selects the registry independently

- **WHEN** `plan --tailnet production` runs with `TAILSCALE_TAILNET=example.com`
- **THEN** it compares `production.ipam.hujson` with live `example.com`
- **AND** the result identifies both sources and leaves registry bytes unchanged

#### Scenario: Missing data cannot produce a partial clean report

- **WHEN** the registry is missing, one compared map is invalid, a selected live
  collection fails, or a live route/address is malformed
- **THEN** both commands exit 2 with empty stdout and a visible stderr error
- **AND** an empty Services list is valid, but a missing list is not

#### Scenario: The tailnet is required by default

- **WHEN** `check` runs without `TAILSCALE_TAILNET` and without `--only`
- **THEN** it exits 2, whether or not any space declares a source

### Requirement: Deterministic report and CI verdict

Both commands SHALL emit the versioned envelope with `comparison` containing
`live_tailnet`, `drift_count`, `entries`, and `excluded` allocation kinds.
Each entry SHALL contain `kind`, `state`, `key`, optional `expected`, and
`observed` and `routes` arrays. Kinds SHALL be `site_id`, `subnet_pool`, and
`service_address`; `excluded` SHALL contain `udp_port`. `observed` SHALL hold
device names for route/site entries and service names for address entries, and
`<source>/<resource>` for entries whose `origin` is `sources`.
Entries SHALL order by kind (site IDs, pools, services), then space (default
first, then by name), then numeric site ID or network address and prefix
length, then origin (tailnet, then sources); evidence SHALL be sorted and
deduplicated.
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

### Requirement: Read every declared source by default

Without `--only`, `plan` and `check` SHALL read the tailnet and every source that
the registry declares before reporting.

#### Scenario: A source is not optional

- **WHEN** `prod` declares a source, no credentials for it exist, and `check` runs
- **THEN** it exits 2 with empty stdout

### Requirement: Limit a comparison to chosen origins

`plan` and `check` SHALL accept a repeatable `--only` naming `tailnet` or a space
that declares sources. Only the named origins SHALL be read and reported.
`tailnet` SHALL select every row compared with the tailnet, including those of
mapped spaces. Pools compared with no live state SHALL be reported only without
`--only`. Any other value SHALL be a usage error.

#### Scenario: A space without Tailscale credentials

- **WHEN** `check --only prod` runs with AWS credentials and no
  `TAILSCALE_TAILNET`
- **THEN** only the `sources` rows of `prod` are reported, `live_tailnet` is empty,
  and no Tailscale request is made

#### Scenario: A space with no source

- **WHEN** `check --only Plant` runs and `Plant` declares none
- **THEN** it exits 2 as a usage error

### Requirement: Disclose what was left out

When `--only` leaves out the tailnet or a space's sources, `excluded` SHALL name
`tailnet` and `sources:<space>` respectively, in both output formats, beside
`udp_port`. A clean result SHALL NOT be reported without them.

#### Scenario: A partial run says so

- **WHEN** `prod` declares a source and `check --only tailnet` finds no drift
- **THEN** it exits 0 and `excluded` holds `sources:prod` and `udp_port`
