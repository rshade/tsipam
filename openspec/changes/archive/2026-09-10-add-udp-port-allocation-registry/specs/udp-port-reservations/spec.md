# UDP Port Reservations Specification

## Purpose

Provides a tailnet-scoped, comment-preserving ledger of explicit per-device
UDP listen-port assignments whose collision boundary is a named site or
shared firewall.

## ADDED Requirements

### Requirement: UDP-port reservation input normalization

The capability SHALL accept a port only as a base-10 integer in the inclusive
range 1024-65535 and SHALL return and persist its canonical decimal spelling.
It SHALL reject empty, non-numeric, signed, fractional, and out-of-range port
input before mutating the registry.

Site and device labels SHALL be trimmed, SHALL be non-empty after trimming,
and SHALL reject control characters. Site labels SHALL otherwise use the same
free-form validation as other site-bearing registry primitives; device labels
SHALL NOT be required to match live Tailscale state or a narrower hostname
grammar.

#### Scenario: Boundary ports are accepted and canonicalized

- **WHEN** an operator reserves ports `01024` and `65535` with valid site and
  device labels
- **THEN** the reservations succeed with canonical ports `1024` and `65535`

#### Scenario: Invalid port is rejected without a write

- **WHEN** an operator supplies a port below 1024, above 65535, negative,
  fractional, or non-numeric
- **THEN** the operation fails as invalid input, identifies the 1024-65535
  range, and leaves the registry byte-for-byte unchanged

#### Scenario: Empty or unsafe labels are rejected

- **WHEN** a site or device label is empty after trimming or contains a
  control character
- **THEN** the operation fails as invalid input before any registry write

### Requirement: Explicit per-site reservation and collision semantics

`tail-ipam port reserve <port> <device> --site <site>` and the corresponding
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

#### Scenario: New reservation creates a missing registry

- **WHEN** an operator reserves port `41642` for `branch-fw01` in site
  `branch-office` and the tailnet registry does not exist
- **THEN** the registry is created, the tuple is reserved, the operation
  reports `changed=true`, and the CLI exits 0

#### Scenario: Exact repeat is idempotent

- **WHEN** `(branch-office, 41642, branch-fw01)` is already reserved and the
  operator reserves that exact tuple again
- **THEN** the operation reports `changed=false`, exits 0, and leaves the
  registry byte-for-byte unchanged

#### Scenario: Same-site collision is refused

- **WHEN** port `41642` in `branch-office` belongs to `branch-fw01` and an
  operator attempts to reserve it for `branch-fw02`
- **THEN** the operation reports a collision, exits 1 at the CLI or returns
  HTTP conflict through the API, and preserves the existing reservation

#### Scenario: Cross-site port reuse succeeds

- **WHEN** port `41642` is reserved in `branch-office` and an operator
  reserves port `41642` for another device in `hq-vpc`
- **THEN** both reservations coexist without collision

### Requirement: Nested comment-preserving registry persistence

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
or invalid stored device labels. A UDP-port-map error SHALL block UDP-port
operations without blocking operations on other valid registry maps, and an
error in another registry map SHALL not block UDP-port operations.

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

### Requirement: Idempotent per-site release

`tail-ipam port release <port> --site <site>` and the corresponding API
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

### Requirement: Deterministic and optionally filtered listing

`tail-ipam port list [--site <site>]` and the corresponding API operation
SHALL return all UDP-port reservations when no site is selected and only the
selected site's reservations when a site is selected. Results SHALL sort by
site label and then by numeric port; filtering SHALL retain the same output
shape and numeric port ordering. An unknown valid site SHALL produce an empty
result. Listing a missing registry file SHALL be an unreadable-registry error.

Human output SHALL contain one tab-separated
`site<TAB>port<TAB>device` line per reservation. An empty result SHALL produce
no human output.

#### Scenario: All sites and ports sort deterministically

- **WHEN** reservations for sites `hq-vpc` and `branch-office` contain ports
  `41650`, `41642`, and `1024` in arbitrary member order
- **THEN** list returns sites lexically and ports within each site in numeric
  order, independent of file order

#### Scenario: Site filter returns only one collision domain

- **WHEN** multiple sites contain reservations and an operator lists with
  `--site branch-office`
- **THEN** only `branch-office` reservations are returned, numerically sorted
  by port

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
`tail-ipam -schema`.

#### Scenario: CLI reservation traverses the transport

- **WHEN** `tail-ipam port reserve 41642 branch-fw01 --site branch-office`
  runs in local mode
- **THEN** it invokes the generated reserve operation over the loopback HTTP
  transport and returns the canonical service result

#### Scenario: Schema catalogs the command group

- **WHEN** `tail-ipam -schema` runs
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
