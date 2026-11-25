# Registry History Delta

## Purpose

Keeps an append-only audit record of every change made to a SQLite-backed
registry, and lets operators read it with `tsipam registry history`, so a
shared registry answers who changed what, and when, without git history.

## ADDED Requirements

### Requirement: Record one event per registry mutation

In a SQLite registry, every command that changes stored state SHALL record
exactly one event per changed reservation, in the same transaction as the
change. A command that changes nothing (`changed=false`) or fails SHALL
record no event.

#### Scenario: Reserve records an event

- **WHEN** `tsipam site-id reserve 7 hq-vpc --ticket OPS-1 --registry sqlite://./tsipam.db`
  succeeds with `changed=true`
- **THEN** the history holds one new `reserve` event for site ID `7` naming
  `hq-vpc` and ticket `OPS-1`

#### Scenario: No-op and refusal record nothing

- **GIVEN** site ID `7` reserved for `hq-vpc`
- **WHEN** `site-id reserve 7 hq-vpc` repeats with no metadata, and then
  `site-id reserve 7 branch-office` is refused
- **THEN** the history gains no events

### Requirement: Event content

Each event SHALL carry its time (RFC 3339, UTC), the tailnet key, an action
(`reserve`, `allocate`, `release`, or `import`), the map kind (`site_id`,
`subnet_pool`, `service_address`, or `udp_port`), the canonical key (plus the
site for UDP ports), the stored value before and after the change, and an
actor.

#### Scenario: Metadata amendment shows before and after

- **GIVEN** subnet pool `10.20.0.0/24` reserved for `hq-vpc` with ticket
  `OPS-1`
- **WHEN** `tsipam subnet reserve 10.20.0.0/24 hq-vpc --ticket OPS-2` runs
- **THEN** the new event's before value has ticket `OPS-1` and its after
  value has ticket `OPS-2`

#### Scenario: Release keeps the released value

- **WHEN** `tsipam service release 100.100.100.8` removes a reservation for
  `svc:dashboard`
- **THEN** the `release` event's before value names `svc:dashboard` and its
  after value is empty

### Requirement: Local commands record no actor

A command that runs against a local registry SHALL record the event actor as
absent. tsipam SHALL NOT fill the actor from local, unauthenticated sources
such as the OS user name or environment variables.

#### Scenario: Local reserve has no actor

- **WHEN** a local `tsipam site-id reserve` records an event
- **THEN** that event's actor is `null` in JSON history output and `-` in
  human output

### Requirement: History is append-only

tsipam SHALL provide no way to edit or delete history events. Releasing a
reservation SHALL NOT remove earlier events about it, and events SHALL be
kept for as long as the database exists.

#### Scenario: History survives release

- **GIVEN** a reserve and a later release of site ID `7`
- **WHEN** `tsipam registry history --key 7` runs
- **THEN** both events are listed

### Requirement: List history with `registry history`

`tsipam registry history` SHALL list the selected tailnet's events, newest
first, limited to `--limit` events (default 100, minimum 1). It SHALL accept
`--kind`, `--key`, and `--since <RFC 3339 time>` filters, the registry
selector flags, and `--tailnet`. Human output SHALL be one tab-separated line
per event; JSON output SHALL carry an `events` array, empty rather than
absent when nothing matches.

#### Scenario: Filtered JSON history

- **WHEN** `tsipam registry history --kind subnet_pool --since 2026-01-01T00:00:00Z
  --format json --registry sqlite://./tsipam.db` runs
- **THEN** stdout is one JSON envelope with `command` `registry history` and
  an `events` array holding only subnet-pool events at or after that time,
  newest first

#### Scenario: Invalid filter

- **WHEN** `tsipam registry history --kind vlan` or `--since yesterday` runs
- **THEN** the command exits 2 with nothing on stdout

### Requirement: History requires the SQLite backend

On the HuJSON file backend, `tsipam registry history` SHALL exit 2 with
nothing on stdout and an error stating that history requires the sqlite
backend and that the file registry's history lives in version control.

#### Scenario: File backend

- **WHEN** `tsipam registry history --registry-dir .` runs
- **THEN** the command exits 2, stdout is empty, and stderr says history
  requires the sqlite backend

### Requirement: History output is sanitized

When sanitization is active, history output SHALL redact site names, service
names, device names, owners, CIDRs, addresses, actors, and the tailnet key
under the same rules as the list commands for the same values.

#### Scenario: Owners are redacted

- **GIVEN** `TAILSCALE_SANITIZE=identity` and an event whose after value has
  owner `alice@example.com`
- **WHEN** `tsipam registry history --format json` runs
- **THEN** stdout does not contain `alice@example.com`
