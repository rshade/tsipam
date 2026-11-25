# Policy Check Specification Delta

## MODIFIED Requirements

### Requirement: Bidirectional matching

An effective authored CIDR SHALL be `matched` if at least one reserved pool contains its entire address space;
otherwise it SHALL be `authored_but_unreserved`. Adjacent reservations SHALL not combine to satisfy containment.
A pool SHALL be `matched` if any effective authored CIDR overlaps it; otherwise it SHALL be
`reserved_but_unreferenced`. IPv4-mapped prefixes SHALL compare with their IPv4 equivalents while retaining native
IPv6 containment. A pool with usage `external` that no authored CIDR overlaps SHALL be `external`, which is not an
unmatched row. These rules SHALL apply to the default space. The report SHALL identify the defining ipset, canonical
CIDR, and containing pools for each authored row, and the CIDR, site, and referencing ipsets for each reserved row.

#### Scenario: Contained subnet

- **WHEN** `ipset:office` contains `192.0.2.0/25` and the registry reserves `192.0.2.0/24`
- **THEN** both the authored CIDR and reserved pool are matched

#### Scenario: Broader authored prefix

- **WHEN** a set contains `192.0.2.0/24` and two adjacent `/25` reservations cover it
- **THEN** the authored `/24` is unreserved and both reservations are referenced

#### Scenario: Drift in both directions

- **WHEN** a set contains `198.51.100.0/24` and the registry only reserves `192.0.2.0/24`
- **THEN** one authored row is unreserved and one reserved row is unreferenced

#### Scenario: Unreferenced external space

- **WHEN** the registry reserves `10.50.0.0/16` as external and no set references it
- **THEN** its row is `external`, it adds nothing to the drift count, and the command exits 0 if nothing else drifts

## ADDED Requirements

### Requirement: Authored via prefixes match mapped spaces

An authored 4via6 CIDR whose site ID maps a space SHALL be decoded and compared only with that space's IPv4 pools, and
its IPv6 form SHALL be compared with default-space pools; containment in either SHALL be `matched`. An IPv4 pool in a
mapped space SHALL be referenced only by via prefixes decoded to its site ID. Rows for a named space SHALL carry
`space`, and decoded matches SHALL carry `mappings`; both fields SHALL be absent otherwise.

#### Scenario: A via prefix in a set

- **WHEN** site ID 3 maps `Boston`, which holds `10.0.0.0/24`, and a set contains `fd7a:115c:a1e0:b1a:0:3:a00:0/120`
- **THEN** the authored CIDR and the `Boston` pool are matched, and `mappings` holds one entry labelled
  `4via6:3:Boston:10.0.0.0/24`

#### Scenario: Hexadecimal site IDs read as decimal

- **WHEN** site IDs 20 and 14 map `Boston` and `Chicago`, both hold `10.0.0.0/24`, and a set contains
  `fd7a:115c:a1e0:b1a:0:14:a00:0/120`
- **THEN** the `Boston` pool is matched with the label `4via6:20:Boston:10.0.0.0/24`, and the `Chicago` pool is
  `reserved_but_unreferenced`

#### Scenario: A via prefix does not reach a default-space IPv4 pool

- **WHEN** site ID 3 maps `Boston`, which holds no pool, the default space holds `10.0.0.0/8`, and a set contains
  `fd7a:115c:a1e0:b1a:0:3:a00:0/120`
- **THEN** the authored CIDR is `authored_but_unreserved`, and `10.0.0.0/8` is not referenced

#### Scenario: A plain IPv4 CIDR does not reach a named space

- **WHEN** only `Boston` holds `10.0.0.0/24` and a set contains `10.0.0.0/24`
- **THEN** the authored CIDR is `authored_but_unreserved`, and the `Boston` pool is `reserved_but_unreferenced` when
  `Boston` is mapped

### Requirement: Spaces the policy cannot reach are not compared

Pools in an unmapped space, IPv6 pools in a named space, and pools in a mapped space with usage `external` that no
decoded via prefix overlaps SHALL be reported as `external` and SHALL add nothing to the drift count.

#### Scenario: An on-prem space

- **WHEN** `Plant` is not mapped and holds `10.0.0.0/8`
- **THEN** its row is `external` and the command exits 0 if nothing else drifts

#### Scenario: An external pool in a mapped space

- **WHEN** mapped `Boston` holds `10.9.0.0/24` as external and no set holds a via prefix that overlaps it
- **THEN** its row is `external`

### Requirement: An invalid space map fails the policy check

`check-policy` SHALL read the space map along with the subnet pools, and SHALL exit 2 with empty stdout when that map
is invalid. It SHALL NOT read the site-ID map.

#### Scenario: Malformed spaces

- **WHEN** `addressSpaces` is malformed
- **THEN** the command exits 2 with empty stdout

#### Scenario: Malformed site IDs do not matter

- **WHEN** `siteIDs` is malformed and the pools and spaces are valid
- **THEN** the comparison completes
