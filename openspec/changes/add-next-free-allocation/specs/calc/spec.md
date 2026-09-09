# Calc next-free delta

## ADDED Requirements

### Requirement: Calc suggests available IPv4 child blocks

`tail-ipam calc next-free <pool-cidr> --size <prefix>` SHALL return the first
available IPv4 child CIDR of the requested size within the pool. It SHALL
exclude every overlapping subnet-pool reservation for the selected registry
tailnet and every overlapping route advertised by a live device. It SHALL
perform no registry write or other mutation.

#### Scenario: Registry and live routes both exclude candidates

- **WHEN** a pool's first child is reserved in the local registry and its
  second child is advertised by a live device
- **THEN** the command returns the third child CIDR

#### Scenario: A live-only route excludes a candidate

- **WHEN** no registry reservation conflicts but a live advertised route
  overlaps the first requested child
- **THEN** the command does not return that child

### Requirement: Calc next-free renders composable CIDR output

A successful single suggestion SHALL write exactly one bare CIDR followed by a
newline in human format. `--format json` SHALL expose the suggested CIDRs in
the versioned envelope. `--all` SHALL return every available child and
`--count` (short form `-n`) SHALL return up to its positive requested count,
with one CIDR per human-output line in ascending address order.

#### Scenario: Single suggestion is usable in shell substitution

- **WHEN** an operator runs `tail-ipam calc next-free 10.0.0.0/24 --size 26`
  and the first child is available
- **THEN** human stdout is exactly `10.0.0.0/26\\n`

#### Scenario: Count limits returned suggestions

- **WHEN** more than two children are available and the operator passes
  `--count 2`
- **THEN** the command returns the first two available CIDRs only

### Requirement: Calc next-free handles exhaustion and unsupported families

When no requested IPv4 child is available, `calc next-free` SHALL return a
normal successful result describing that no free block exists. It SHALL reject
native IPv6 input as a usage error that references the IPv6 follow-up issue.

#### Scenario: Exhausted pool is not a command failure

- **WHEN** exclusions cover every requested child of the pool
- **THEN** the command exits successfully and reports that no free block is
  available

#### Scenario: IPv6 waits for the follow-up capability

- **WHEN** an operator supplies an IPv6 pool
- **THEN** the command exits with a usage error that names IPv6 and issue #257
