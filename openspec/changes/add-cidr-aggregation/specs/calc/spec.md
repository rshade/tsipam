# calc CIDR Aggregation Delta

## ADDED Requirements

### Requirement: Aggregate supplied IPv4 CIDRs

`tail-ipam calc aggregate <cidr...>` SHALL accept one or more supplied IPv4
CIDRs and print their deterministic minimal CIDR cover, one normalized CIDR
per line in ascending network-address order. Duplicate, covered, and
adjacent sibling CIDRs SHALL collapse into that cover. The command SHALL be
stateless and SHALL not read a registry, tailnet, or external API.

#### Scenario: A complete pair becomes its supernet

- **WHEN** an operator runs `calc aggregate 10.0.0.0/25 10.0.0.128/25`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline

#### Scenario: Unrelated CIDRs remain separate

- **WHEN** an operator runs `calc aggregate 10.1.0.0/24 10.0.0.0/24`
- **THEN** stdout lists `10.0.0.0/24` and then `10.1.0.0/24`, one per line

#### Scenario: Covered CIDRs do not produce duplicates

- **WHEN** an operator runs `calc aggregate 10.0.0.0/24 10.0.0.0/25`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline

### Requirement: Aggregate JSON output carries supernets

Successful aggregate JSON output SHALL use the versioned output envelope with
the command identifier and a `supernets` array containing the same ordered
CIDRs as human output. Aggregate CIDRs SHALL remain unredacted at every
sanitize level, consistent with other stateless calc output.

#### Scenario: JSON preserves the aggregate result

- **WHEN** an operator runs `calc aggregate 10.0.0.0/25 10.0.0.128/25 --format json`
- **THEN** the JSON envelope identifies the aggregate command and contains
  `"supernets": ["10.0.0.0/24"]`

### Requirement: Native IPv6 aggregate input is refused

The aggregate command SHALL reject native IPv6 CIDRs as unsupported until the
IPv6 aggregate follow-up is implemented, with no success output. IPv4-mapped
prefixes of `/96` or longer SHALL normalize to their plain IPv4 equivalents,
consistent with the other calc commands.

#### Scenario: Native IPv6 is rejected

- **WHEN** an operator runs `calc aggregate 2001:db8::/64`
- **THEN** the command fails without stdout and stderr identifies IPv6 as
  unsupported and references the tracking follow-up `#253`

#### Scenario: An IPv4-mapped prefix aggregates as IPv4

- **WHEN** an operator runs `calc aggregate ::ffff:10.0.0.0/121 ::ffff:10.0.0.128/121`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline
