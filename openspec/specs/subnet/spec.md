# Subnet Specification

## Purpose

Provides a comment-preserving, tailnet-scoped registry for reserving subnet
CIDR pools to site names, with overlap detection across sites.

## Requirements

### Requirement: CIDR must be canonical, not silently masked

A subnet CIDR SHALL be accepted only in its exact canonical form (no host
bits set, standard textual spelling); unlike a site ID, a non-canonical
input is rejected rather than masked to its network address. The rejection
SHALL name the canonical form the operator should use instead.

#### Scenario: Non-canonical CIDR is rejected with a suggestion

- **WHEN** an operator attempts to reserve `10.0.0.1/24` (host bits set)
- **THEN** the operation fails as invalid input, and the error names
  `10.0.0.0/24` as the canonical form to use, without silently reserving
  the masked address

#### Scenario: The suggested canonical form is also redacted under sanitize

- **WHEN** a non-canonical CIDR is rejected under `--sanitize identity`
- **THEN** stderr's canonical-form suggestion is replaced with a CIDR
  placeholder, not left as the raw canonical address

### Requirement: Collision-safe reservation with cross-site overlap detection

`tail-ipam subnet reserve <cidr> <site-name>` SHALL create the registry when
absent and reserve the exact CIDR for the site. An exact-CIDR reservation
already owned by a different site SHALL be refused as a collision. A new
CIDR that overlaps any existing pool owned by a *different* site SHALL also
be refused as a collision, regardless of which CIDR is wider. Overlapping
CIDRs owned by the *same* site SHALL be allowed. An exact repeat with no
metadata change SHALL succeed with `changed=false`.

#### Scenario: New non-overlapping CIDR is reserved

- **WHEN** an operator reserves `10.0.0.0/24` for `hq-vpc` in a missing
  registry
- **THEN** the registry is created, `changed=true` is reported, and the
  command exits 0

#### Scenario: Overlapping CIDR for a different site is refused

- **WHEN** `10.0.0.0/16` already belongs to `hq-vpc` and an operator attempts
  to reserve the narrower `10.0.1.0/24` for `branch-office`
- **THEN** the operation is refused as a collision (a `*goa.ServiceError`
  named `collision`, CLI exit 1), and the existing `hq-vpc` reservation is
  unaffected

#### Scenario: Overlap within the same site is allowed

- **WHEN** `10.0.0.0/16` already belongs to `hq-vpc` and an operator reserves
  the narrower `10.0.1.0/24` also for `hq-vpc`
- **THEN** both reservations succeed and coexist

### Requirement: Comment-preserving registry persistence

Subnet-pool reservations SHALL persist under the top-level `subnetPools` map,
keyed by canonical CIDR. Edits SHALL preserve HuJSON comments, formatting,
unknown top-level members, and untouched entries. A duplicate or
canonically-duplicate CIDR key, a near-miss or duplicate `subnetPools`
top-level key, and an empty or null site value SHALL be rejected. A
malformed `subnetPools` map SHALL NOT block operations on the other registry
maps.

#### Scenario: Hand-written comment survives a second reservation

- **WHEN** a hand-written comment is attached to one `subnetPools` entry and
  an operator reserves a different, non-overlapping CIDR
- **THEN** the original entry and its comment remain byte-for-byte unchanged

### Requirement: Idempotent subnet-pool release

`tail-ipam subnet release <cidr>` SHALL remove the canonical CIDR's
reservation when present and report `changed=true`. Releasing an unreserved
CIDR from an existing registry SHALL succeed as a no-op with
`changed=false`. A missing registry file SHALL remain an unreadable-registry
error rather than being created by release.

#### Scenario: Reserve, list, then release round-trips

- **WHEN** an operator reserves `10.0.0.0/24` for `hq-vpc`, lists subnet
  pools, then releases `10.0.0.0/24`
- **THEN** list shows the reservation before release, and release reports
  `changed=true` and confirms the CIDR is released

### Requirement: Release best-effort repairs an undecodable registry

When `subnet release <cidr>` cannot build any canonical view of
`subnetPools` at all (for example, an overlap or decode failure), it SHALL
NOT fail closed immediately. It SHALL search the raw on-file `subnetPools`
keys for spellings that normalize to the canonical CIDR, remove each
candidate in memory, and keep only a removal that makes `subnetPools`
decode cleanly with that CIDR now absent; the file is written at most once,
and only for a verified candidate. When no candidate repairs the map,
release SHALL fail with an error and leave the file byte-for-byte unchanged.

#### Scenario: Removing the overlapping entry repairs the map

- **WHEN** `subnetPools` fails validation because two entries owned by
  different sites overlap, and an operator releases one of the overlapping
  CIDRs
- **THEN** release removes that entry, `subnetPools` now validates without
  the overlap, and the operation reports `changed=true`

### Requirement: Deterministic subnet-pool listing

`tail-ipam subnet list` SHALL return every subnet-pool reservation sorted by
network address, then by prefix length. Human output SHALL contain one
tab-separated `cidr<TAB>site` line per reservation, with metadata columns
appended when present. JSON output SHALL carry a `subnet_pools` array in the
versioned envelope, with an empty registry map represented as an empty array.

#### Scenario: List reflects a single reservation in JSON

- **WHEN** `10.0.0.0/24` is reserved for `hq-vpc` and `subnet list --format
  json` is run
- **THEN** the JSON envelope's `subnet_pools` array contains exactly one
  entry with `cidr: "10.0.0.0/24"` and `site: "hq-vpc"`
