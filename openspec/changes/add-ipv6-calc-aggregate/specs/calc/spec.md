# Calc Spec Delta

## RENAMED Requirements

- FROM: `### Requirement: Aggregate supplied IPv4 CIDRs`
- TO: `### Requirement: Aggregate supplied CIDRs`

## MODIFIED Requirements

### Requirement: Aggregate supplied CIDRs

`tail-ipam calc aggregate <cidr...>` SHALL accept one or more supplied IPv4
or IPv6 CIDRs and print their deterministic minimal CIDR cover, one
normalized CIDR per line in ascending network-address order. Duplicate,
covered, and adjacent sibling CIDRs SHALL collapse into that cover. A CIDR
SHALL never merge with, or be treated as covered by, a CIDR of the other
address family; when both families are supplied, every IPv4 supernet SHALL
precede every IPv6 supernet. The command SHALL be stateless and SHALL not
read a registry, tailnet, or external API.

#### Scenario: A complete pair becomes its supernet

- **WHEN** an operator runs `calc aggregate 10.0.0.0/25 10.0.0.128/25`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline

#### Scenario: Unrelated CIDRs remain separate

- **WHEN** an operator runs `calc aggregate 10.1.0.0/24 10.0.0.0/24`
- **THEN** stdout lists `10.0.0.0/24` and then `10.1.0.0/24`, one per line

#### Scenario: Covered CIDRs do not produce duplicates

- **WHEN** an operator runs `calc aggregate 10.0.0.0/24 10.0.0.0/25`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline

#### Scenario: Adjacent IPv6 siblings merge

- **WHEN** an operator runs `calc aggregate 2001:db8:0:8000::/49 2001:db8::/49`
- **THEN** stdout contains exactly `2001:db8::/48` followed by a newline

#### Scenario: ULA blocks merge across levels

- **WHEN** an operator runs
  `calc aggregate fd12:3456:789a:1::/64 fd12:3456:789a:2::/63 fd12:3456:789a::/64`
- **THEN** stdout contains exactly `fd12:3456:789a::/62` followed by a newline

#### Scenario: Unmergeable IPv6 CIDRs stay separate and ordered

- **WHEN** an operator runs `calc aggregate 2001:db8:2::/48 2001:db8::/48`
- **THEN** stdout lists `2001:db8::/48` and then `2001:db8:2::/48`, one per
  line

#### Scenario: Mixed families aggregate independently with IPv4 first

- **WHEN** an operator runs
  `calc aggregate 2001:db8::/49 10.0.0.128/25 2001:db8:0:8000::/49 10.0.0.0/25`
- **THEN** stdout lists `10.0.0.0/24` and then `2001:db8::/48`, one per line

## REMOVED Requirements

### Requirement: Native IPv6 aggregate input is refused

**Reason**: Native IPv6 aggregation is now supported, so the refusal and its
tracking-issue reference are obsolete.
**Migration**: None. A caller that relied on the usage error to detect IPv6
input now receives an aggregated result. IPv4-mapped normalization moves to
the added requirement below.

## ADDED Requirements

### Requirement: Aggregate normalizes IPv4-mapped input

IPv4-mapped prefixes of `/96` or longer SHALL normalize to their plain IPv4
equivalents before aggregation, consistent with the other calc commands, so
their result is identical to aggregating the equivalent IPv4 CIDRs. A mapped
prefix shorter than `/96` cannot be exclusively IPv4-mapped and SHALL be
aggregated as native IPv6.

#### Scenario: An IPv4-mapped prefix aggregates as IPv4

- **WHEN** an operator runs `calc aggregate ::ffff:10.0.0.0/121 ::ffff:10.0.0.128/121`
- **THEN** stdout contains exactly `10.0.0.0/24` followed by a newline

#### Scenario: A short mapped prefix aggregates as native IPv6

- **WHEN** an operator runs `calc aggregate ::ffff:10.0.0.0/95`
- **THEN** the command succeeds and stdout contains exactly `::fffe:0:0/95`
  followed by a newline
