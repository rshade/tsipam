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

Invalid input SHALL fail before any registry mutation.

#### Scenario: Privileged port is rejected

- **WHEN** an operator attempts to reserve port `1023` for a device
- **THEN** the operation fails as invalid input, naming the valid range
  `1024-65535`

#### Scenario: Padded port and trimmed labels are canonicalized

- **WHEN** an operator reserves port `041642` for device `" branch-fw01 "`
  in site `" branch "`
- **THEN** the reservation is stored and confirmed using the canonical
  spellings `41642`, `branch-fw01`, and `branch`

#### Scenario: Boundary ports are accepted and canonicalized

- **WHEN** an operator reserves ports `01024` and `65535` with valid site and
  device labels
- **THEN** the reservations succeed with canonical ports `1024` and `65535`

#### Scenario: Invalid port is rejected without a write

- **WHEN** an operator supplies a port below 1024, above 65535, negative,
  fractional, or non-numeric
- **THEN** the operation fails as invalid input and leaves the registry
  byte-for-byte unchanged
- **AND** out-of-range decimal input identifies the valid 1024-65535 range,
  while negative, fractional, or non-numeric input identifies invalid
  decimal syntax

#### Scenario: Empty or unsafe labels are rejected

- **WHEN** a site or device label is empty after trimming or contains a
  control character
- **THEN** the operation fails as invalid input before any registry write

### Requirement: Port reservation is scoped per site, reusable across sites

`tsipam port reserve <port> <device> --site <site>` and the corresponding
API operation SHALL explicitly reserve the canonical port for the canonical
device label within the canonical site label. Reserve SHALL create the
tailnet registry when it is absent. Every reserve SHALL require the caller to
choose the port; the capability SHALL NOT select a next-free port.

An exact repeat of a `(site, port, device)` tuple SHALL succeed as an
idempotent no-op and report `changed=false`. A port already owned by another
device in the same site SHALL be refused without overwriting the existing
entry, with CLI exit status 1 and HTTP conflict status. The same port SHALL be
allowed in different sites, and neither site SHALL need a corresponding
site-ID reservation.

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

#### Scenario: New reservation creates a missing registry

- **WHEN** an operator reserves port `41642` for `branch-fw01` in site
  `branch-office` and the tailnet registry does not exist
- **THEN** the registry is created, the tuple is reserved, the operation
  reports `changed=true`, and the CLI exits 0

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

UDP-port reservations SHALL persist below the exact top-level `udpPorts` key
as a two-level map from canonical site label to canonical decimal port and
then to canonical device label. A metadata-free reservation SHALL use a
string device value. Reserve and release SHALL perform their complete
read-decide-write transaction under the registry's existing cross-process
lock and SHALL write through the comment-preserving HuJSON edit path.

Edits SHALL preserve comments, whitespace, unknown top-level members,
unrelated registry maps, sibling sites, and sibling ports. The editor SHALL
distinguish an absent, null, empty, and populated `udpPorts` member and shall
make the same distinction for the selected site's child object so adding a
site or port never replaces an existing sibling or discards an attached
comment. Releasing the last port in a site SHALL leave the now-empty site
object in place rather than remove its node and comments.

Loading UDP-port state SHALL reject a case-insensitive near-miss or duplicate
`udpPorts` top-level key, duplicate or canonically duplicate site members,
duplicate or canonically duplicate port members within a site, site values
that are neither objects nor null, invalid stored site/port labels, and empty
or invalid stored device labels. Validation failures SHALL remain scoped to
UDP-port operations; operations on other valid maps SHALL remain available,
and errors in other maps SHALL not block UDP-port operations.
Reserve and list SHALL reject malformed
UDP-port state; release SHALL follow the separate release-repair requirement
when no canonical view can be built.

#### Scenario: Releasing the last port in a site keeps the site object

- **WHEN** a site has exactly one UDP-port reservation and an operator
  releases it
- **THEN** the release succeeds, and the site's object remains present
  (empty) in the registry rather than being removed

#### Scenario: Adding a second site preserves the first site

- **WHEN** a populated `udpPorts` map contains a commented reservation in
  `branch-office` and an operator reserves a port in a new `hq-vpc` site
- **THEN** the new site is added without changing the first site's entry or
  its comment

#### Scenario: Reserving into empty and null nodes is safe

- **WHEN** `udpPorts` or the selected site is present as an empty object or as
  null and an operator makes a valid reservation
- **THEN** only the required object or member is installed, while comments
  and unrelated siblings remain unchanged

#### Scenario: Validation errors remain map-scoped

- **WHEN** another registry map is malformed but `udpPorts` is valid
- **THEN** UDP-port reserve, release, and list remain available

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

An empty result SHALL produce no human output. Listing an unknown valid site
SHALL produce an empty result, and listing a missing registry file SHALL fail
as an unreadable-registry error.

#### Scenario: List sorts by site then numeric port

- **WHEN** ports `41650` and `1024` are reserved in site `hq`, and `41642`
  and `10240` in site `branch`
- **THEN** `port list` returns `branch/10240`, `branch/41642`, `hq/1024`,
  `hq/41650`, in that order — numeric port order, not lexical

#### Scenario: Listing does not create a registry

- **WHEN** an operator lists ports and the selected tailnet registry is missing
- **THEN** the operation fails as an unreadable-registry error, stdout is
  empty, and no registry is created

### Requirement: Idempotent per-site release

`tsipam port release <port> --site <site>` and the corresponding API
operation SHALL remove only the canonical `(site, port)` reservation and
report `changed=true` when present. Releasing an unreserved port or an unknown
site from an existing valid registry SHALL succeed as a no-op with
`changed=false` and SHALL not rewrite the file. A missing registry file SHALL
remain an unreadable-registry error and SHALL not be created by release.

#### Scenario: Existing reservation is released

- **WHEN** an operator releases port `41642` from `branch-office`
- **THEN** that reservation is removed, `changed=true` is reported, and
  reservations in other ports and sites remain unchanged

#### Scenario: Absent site-port pair is a no-op

- **WHEN** an operator releases a valid `(site, port)` pair that is not
  reserved in an existing registry
- **THEN** the operation exits 0, reports `changed=false`, and leaves the file
  byte-for-byte unchanged

#### Scenario: Release does not create a registry

- **WHEN** an operator releases a port and the selected tailnet registry does
  not exist
- **THEN** the operation fails as an unreadable-registry error and no file is
  created

### Requirement: API-first command path and discoverability

The goa service contract SHALL expose tailnet-scoped list, reserve, and
release operations for UDP-port reservations, including an optional site
filter for list and required site inputs for reserve and release. Invalid
caller input SHALL map to HTTP bad request and collisions SHALL map to HTTP
conflict. The CLI SHALL execute all three operations through the generated
HTTP client and in-process loopback server rather than calling the registry
directly.

The `port` command group and its `reserve`, `release`, and `list` subcommands,
including the required or optional `--site` flags, SHALL appear in help and in
`tsipam -schema`.

#### Scenario: CLI reservation traverses the transport

- **WHEN** `tsipam port reserve 41642 branch-fw01 --site branch-office`
  runs in local mode
- **THEN** it invokes the generated reserve operation over the loopback HTTP
  transport and returns the canonical service result

#### Scenario: Schema catalogs the command group

- **WHEN** `tsipam -schema` runs
- **THEN** the command catalog includes `port reserve`, `port release`, and
  `port list` with their site-flag requirements

### Requirement: Identity-safe and stream-safe output

Site and device labels and the tailnet routing key SHALL be treated as
identity-bearing output and redacted consistently when sanitization is
enabled, including stored-state failures and collision errors. Port numbers
SHALL remain visible because they are not identity values. Successful command
results SHALL go to stdout, diagnostics and errors SHALL go to stderr, and a
failed operation SHALL emit no partial success payload.

The existing global JSON mode SHALL emit one parseable versioned envelope.
Reserve results SHALL carry canonical `site`, `port`, and `device` fields plus
`changed`; release results SHALL carry canonical `site` and `port` plus
`changed`; list results SHALL carry a `udp_ports` array whose entries contain
canonical `site`, `port`, and `device` fields, with an empty list represented
as `"udp_ports": []`.

#### Scenario: Sanitized collision leaks no labels

- **WHEN** two device labels collide on a site and identity sanitization is
  enabled
- **THEN** stderr preserves the collision explanation and port number while
  exposing neither raw site nor either raw device label, and stdout is empty

#### Scenario: Failure keeps stdout clean

- **WHEN** reserve, release, or list fails validation, collision, or registry
  loading
- **THEN** stdout is empty and the complete diagnostic is written to stderr

#### Scenario: JSON list is parse-clean and structurally empty

- **WHEN** a successful filtered list has no matching reservations and
  `--format json` is selected
- **THEN** stdout contains exactly one parseable versioned envelope with
  `"udp_ports": []`
