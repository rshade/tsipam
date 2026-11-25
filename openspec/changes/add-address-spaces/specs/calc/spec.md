# Calc Specification Delta

## MODIFIED Requirements

### Requirement: Aggregate subnet reservations from the selected registry

`tsipam calc aggregate --from-registry` SHALL summarize all subnet-pool
reservations in one space of the selected registry using the same normalization and
minimal-cover calculation as positional aggregation. It SHALL perform the
read through the registry API, without collecting live device routes or
mutating the registry. `--registry-dir` SHALL use the shared directory
selection (default `.`); the tailnet SHALL resolve from `--tailnet`, then
`TAILSCALE_TAILNET`, then `local`.

#### Scenario: Reserved IPv4 siblings aggregate like supplied CIDRs

- **WHEN** the selected registry reserves `10.0.0.0/25`, `10.0.0.128/25`,
  and `10.2.0.0/24`, and `calc aggregate --from-registry` runs
- **THEN** human stdout is `10.0.0.0/24` followed by `10.2.0.0/24`, each
  on its own line, matching positional aggregation of those reservations
- **AND** the registry bytes are unchanged and no live device read occurs

#### Scenario: Tailnet selection uses the existing precedence

- **WHEN** both `--tailnet production` and `TAILSCALE_TAILNET=staging` apply
- **THEN** registry aggregation reads `production.ipam.hujson` from the
  selected directory

#### Scenario: Empty reservations are a successful empty result

- **WHEN** the selected registry exists with an empty or absent subnet-pool map
- **THEN** the command exits 0, human stdout is empty, and JSON output
  contains `"supernets": []` with status `ok`

#### Scenario: Registry errors do not become empty successes

- **WHEN** the selected registry is missing, unreadable, or has an invalid
  subnet-pool map
- **THEN** the command exits 2, stdout is empty, stderr reports the error,
  and no registry state is written

#### Scenario: Registry-derived output is sanitized

- **WHEN** registry aggregation runs with identity or strict sanitization
- **THEN** human and JSON supernets use CIDR placeholders, JSON replaces
  the tailnet with its placeholder, and diagnostics do not reveal registry
  identities
- **AND** a configured sanitization key produces stable placeholders scoped
  to the selected tailnet

#### Scenario: Invalid registry entries retain safe diagnostics

- **WHEN** registry aggregation with identity or strict sanitization rejects
  a non-canonical stored CIDR or invalid reservation owner metadata
- **THEN** diagnostics retain the validation reason while replacing raw and
  trimmed CIDRs, canonical CIDR suggestions, and owner values with placeholders
- **AND** the command exits 2 with empty stdout and leaves registry bytes unchanged

### Requirement: Calc suggests available IPv4 and IPv6 child blocks

`tsipam calc next-free <pool-cidr> --size <prefix>` SHALL return the first
available IPv4 or IPv6 child CIDR of the requested size within the canonical
pool. It SHALL exclude every overlapping same-family subnet-pool reservation
in the selected space of the selected registry tailnet and every overlapping
same-family live prefix that is compared with that space, which for the
default space is every route advertised by a live device. Other-family and outside-pool prefixes SHALL be
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

## ADDED Requirements

### Requirement: Registry aggregation selects a space

`calc aggregate --from-registry` SHALL read the space named by `--space` (default
`tailnet`). `--space` without `--from-registry` SHALL be a usage error.

#### Scenario: One space is aggregated

- **WHEN** the default space holds `10.0.0.0/25`, `Boston` holds
  `10.0.0.128/25`, and `calc aggregate --from-registry` runs
- **THEN** stdout is `10.0.0.0/25`

#### Scenario: Space requires registry mode

- **WHEN** `calc aggregate --space Boston 10.0.0.0/24` runs
- **THEN** it exits 2 with an error naming `--from-registry`

### Requirement: Calc next-free selects a space

`calc next-free` SHALL accept `--space` (default `tailnet`). In a mapped space the
live prefixes SHALL be those decoded from via routes that carry its site ID. It
SHALL require a live read for the default
space and for a mapped space, and none for a space compared with no live state.

#### Scenario: The same search in two spaces

- **WHEN** `Boston` holds `10.0.0.0/24`, `Lab` holds nothing, neither is mapped,
  and `calc next-free 10.0.0.0/16 --size 24` runs with each `--space`
- **THEN** `Boston` returns `10.0.1.0/24`, `Lab` returns `10.0.0.0/24`, and no
  live read occurs
