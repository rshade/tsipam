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

`tsipam subnet reserve <cidr> <site-name>` SHALL create the registry when
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

`tsipam subnet release <cidr>` SHALL remove the canonical CIDR's
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

`tsipam subnet list` SHALL return every subnet-pool reservation sorted by
network address, then by prefix length. Human output SHALL contain one
tab-separated `cidr<TAB>site` line per reservation, with metadata columns
appended when present. JSON output SHALL carry a `subnet_pools` array in the
versioned envelope, with an empty registry map represented as an empty array.

#### Scenario: List reflects a single reservation in JSON

- **WHEN** `10.0.0.0/24` is reserved for `hq-vpc` and `subnet list --format
  json` is run
- **THEN** the JSON envelope's `subnet_pools` array contains exactly one
  entry with `cidr: "10.0.0.0/24"` and `site: "hq-vpc"`

### Requirement: Atomic next-free allocation under the registry lock

`tsipam subnet allocate <pool-cidr> --size <prefix> <site-name>` SHALL
reserve the first available child CIDR of the requested prefix length
within the canonical pool for the site, creating the registry when absent.
The pick and the write SHALL happen inside one locked read-decide-write
transaction against the registry file, so that concurrent allocations from
the same pool never receive the same block. A candidate SHALL be counted as
taken when it overlaps any same-family subnet-pool reservation for the
selected registry tailnet, regardless of owning site, or any same-family
route advertised by a live device; other-family and outside-pool prefixes
SHALL be ignored, and IPv4-mapped pools SHALL normalize exactly as
`calc next-free` normalizes them. A live device read SHALL be required; the
command SHALL refuse to allocate without one. The reservation SHALL carry
the same optional metadata (`--owner`, `--description`, `--ticket`,
`--reserved-at`) and auto-stamping rules as `subnet reserve`, and SHALL
accept the same `--registry-dir` and `--tailnet` selectors. A successful
allocation SHALL report the reserved CIDR and site with `changed=true` and
exit 0, in human form and in the versioned JSON envelope, through the same
sanitization as `subnet reserve`.

#### Scenario: Two concurrent allocations receive distinct blocks

- **WHEN** two `subnet allocate 10.0.0.0/16 --size 24 <site>` invocations
  run concurrently against the same registry directory
- **THEN** both exit 0 and the two reported CIDRs are distinct `/24`
  children of `10.0.0.0/16`, and both appear in `subnet list`

#### Scenario: A live-advertised route is never allocated

- **WHEN** the pool's first child is reserved in the registry and its second
  child is advertised by a live device, and an operator allocates one child
- **THEN** the third child is reserved, and neither the registry-reserved
  nor the live-advertised child is returned

#### Scenario: Allocation refuses to run without a live read

- **WHEN** the device collector is unavailable
- **THEN** the command fails before touching the registry, and no
  reservation is written

#### Scenario: Metadata is recorded on the allocated block

- **WHEN** an operator allocates with `--owner alice --ticket OPS-1234`
- **THEN** the new reservation stores owner `alice`, ticket `OPS-1234`, and
  an auto-stamped `reserved_at`, exactly as `subnet reserve` would

#### Scenario: Hand-written comments survive an allocation

- **WHEN** a hand-written comment is attached to an existing `subnetPools`
  entry and an operator allocates a new block from a pool
- **THEN** the original entry and its comment remain byte-for-byte unchanged

### Requirement: Ticket-keyed idempotent replay

When `--ticket` is supplied and a subnet-pool reservation already exists
inside the requested pool for the same site and the same ticket, `subnet
allocate` SHALL report that existing reservation with `changed=false`, exit
0, and leave the registry file byte-for-byte unchanged, without picking a
new block and without merging the other supplied metadata into the existing
entry. When more than one reservation matches, the lowest by network
address, then prefix length, SHALL be reported. Without `--ticket`, every
run SHALL allocate a fresh block. A matching ticket owned by a different
site, or outside the pool, SHALL NOT count as a replay.

#### Scenario: Repeat with the same ticket returns the existing block

- **WHEN** `subnet allocate 10.0.0.0/16 --size 24 hq-vpc --ticket OPS-1`
  runs twice against the same registry
- **THEN** the second run reports the CIDR the first run reserved with
  `changed=false` and exit 0, and the registry bytes are unchanged

#### Scenario: Repeat without a ticket allocates again

- **WHEN** `subnet allocate 10.0.0.0/16 --size 24 hq-vpc` runs twice
- **THEN** the second run reserves a different `/24` with `changed=true`

#### Scenario: The same ticket under another site is not a replay

- **WHEN** `10.0.1.0/24` is reserved for `hq-vpc` with ticket `OPS-1`, and
  an operator allocates from `10.0.0.0/16` for `branch` with ticket `OPS-1`
- **THEN** a fresh block is reserved for `branch` rather than `10.0.1.0/24`
  being reported

### Requirement: Allocation exhaustion and invalid target sizes

When no child of the requested size is available, `subnet allocate` SHALL
exit 2, write an exhaustion message naming the block size and the pool to
stderr, write nothing to stdout in either output format, and write no
reservation (an absent registry file is still created empty, as `subnet
reserve` creates it). Target widths shorter than the canonical pool or greater
than the address-family width SHALL be usage errors. Configured
sanitization SHALL replace the pool in the exhaustion message.

#### Scenario: Exhausted pool exits 2 with an empty stdout

- **WHEN** reservations and live routes cover every requested child of the
  pool
- **THEN** the command exits 2 in both human and JSON formats, stdout is
  empty, stderr names the missing block size and the pool, and no
  reservation is written

#### Scenario: Target width outside the pool is a usage error

- **WHEN** an operator runs `subnet allocate 10.0.0.0/24 --size 16 hq-vpc`
- **THEN** the command rejects the target with a usage error before reading
  devices or the registry

#### Scenario: Sanitized exhaustion message hides the pool

- **WHEN** an exhausted allocation runs under `--sanitize identity`
- **THEN** stderr's exhaustion message carries a CIDR placeholder in place
  of the pool
