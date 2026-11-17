# Server UI Delta

## Purpose

Give tailnet members a read-only web view of the shared registry served by
`tsipam serve`, covering reservations, pool usage, drift, and history, without
any client tooling.

## ADDED Requirements

### Requirement: Read-only pages behind the read capability

`tsipam serve` SHALL serve HTML pages at `/` and below on the API's tailnet
listener. Each page SHALL require the `read` action for the alias it shows,
and SHALL show only aliases, sites, and services the caller's grant covers.
A refused caller SHALL get a 403 page with no registry data. Pages SHALL
accept only `GET` and `HEAD`.

#### Scenario: A member with read access opens the overview

- **WHEN** a user holding the `read` capability opens
  `https://tsipam.example.ts.net/`
- **THEN** the page lists each registry tailnet with its reservation counts
  and drift status

#### Scenario: A scoped reader sees only granted aliases

- **WHEN** a user whose `read` grant covers only alias `staging` opens `/`
- **THEN** only `staging` is listed, and opening another alias's page
  returns 403

#### Scenario: A write method is refused

- **WHEN** any client sends `POST` to a UI page
- **THEN** the response is 405 and the registry is unchanged

### Requirement: Search reservations across all kinds

A reservation page per registry tailnet SHALL list site IDs, subnet pools,
service addresses, and UDP ports. It SHALL filter by kind, site or service,
owner, and ticket, and by free text matching the key or label. Filters SHALL
be URL query parameters, so a filtered view can be bookmarked and shared.
Rows SHALL use the same ordering as the matching `list` command.

#### Scenario: Filter by owner

- **WHEN** a reader opens the reservations page with `?owner=platform-team`
- **THEN** only reservations whose owner is `platform-team` are shown, in
  list-command order

#### Scenario: Search finds a CIDR inside a pool

- **WHEN** a reader searches for `10.20.5.0`
- **THEN** every subnet pool containing that address is shown

### Requirement: Show pool usage

A pools page SHALL show each root pool with a usage bar and the same
utilization and free-block figures `/metrics` exports, followed by the
reservations nested inside it.

#### Scenario: Usage bar matches the metric

- **WHEN** `tsipam_pool_utilization_ratio` for a pool is 0.5
- **THEN** that pool's bar on the pools page shows 50%

### Requirement: Show the latest drift report and history

A drift page SHALL show the latest scheduled report with its run status,
including a visible stale marker when the last run failed. A history page
SHALL list registry change events newest first, with paging and the same
fields as `registry history`.

#### Scenario: A failed run is marked stale

- **WHEN** the last run failed after an earlier successful run
- **THEN** the drift page shows the earlier report under a notice naming
  the failed run's time and its failure message

#### Scenario: History pages through events

- **WHEN** a tailnet has more events than one page holds
- **THEN** the history page shows the newest page first and links to older
  pages

### Requirement: Pages work without scripts or external assets

Pages SHALL be fully usable with JavaScript disabled, SHALL load no
third-party assets, and SHALL send a Content-Security-Policy that forbids
scripts and allows styles only from the server itself. They SHALL also
forbid framing.

#### Scenario: Security headers are present

- **WHEN** any UI page is fetched
- **THEN** the response carries a Content-Security-Policy with
  `script-src 'none'` and a header that forbids framing

### Requirement: Rendered identities follow server sanitization

UI pages SHALL show real values when server sanitization is off. When it is
`identity` or `strict`, they SHALL replace the same identity values the CLI
replaces at that level, and SHALL keep visible what the CLI keeps visible.

#### Scenario: Sanitized UI hides CIDRs but keeps tickets

- **WHEN** the server runs with `TAILSCALE_SANITIZE=identity`
- **THEN** pool CIDRs, site names, and owners appear as placeholders while
  tickets, descriptions, and numeric site IDs remain visible
