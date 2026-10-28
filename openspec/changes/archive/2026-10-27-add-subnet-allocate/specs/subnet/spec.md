# Subnet Allocate Delta

## ADDED Requirements

### Requirement: Atomic next-free allocation under the registry lock

`tail-ipam subnet allocate <pool-cidr> --size <prefix> <site-name>` SHALL
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
