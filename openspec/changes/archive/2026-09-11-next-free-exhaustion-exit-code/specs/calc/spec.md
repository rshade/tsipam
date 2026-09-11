# Calc Next-Free Exit Code Delta

## REMOVED Requirements

### Requirement: Calc next-free handles exhaustion and invalid target sizes

**Reason**: exhaustion was a successful result with an empty `free_blocks`
array; since #356 it is reported as a failed output operation, so the
requirement is replaced by the one added below.

## ADDED Requirements

### Requirement: Calc next-free rejects exhausted pools and invalid target sizes

When no requested IPv4 or IPv6 child is available, `calc next-free` SHALL
exit 2, write the exhaustion message to stderr, and write nothing to stdout
in either output format, so the documented composition
`tail-ipam subnet reserve "$(tail-ipam calc next-free ...)"` fails at the
composing command instead of passing text as a CIDR. Target widths shorter
than the canonical pool or greater than the address-family width SHALL be
usage errors.

#### Scenario: Exhausted pool exits 2 with nothing on stdout

- **WHEN** exclusions cover every requested child of the pool
- **THEN** the command exits 2 in both human and JSON formats, stdout is
  empty, and stderr names the missing block size and the pool

#### Scenario: Shell composition fails at the composing command

- **WHEN** the documented composition runs against an exhausted pool
- **THEN** `subnet reserve` receives an empty argument and fails with
  `subnet CIDR must not be empty`, with the exhaustion reason already on
  stderr from `calc next-free`

#### Scenario: IPv6 host pool can be exhausted

- **WHEN** a `/128` pool is entirely excluded
- **THEN** the command exits 2 with no suggestions

#### Scenario: IPv6 target width exceeds its address width

- **WHEN** an IPv6 pool is searched with `--size 129`
- **THEN** the command rejects the target with a usage error
