# Server Drift Delta

## Purpose

Run the registry-versus-live comparison on a schedule inside `tsipam serve`
and make the latest result available read-only, so drift is visible without
anyone running `plan` or `check`.

## ADDED Requirements

### Requirement: Configure scheduled drift targets

`tsipam serve` SHALL accept a repeatable `--drift-tailnet <alias>` flag
naming a registry alias to compare with the server's live tailnet.
`--drift-interval` SHALL default to `15m` and accept `0` to disable. A
non-zero value below `1m`, or a target configured on a server with no live
credentials, SHALL be a startup error with exit 2.

#### Scenario: An alias is compared with the server's live tailnet

- **WHEN** `serve` runs with `TAILSCALE_TAILNET=example.com` and
  `--drift-tailnet production`
- **THEN** scheduled runs compare the `production` registry with live
  `example.com`

#### Scenario: Drift without live credentials is refused at startup

- **WHEN** `serve` starts with `--drift-tailnet production` and no
  Tailscale API credentials
- **THEN** it exits 2 before listening and names the missing configuration

#### Scenario: Too short an interval is refused

- **WHEN** `serve` starts with `--drift-interval 10s`
- **THEN** it exits 2 before listening and names the `1m` minimum on stderr

#### Scenario: No targets means no scheduled runs

- **WHEN** `serve` starts without any `--drift-tailnet`
- **THEN** no live API calls are made for drift
- **AND** the drift endpoint reports that drift is not configured for any
  requested tailnet

### Requirement: Scheduled runs use the plan comparison unchanged

Each scheduled run SHALL produce the same report `plan` would produce for the
same registry alias and live tailnet at that moment: the same entries, states,
ordering, drift count, and exclusions. A run SHALL NOT modify the registry or
the control plane. Runs for one target SHALL NOT overlap; a tick that arrives
while a run is in progress SHALL be skipped.

#### Scenario: Scheduled and on-demand reports agree

- **WHEN** a scheduled run completes and `plan` runs against the same
  registry and live state with no change in between
- **THEN** both reports have identical `entries`, `drift_count`, and
  `excluded`

#### Scenario: A slow run is not stacked

- **WHEN** a run is still collecting live state when the next interval
  elapses
- **THEN** no second run starts for that target until the first finishes

### Requirement: Retain the latest complete report and run status

The server SHALL run each target once at startup and then on every interval.
It SHALL retain, per target, the latest complete report and a run status:
`started_at`, `completed_at`, `outcome` (`ok`, `drift`, or `failed`), and a
failure message. A failed run SHALL NOT replace the last complete report and
SHALL NOT produce a partial one.

#### Scenario: A failed run keeps the previous report visible

- **WHEN** a run completes with drift and the next run fails because the
  live API is unreachable
- **THEN** the endpoint returns the earlier report together with a run
  status whose outcome is `failed` and whose timestamps are the failed run's

#### Scenario: Restart before the first run completes

- **WHEN** the drift endpoint is queried after a restart but before the
  first run for that target has completed
- **THEN** the request fails as drift unavailable, and no empty or clean
  report is returned

### Requirement: Read the latest drift report remotely

A read-only API method SHALL return the latest report for a requested
registry alias in the existing comparison envelope plus a `run` object
carrying the run status. It SHALL require the caller to hold the `read`
capability. `tsipam drift` SHALL call it and SHALL require `--server`.

#### Scenario: An authorized reader fetches the report

- **WHEN** a caller with the `read` capability runs
  `tsipam drift --server https://tsipam.example.ts.net --tailnet production`
- **THEN** stdout holds the comparison envelope with `entries`,
  `drift_count`, `excluded`, `live_tailnet`, and a `run` object

#### Scenario: The command refuses to run locally

- **WHEN** `tsipam drift` runs without `--server`
- **THEN** it exits 2 with a usage error explaining that scheduled drift
  exists only on a server

#### Scenario: A caller without the read capability is refused

- **WHEN** a tailnet peer without the `read` capability calls the method
- **THEN** the request is refused and no report data is returned

### Requirement: Drift command exit codes

`tsipam drift` SHALL exit 0 whenever it prints a report, including a report
with drift or an outcome of `failed`. It SHALL exit 2 when no report is
available, the target is not configured, the server is unreachable, or the
caller is refused.

#### Scenario: Drift present is still a successful read

- **WHEN** the latest report has a non-zero `drift_count`
- **THEN** `tsipam drift` prints it and exits 0

#### Scenario: An unconfigured alias is an error

- **WHEN** `tsipam drift --tailnet staging` runs and `staging` is not a
  drift target
- **THEN** it exits 2 with empty stdout

### Requirement: Disclose that runs are consecutive observations

The `run` object and the drift documentation SHALL make clear that each run
reads devices and then Services as two consecutive observations. A change
between those reads can appear as drift in a single run. Documentation SHALL
recommend alerting only on drift that persists across consecutive runs.

#### Scenario: Documentation guides alerting on persistent drift

- **WHEN** an operator reads the monitoring how-to
- **THEN** it explains the consecutive-read window and shows an alert rule
  that requires drift to persist for at least two intervals
