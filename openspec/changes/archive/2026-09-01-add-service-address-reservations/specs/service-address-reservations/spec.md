# Service Address Reservations Spec Delta

## Purpose

Provides a comment-preserving, tailnet-scoped ledger for reserving individual
IPv4 addresses for Tailscale Services before those services are created.

## ADDED Requirements

### Requirement: Service address and name normalization

The service-address capability SHALL accept an IPv4 host as either a bare
address or an IPv4 prefix whose length is exactly `/32`. Both spellings SHALL
canonicalize to the bare `netip` address spelling used in registry keys and
results. It SHALL reject wider prefixes without masking or truncating them,
and SHALL reject malformed addresses, native IPv6 addresses or prefixes, and
IPv4-mapped IPv6 spellings.

A service identity SHALL be accepted as either a bare non-empty name or the
`svc:<name>` selector spelling. Results and registry values SHALL use exactly
one `svc:` prefix. Normalization SHALL trim surrounding whitespace and reject
an empty name or control characters, but SHALL NOT contact the Tailscale
control plane or impose an unverified service-name grammar.

#### Scenario: Bare address and exact prefix are equivalent

- **WHEN** an operator reserves `100.100.100.7` for `svc:payments` and then
  repeats the reservation as `100.100.100.7/32` for `payments`
- **THEN** both operations resolve to address `100.100.100.7` and service
  `svc:payments`, and the second bare-metadata operation is an idempotent no-op

#### Scenario: Wider prefix is refused without truncation

- **WHEN** an operator attempts to reserve `100.100.100.7/24`
- **THEN** the invocation fails as invalid input, identifies that an IPv4 host
  or exact `/32` is required, writes nothing to stdout, and does not mutate the
  registry

#### Scenario: Non-IPv4 input is rejected

- **WHEN** an operator supplies native IPv6, IPv4-mapped IPv6, or malformed
  input as the service address
- **THEN** the operation fails as invalid input and does not mutate the
  registry

#### Scenario: Empty service selector is rejected

- **WHEN** an operator attempts to reserve an address for `svc:` or a
  whitespace-only service name
- **THEN** the operation fails as invalid input and does not mutate the
  registry

### Requirement: Explicit service-address ledger scope

Every reserve operation SHALL require the caller to choose the address. The
capability SHALL NOT declare parent service pools, select a next-free address,
calculate exhaustion, require membership in a subnet-pool reservation, or
cross-check live Tailscale state. Any syntactically valid IPv4 host SHALL be
eligible for explicit reservation regardless of the other registry maps.

#### Scenario: Reservation needs no parent pool

- **WHEN** an operator reserves a valid IPv4 host in a registry that contains
  no subnet pool covering that host
- **THEN** the reservation succeeds without creating a parent pool or
  reporting an exhaustion or membership error

### Requirement: Collision-safe and idempotent reservation

`tail-ipam service reserve <addr> <service-name>` and the corresponding API
operation SHALL create the tailnet registry when it is absent and reserve the
canonical address for the canonical service. An address already owned by a
different service SHALL be refused without overwriting the existing entry; the
CLI SHALL exit 1 and the HTTP operation SHALL return conflict status. Multiple
different addresses SHALL be allowed to belong to the same service.

An exact repeat with no metadata change SHALL succeed with `changed=false`.
Reservation metadata (`owner`, `description`, `ticket`, and `reserved_at`)
SHALL follow the existing registry merge and timestamp rules: supplied fields
update without clearing omitted fields, and adding ownership/tracking metadata
auto-stamps an absent reservation time.

#### Scenario: New service address is reserved

- **WHEN** an operator reserves `100.100.100.7` for `payments` in a missing
  tailnet registry
- **THEN** the registry is created, `100.100.100.7` is owned by
  `svc:payments`, the result reports `changed=true`, and the operation exits 0

#### Scenario: Different service cannot take the address

- **WHEN** `100.100.100.7` belongs to `svc:payments` and an operator attempts
  to reserve it for `svc:search`
- **THEN** the operation is refused as a collision, preserves the payments
  reservation byte-for-byte, emits no success payload on stdout, and reports
  failure through CLI exit 1 and HTTP conflict status

#### Scenario: One service can own multiple addresses

- **WHEN** `100.100.100.7` already belongs to `svc:payments` and the operator
  reserves `100.100.100.8` for the same service
- **THEN** both distinct addresses remain reserved for `svc:payments`

#### Scenario: Metadata-only update preserves omitted fields

- **WHEN** an existing service reservation has owner and ticket metadata and a
  repeat reserve supplies only a new description
- **THEN** the description is added, the stored owner and ticket remain
  unchanged, and the result reports `changed=true`

### Requirement: Comment-preserving registry persistence

Service reservations SHALL persist under the top-level `serviceAddresses`
map, logically keyed by canonical bare IPv4 address. New writes SHALL use the
bare spelling; loading SHALL also accept an existing exact `/32` key as an
alias while retaining its raw spelling for comment-preserving edits. A
metadata-free value SHALL be the canonical `svc:<name>` string; a value with
metadata SHALL be an object whose required `service` field carries that
identity and whose optional metadata fields use the established registry
spellings.

Edits SHALL preserve HuJSON comments, formatting, unknown top-level members,
and untouched entries. Duplicate entry keys, canonically duplicate address
keys, a near-miss or duplicate `serviceAddresses` top-level key, an invalid
address key, and an empty or malformed service value SHALL be rejected for
service operations. Validation failures SHALL remain scoped: malformed site-ID
or subnet-pool content SHALL NOT block a service-address operation, and a
malformed service-address map SHALL NOT block operations on the other maps.

#### Scenario: Comment on another entry survives an edit

- **WHEN** a hand-written comment is attached to one `serviceAddresses` entry
  and an operator reserves or updates a different entry
- **THEN** the original entry and its comment remain byte-for-byte unchanged

#### Scenario: Existing metadata-free document stays concise

- **WHEN** an operator reserves an address without metadata
- **THEN** its on-disk value is the bare canonical service string rather than
  an object with empty metadata fields

#### Scenario: Service map errors are isolated

- **WHEN** `subnetPools` is malformed but `serviceAddresses` is valid
- **THEN** service reserve, release, and list evaluate only their required map
  and remain available

### Requirement: Idempotent service-address release

`tail-ipam service release <addr>` and the corresponding API operation SHALL
remove the canonical address reservation when present and report
`changed=true`. Releasing an unreserved address from an existing registry
SHALL succeed as a no-op with `changed=false`. A missing registry file SHALL
remain an unreadable-registry error rather than being created by release.

#### Scenario: Existing reservation is released

- **WHEN** an operator releases a reserved address using either its bare or
  exact `/32` spelling
- **THEN** the canonical entry is removed, the result reports `changed=true`,
  and unrelated entries and comments remain unchanged

#### Scenario: Absent reservation is a no-op

- **WHEN** an operator releases a valid address that is not reserved in an
  existing registry
- **THEN** the operation exits 0, reports `changed=false`, and leaves the file
  unchanged

### Requirement: Deterministic service-address listing

`tail-ipam service list` and the corresponding API operation SHALL return all
service-address reservations sorted by numeric IPv4 address, independent of
registry member order. Human output SHALL contain one tab-separated
`address<TAB>service` line per reservation, followed by the established third
metadata column only when metadata exists. JSON output SHALL carry a
`service_addresses` array in the versioned envelope with `address`, `service`,
and optional metadata fields; an empty registry map SHALL be represented as an
empty array.

#### Scenario: Addresses sort numerically

- **WHEN** the registry stores `100.100.100.10`, `100.100.100.2`, and
  `100.100.100.1` in arbitrary member order
- **THEN** list returns them in `.1`, `.2`, `.10` order in both human and JSON
  formats

#### Scenario: Empty list is machine-distinguishable

- **WHEN** `serviceAddresses` is absent or empty in an existing registry
- **THEN** human output is empty and JSON output contains
  `"service_addresses": []`

### Requirement: API-first command path and discoverability

The goa service contract SHALL expose tailnet-scoped list, reserve, and release
operations at `/tailnet/{tailnet}/ipam/service-addresses`; address input for
release SHALL be carried without treating the optional `/32` slash as a path
separator. The CLI SHALL execute these operations through the generated HTTP
client and in-process loopback server, not by calling the registry directly.
The `service` command group and all three subcommands SHALL appear in
`tail-ipam -schema`.

#### Scenario: CLI reservation traverses the transport

- **WHEN** `tail-ipam service reserve 100.100.100.7 payments` runs in local
  mode
- **THEN** it uses the generated reserve operation over the loopback HTTP
  transport and returns the canonical API result

#### Scenario: Schema catalogs service commands

- **WHEN** `tail-ipam -schema` runs
- **THEN** the command catalog includes `service reserve`, `service release`,
  and `service list`

### Requirement: Sanitized and parse-clean output

Service addresses, service identities, owners, and the tailnet routing key
SHALL be treated as identity-bearing output and redacted consistently when
sanitization is enabled, including collision errors. Successful JSON output
SHALL remain a single parseable envelope on stdout; diagnostics and failures
SHALL go to stderr, and failed operations SHALL emit no partial success
payload.

#### Scenario: Sanitized collision leaks no identities

- **WHEN** a collision between two service identities is run with identity
  sanitization enabled
- **THEN** stderr preserves the collision explanation while exposing neither
  raw service identity nor the raw address, and stdout is empty

#### Scenario: JSON result stays parse-clean

- **WHEN** any successful service reserve, release, or list command runs with
  `--format json`
- **THEN** stdout contains exactly one parseable versioned JSON envelope and
  no diagnostics or banner text
