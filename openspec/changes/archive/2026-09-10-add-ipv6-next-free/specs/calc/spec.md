# IPv6 next-free delta

## RENAMED Requirements

- FROM: `### Requirement: Calc suggests available IPv4 child blocks`
- TO: `### Requirement: Calc suggests available IPv4 and IPv6 child blocks`

## MODIFIED Requirements

### Requirement: Calc suggests available IPv4 and IPv6 child blocks

`tail-ipam calc next-free <pool-cidr> --size <prefix>` SHALL return the first
available IPv4 or IPv6 child CIDR of the requested size within the canonical
pool. It SHALL exclude every overlapping same-family subnet-pool reservation
for the selected registry tailnet and every overlapping same-family route
advertised by a live device. Other-family and outside-pool prefixes SHALL be
ignored. It SHALL perform no registry write or other mutation. IPv4-mapped
prefixes of `/96` or longer SHALL normalize to IPv4 before target validation;
shorter mapped-looking prefixes SHALL remain IPv6.

#### Scenario: Registry and live routes both exclude candidates

- **WHEN** a pool's first child is reserved in the local registry and its
  second child is advertised by a live device
- **THEN** the command returns the third child CIDR

#### Scenario: A live-only route excludes a candidate

- **WHEN** no registry reservation conflicts but a live advertised route
  overlaps the first requested child
- **THEN** the command does not return that child

#### Scenario: IPv6 allocation ignores IPv4 exclusions

- **WHEN** the registry reserves `2001:db8::/48` and `10.0.0.0/8`, and live
  routes include `2001:db8:1::/48` and `0.0.0.0/0`
- **THEN** searching `2001:db8::/32` for `/48` returns `2001:db8:2::/48`

#### Scenario: IPv4 allocation ignores IPv6 exclusions

- **WHEN** the registry and live routes contain IPv6 prefixes alongside IPv4
  prefixes that exclude the first two IPv4 children
- **THEN** an IPv4 search returns the third IPv4 child

### Requirement: Calc next-free renders composable CIDR output

A successful single suggestion SHALL write exactly one bare CIDR followed by a
newline in human format. `--format json` SHALL expose the suggested CIDRs in
the versioned envelope's `free_blocks` array. `--all` SHALL return every
available child and `--count` (short form `-n`) SHALL return up to its positive
requested count, with one CIDR per human-output line in ascending address
order. These rules SHALL apply to both families. Configured sanitization
SHALL replace IPv6 suggestions and pool identifiers in exhaustion messages.

#### Scenario: Single suggestion is usable in shell substitution

- **WHEN** an operator runs `tail-ipam calc next-free 10.0.0.0/24 --size 26`
  and the first child is available
- **THEN** human stdout is exactly `10.0.0.0/26` followed by a newline

#### Scenario: Count limits returned suggestions

- **WHEN** more than two children are available and the operator passes
  `--count 2`
- **THEN** the command returns the first two available CIDRs only

#### Scenario: IPv6 single suggestion is a bare CIDR

- **WHEN** `calc next-free 2001:db8::/32 --size 48` finds the first child free
- **THEN** stdout is exactly `2001:db8::/48` followed by a newline, with no
  diagnostic text on stderr

#### Scenario: IPv6 host children are ordered

- **WHEN** `calc next-free 2001:db8::/127 --size 128 --all` has no exclusions
- **THEN** it returns `2001:db8::/128` and `2001:db8::1/128` in that order

#### Scenario: JSON and sanitized output support IPv6

- **WHEN** an IPv6 search uses JSON output with identity sanitization
- **THEN** `free_blocks` contains CIDR placeholders rather than raw addresses

## REMOVED Requirements

### Requirement: Calc next-free handles exhaustion and unsupported families

**Reason**: Native IPv6 is now supported, so the scenario requiring its
rejection is intentionally removed.

**Migration**: Use the replacement exhaustion and target-validation
requirement below; IPv4 exhaustion behavior is preserved.

## ADDED Requirements

### Requirement: Calc next-free handles exhaustion and invalid target sizes

When no requested IPv4 or IPv6 child is available, `calc next-free` SHALL
return a normal successful result describing that no free block exists, with
an empty JSON `free_blocks` array. Target widths shorter than the canonical
pool or greater than the address-family width SHALL be usage errors.

#### Scenario: Exhausted pool is not a command failure

- **WHEN** exclusions cover every requested child of the pool
- **THEN** the command exits successfully and reports that no free block is
  available

#### Scenario: IPv6 host pool can be exhausted

- **WHEN** a `/128` pool is entirely excluded
- **THEN** the command succeeds with no suggestions

#### Scenario: IPv6 target width exceeds its address width

- **WHEN** an IPv6 pool is searched with `--size 129`
- **THEN** the command rejects the target with a usage error
