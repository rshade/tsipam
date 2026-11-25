# Plan Check Specification Delta

## MODIFIED Requirements

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
