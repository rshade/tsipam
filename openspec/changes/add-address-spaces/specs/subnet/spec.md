# Subnet Specification Delta

## MODIFIED Requirements

### Requirement: Collision-safe reservation with cross-site overlap detection

`tsipam subnet reserve <cidr> <site-name>` SHALL create the registry when
absent and reserve the exact CIDR for the site. An exact-CIDR reservation
already owned by a different site SHALL be refused as a collision. A new
CIDR that overlaps any pool in its space owned by a *different* site SHALL also
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

### Requirement: Atomic next-free allocation under the registry lock

`tsipam subnet allocate <pool-cidr> --size <prefix> <site-name>` SHALL
reserve the first available child CIDR of the requested prefix length
within the canonical pool for the site, creating the registry when absent.
The pick and the write SHALL happen inside one locked read-decide-write
transaction against the registry file, so that concurrent allocations from
the same pool never receive the same block. A candidate SHALL be counted as
taken when it overlaps any same-family subnet-pool reservation in the
selected space of the selected registry tailnet, regardless of owning site or
usage, or any same-family route advertised by a live device; other-family and outside-pool prefixes
SHALL be ignored, and IPv4-mapped pools SHALL normalize exactly as
`calc next-free` normalizes them. In a mapped space the live routes SHALL be
the IPv4 prefixes decoded from via routes that carry the space's site ID. A
live device read SHALL be required for the default space and for a mapped
space; the command SHALL refuse to allocate there without one. A space that is
compared with no live state SHALL need no live read. The reservation SHALL carry
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

#### Scenario: A via route blocks only its own space

- **WHEN** site ID 3 maps `Boston`, a live device advertises the via route for
  site ID 3 and `10.0.0.0/24`, and an operator allocates a `/24` from
  `10.0.0.0/16` in `Boston`, then in an unmapped space `Lab`
- **THEN** `Boston` receives `10.0.1.0/24` and `Lab` receives `10.0.0.0/24`

#### Scenario: An unmapped space allocates offline

- **WHEN** `Lab` is not mapped, no Tailscale credentials are set, and
  `subnet allocate 10.0.0.0/16 --size 24 lab --space Lab` runs
- **THEN** it reserves the first free child and exits 0

#### Scenario: A mapped space refuses without a live read

- **WHEN** `Boston` is mapped and the live read is unavailable or incomplete
- **THEN** the command exits 2 and no reservation is written

#### Scenario: A space mapped during the request

- **WHEN** an allocation began while `Lab` was unmapped, without a live read,
  and `Lab` is mapped before the registry transaction starts
- **THEN** the command exits 2 and no reservation is written

### Requirement: Ticket-keyed idempotent replay

When `--ticket` is supplied and a subnet-pool reservation already exists
inside the requested pool for the same site and the same ticket, `subnet
allocate` SHALL report that existing reservation with `changed=false`, exit
0, and leave the registry file byte-for-byte unchanged, without picking a
new block and without merging the other supplied metadata into the existing
entry. When more than one reservation matches, the lowest by network
address, then prefix length, SHALL be reported. Without `--ticket`, every
run SHALL allocate a fresh block. A matching ticket owned by a different
site, outside the pool, or in another space SHALL NOT count as a replay.

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

#### Scenario: A ticket in another space is not a replay

- **WHEN** ticket `OPS-1` allocated a block for `lab` in `Boston` and the same
  allocation runs in `Chicago`
- **THEN** a block is reserved in `Chicago` with `changed=true`

### Requirement: Deterministic subnet-pool listing

`tsipam subnet list` SHALL return every subnet-pool reservation in the
selected space (default `tailnet`) sorted by network address, then by
prefix length. Human output SHALL contain one
tab-separated `cidr<TAB>site` line per reservation, with metadata columns
appended when present. JSON output SHALL carry a `subnet_pools` array in the
versioned envelope, with an empty registry map represented as an empty array.

#### Scenario: List reflects a single reservation in JSON

- **WHEN** `10.0.0.0/24` is reserved for `hq-vpc` and `subnet list --format
  json` is run
- **THEN** the JSON envelope's `subnet_pools` array contains exactly one
  entry with `cidr: "10.0.0.0/24"` and `site: "hq-vpc"`

## ADDED Requirements

### Requirement: Spaces do not conflict

Collision and overlap rules SHALL apply within the space selected by `--space`
(default `tailnet`). Pools in different spaces SHALL NOT conflict, whether their
CIDRs are equal or overlapping.

#### Scenario: The same CIDR in two spaces

- **WHEN** `10.0.0.0/24` is reserved for `boston` in space `Boston` and an
  operator reserves `10.0.0.0/24` for `chicago` in space `Chicago`
- **THEN** both reservations succeed, and each `subnet list --space` shows
  only its own

### Requirement: Pools have a usage

A pool SHALL have the usage `routed`, the default, or `external`, set by
`subnet reserve` and `subnet allocate` `--usage`. `external` SHALL mean that no
tailnet route may use the pool. Only `external` SHALL be stored. A repeat reserve
that omits `--usage` SHALL keep the stored usage; one that names a different
usage SHALL change it and report `changed=true`.

#### Scenario: Marking on-prem space

- **WHEN** `subnet reserve 10.50.0.0/16 plant --usage external` runs
- **THEN** the pool is stored with usage `external` and `subnet list` shows it

#### Scenario: Omitting the flag keeps the usage

- **WHEN** that pool is reserved again for `plant` with only `--owner ops`
- **THEN** its usage is still `external`

#### Scenario: Returning to routed

- **WHEN** it is reserved again with `--usage routed`
- **THEN** the result reports `changed=true` and no usage is stored

#### Scenario: An unknown usage

- **WHEN** `--usage transit` is given
- **THEN** the command exits 2 and the registry is unchanged

### Requirement: External pools block overlap and allocation

An external pool SHALL take part in collision checks and SHALL count as taken in
its space exactly as a routed pool does.

#### Scenario: Allocation skips external space

- **WHEN** `10.0.0.0/24` is external and a `/24` is allocated from `10.0.0.0/16`
- **THEN** the allocation does not return `10.0.0.0/24`

### Requirement: Subnet commands select a space

`subnet reserve`, `allocate`, `release`, and `list` SHALL accept `--space`.
Results for a named space SHALL carry its name in JSON output; results for the
default space SHALL NOT carry the field.

#### Scenario: Release touches one space

- **WHEN** `10.0.0.0/24` exists in `Boston` and `Chicago` and
  `subnet release 10.0.0.0/24 --space Boston --yes` runs
- **THEN** only the `Boston` reservation is removed
