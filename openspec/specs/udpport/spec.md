# UDP Port Specification

## Purpose

Provides a comment-preserving registry for reserving per-device UDP listen
ports, scoped to a site/firewall-boundary label rather than to the whole
tailnet, with no metadata support. This capability has no reservation-time
tracking: it does not accept the owner/description/ticket/reserved-at flags
that site-id, subnet, and service reservations do.

## Requirements

### Requirement: Port and device-name normalization

A UDP port SHALL be accepted only as ASCII base-10 digits in the range
1024-65535 (privileged ports below 1024 are rejected); leading zeroes are
trimmed in the canonical form. A device name SHALL be accepted as any
non-empty, valid-UTF-8 string free of control characters after trimming
whitespace, with no hostname grammar imposed. The `--site` label follows the
same rule as a site name elsewhere in the registry.

#### Scenario: Privileged port is rejected

- **WHEN** an operator attempts to reserve port `1023` for a device
- **THEN** the operation fails as invalid input, naming the valid range
  `1024-65535`

#### Scenario: Padded port and trimmed labels are canonicalized

- **WHEN** an operator reserves port `041642` for device `" branch-fw01 "`
  in site `" branch "`
- **THEN** the reservation is stored and confirmed using the canonical
  spellings `41642`, `branch-fw01`, and `branch`

### Requirement: Port reservation is scoped per site, reusable across sites

`tail-ipam port reserve <port> <device> --site <site>` SHALL require
`--site`. Reserving the same port for a different device *within the same
site* SHALL be refused as a collision; the same port number MAY be reserved
independently in a different site. An exact repeat (same site, port, and
device) SHALL succeed as a no-op with `changed=false`.

#### Scenario: Same port collides only within one site

- **WHEN** port `41642` is reserved for `branch-fw01` in site `branch`, and
  an operator then reserves port `41642` for a different device in the same
  site `branch`
- **THEN** the second reservation is refused as a collision (CLI exit
  through a named `collision` `*goa.ServiceError`)

#### Scenario: Same port number is independent across sites

- **WHEN** port `41642` is reserved for `branch-fw01` in site `branch`
- **THEN** reserving port `41642` for `hq-fw01` in site `hq` succeeds without
  conflict

#### Scenario: Exact repeat is an idempotent no-op

- **WHEN** port `41642` is reserved for `branch-fw01` in site `branch`, and
  the identical reservation is repeated
- **THEN** the repeat reports `status: no_changes` and `changed: false`

#### Scenario: A collision redacts identities but leaves the port visible

- **WHEN** a UDP-port collision occurs between two devices in the same site,
  under `--sanitize identity`
- **THEN** stderr's collision message redacts the site and both device
  identities, while the port number remains visible (a port is not treated
  as identity)

### Requirement: `--site` is required for reserve and release, optional for list

`port reserve` and `port release` SHALL fail as a usage error when `--site`
is omitted. `port list` SHALL accept an optional `--site` filter: when
given, only that site's reservations are returned; when omitted, every
site's reservations are returned.

#### Scenario: Reserve without --site is a usage error

- **WHEN** an operator runs `port reserve 41642 fw` without `--site`
- **THEN** the command exits as a usage error before any registry access

#### Scenario: List filters by site when provided

- **WHEN** reservations exist in both `branch` and `hq`, and an operator runs
  `port list --site hq`
- **THEN** only `hq`'s reservations are returned; a site with no
  reservations returns an empty list rather than an error

### Requirement: Comment-preserving nested registry persistence

UDP-port reservations SHALL persist under the top-level `udpPorts` map,
nested as `udpPorts.<site>.<port> = <device>`. Edits SHALL preserve HuJSON
comments, formatting, and untouched entries; releasing a site's last port
SHALL keep that site's (now-empty) object rather than deleting it, so any
comment attached to the site key survives. A duplicate site or port key
(exact or canonically duplicate) SHALL be rejected.

#### Scenario: Releasing the last port in a site keeps the site object

- **WHEN** a site has exactly one UDP-port reservation and an operator
  releases it
- **THEN** the release succeeds, and the site's object remains present
  (empty) in the registry rather than being removed

### Requirement: Release best-effort repairs an undecodable registry

When `port release <port> --site <site>` cannot build any canonical view of
`udpPorts` at all, it SHALL NOT fail closed immediately. It SHALL search the
raw on-file site spellings that normalize to the canonical site, then within
each candidate site the raw port spellings that normalize to the canonical
port, remove each (site, port) candidate in memory, and keep only a removal
that makes `udpPorts` decode cleanly with that pair now absent. The file is
written at most once, and only for a verified candidate; when no candidate
repairs the map, release SHALL fail with an error and leave the file
byte-for-byte unchanged.

#### Scenario: An invalid device value at the target port is removed

- **WHEN** `udpPorts` fails to decode because the device value at the exact
  (site, port) an operator releases is an empty string (not a valid device
  name)
- **THEN** release removes that (site, port) entry, `udpPorts` now decodes
  cleanly without it, and the operation reports `changed=true`

### Requirement: Deterministic listing sorted by site then numeric port

`port list` SHALL return reservations sorted by site name, then by numeric
port value within each site. Human output SHALL contain one tab-separated
`site<TAB>port<TAB>device` line per reservation. JSON output SHALL carry a
`udp_ports` array in the versioned envelope, with an empty result
represented as an empty array.

#### Scenario: List sorts by site then numeric port

- **WHEN** ports `41650` and `1024` are reserved in site `hq`, and `41642`
  and `10240` in site `branch`
- **THEN** `port list` returns `branch/10240`, `branch/41642`, `hq/1024`,
  `hq/41650`, in that order — numeric port order, not lexical
