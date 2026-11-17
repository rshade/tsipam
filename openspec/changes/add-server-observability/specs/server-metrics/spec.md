# Server Metrics Delta

## Purpose

Expose registry contents, pool capacity, and scheduled drift health from
`tsipam serve` as Prometheus metrics, so teams can graph usage and alert on
drift or pool exhaustion.

## ADDED Requirements

### Requirement: Serve metrics on the service listener behind read access

`tsipam serve` SHALL serve Prometheus text exposition at `/metrics` on the
same tailnet listener as the API. A request SHALL succeed only when the
caller holds the `read` action for every registry alias; a caller with no
grant or only a scoped one SHALL get 403 with no metric data. The endpoint
SHALL accept only `GET` and `HEAD`.

#### Scenario: A granted scraper reads metrics

- **WHEN** a node tagged `tag:prometheus` that holds the `read` capability
  requests `https://tsipam.example.ts.net/metrics`
- **THEN** the response is Prometheus text exposition

#### Scenario: An ungranted peer is refused

- **WHEN** a tailnet peer without the `read` capability requests `/metrics`
- **THEN** the response is 403 and contains no metric samples

#### Scenario: A scoped reader cannot scrape

- **WHEN** a caller whose `read` grant covers only alias `staging` requests
  `/metrics`
- **THEN** the response is 403, because one scrape covers every alias

### Requirement: Report drift health per target

For each drift target, the server SHALL export: drift rows by `kind` and
`state` from the latest complete report, the report's drift count, the
completion time of the last run and of the last successful run, whether the
last run succeeded, and the last run's duration. Before the first run
completes, it SHALL export the run-health series but no drift-row series.

#### Scenario: Drift rows follow the latest report

- **WHEN** the latest report has two `reserved_but_unadvertised` subnet pools
- **THEN** `tsipam_drift_rows{kind="subnet_pool",state="reserved_but_unadvertised"}`
  is 2 for that target

#### Scenario: A failed run is visible without hiding the last report

- **WHEN** the latest run failed after an earlier successful run
- **THEN** `tsipam_drift_last_run_success` is 0, the last-success timestamp
  still holds the earlier run's time, and the drift rows still reflect the
  earlier report

### Requirement: Report reservation counts and free site IDs

The server SHALL export, per registry tailnet, the reservation count for
each of `site_id`, `subnet_pool`, `service_address`, and `udp_port`, and the
number of unreserved 4via6 site IDs in 0–65535. These values SHALL reflect
the registry at scrape time.

#### Scenario: Counts follow a new reservation without a restart

- **WHEN** a site ID is reserved and `/metrics` is scraped afterwards
- **THEN** `tsipam_reservations{kind="site_id"}` has increased by one and
  `tsipam_site_ids_free` has decreased by one

### Requirement: Report root-pool utilization and free blocks

For every root pool (a reserved subnet pool not contained in another
reservation), the server SHALL export the fraction of its addresses covered
by contained reservations. For aliases with a completed drift run, it SHALL
also export free aligned blocks per configured block size, counting
reservations and that run's live routes as taken, as `subnet allocate` does.

#### Scenario: Nested reservations raise utilization

- **WHEN** `10.20.0.0/16` is a root pool with `10.20.0.0/17` reserved inside
  it for the same site
- **THEN** `tsipam_pool_utilization_ratio` for that pool is 0.5

#### Scenario: Free blocks match what allocation would see

- **WHEN** block size `/24` is configured and `subnet allocate` would find
  exactly three free `/24` blocks in a pool
- **THEN** `tsipam_pool_free_blocks{size="24"}` for that pool is 3

#### Scenario: Without live data there is no free-block series

- **WHEN** an alias is not a drift target
- **THEN** its root pools export utilization but no free-block series, so a
  registry-only count is never presented as allocatable space

#### Scenario: A pool too small for a size reports nothing for it

- **WHEN** a root pool is a `/26` and block size `/24` is configured
- **THEN** no free-block series is exported for that pool at size 24

### Requirement: Identity labels follow server sanitization

Metric labels that carry identities (tailnet alias, pool CIDR, site name)
SHALL hold real values when server sanitization is off. When it is
`identity` or `strict`, those labels SHALL hold keyed placeholders. If
sanitization is on but no sanitize key is set, per-pool series SHALL be
omitted and the server SHALL log a warning once at startup.

#### Scenario: Sanitized labels are stable across restarts

- **WHEN** the server runs with `TAILSCALE_SANITIZE=identity` and a fixed
  `TAILSCALE_SANITIZE_KEY`, and restarts
- **THEN** each pool's label value is the same placeholder before and after
  the restart, and no real CIDR appears in the exposition

#### Scenario: No key means no per-pool series

- **WHEN** the server runs with `TAILSCALE_SANITIZE=identity` and no key
- **THEN** the exposition contains no `tsipam_pool_*` series and stderr has
  one warning naming `TAILSCALE_SANITIZE_KEY`
