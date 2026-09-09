# Calc Specification

## Purpose

Provides stateless IPv4/IPv6 subnet and CIDR arithmetic (`calc split`,
`calc info`) with no registry, tailnet, or control-plane dependency.

## Requirements

### Requirement: Splitting a CIDR into equal child prefixes

`tail-ipam calc split <cidr> --prefix <bits>` SHALL divide the given CIDR into
equal-sized child prefixes of the target length and print one child per line
in ascending order. A 4-in-6 mapped IPv4 prefix (`::ffff:a.b.c.d/n` with
`n >= 96`) SHALL be normalized to its plain IPv4 form before splitting, so its
result set is identical to splitting the equivalent IPv4 CIDR; a mapped prefix
shorter than `/96` SHALL be split as native IPv6 instead. A split that would
produce more child prefixes than the tool can materialize SHALL be refused as
a failure (exit 1) rather than truncated, and SHALL write nothing to stdout.

#### Scenario: IPv4 CIDR splits into ordered children

- **WHEN** an operator runs `calc split 10.0.0.0/24 --prefix 26`
- **THEN** stdout lists exactly `10.0.0.0/26`, `10.0.0.64/26`,
  `10.0.0.128/26`, and `10.0.0.192/26`, one per line, in that order

#### Scenario: 4-in-6 mapped prefix matches its IPv4 equivalent

- **WHEN** `calc split ::ffff:10.0.0.0/120 --prefix 28` and
  `calc split 10.0.0.0/24 --prefix 28` are both run
- **THEN** both commands produce byte-identical stdout

#### Scenario: Oversized split is refused, not truncated

- **WHEN** an operator runs `calc split 10.0.0.0/8 --prefix 32`
- **THEN** the command exits 1, writes nothing to stdout, and stderr explains
  that splitting would produce more subnets than the tool will list, naming
  the exact count and the configured maximum

### Requirement: Reporting block facts for a CIDR

`tail-ipam calc info <cidr>` SHALL report the network address, first/last
usable address, and usable host count for an IPv4 CIDR, masking any supplied
host bits to the network address first. A broadcast address SHALL be reported
for prefixes shorter than `/31`, and SHALL be omitted entirely (not emitted as
empty) for RFC 3021 `/31` and `/32` prefixes, which have no broadcast. IPv6
input SHALL be rejected as not yet supported, naming the tracking issue.

#### Scenario: Standard IPv4 block reports every fact

- **WHEN** an operator runs `calc info 192.168.1.0/24`
- **THEN** stdout reports network `192.168.1.0`, broadcast `192.168.1.255`,
  first usable `192.168.1.1`, last usable `192.168.1.254`, and usable hosts
  `254`

#### Scenario: RFC 3021 prefixes omit broadcast

- **WHEN** an operator runs `calc info 10.0.0.0/31` or `calc info 10.0.0.5/32`
- **THEN** stdout reports network, first usable, last usable, and usable
  hosts, and contains no broadcast line (or, under `--format json`, no
  `broadcast` key)

#### Scenario: IPv6 input is rejected as unsupported

- **WHEN** an operator runs `calc info 2001:db8::/64`
- **THEN** the command exits with a usage error, and stderr names IPv6 as
  unsupported and references the tracking issue `#251`

### Requirement: Calc output is never sanitized

Because `calc` operates only on CLI-supplied CIDRs with no tailnet-derived
identity, its output SHALL be emitted unredacted regardless of the active
`--sanitize` level.

#### Scenario: Sanitize identity does not redact calc output

- **WHEN** `calc split 10.0.0.0/24 --prefix 26` or `calc info 192.168.1.0/24`
  runs with `--sanitize identity` active, in either human or JSON format
- **THEN** the resulting subnets and addresses appear unredacted, with no
  placeholder substitution

### Requirement: Usable-host count survives large ranges in JSON

`--format json` output for `calc info` SHALL encode the usable-host count as
a decimal string rather than a JSON number, so a future extension to larger
address ranges cannot silently overflow a consumer's integer parsing.

#### Scenario: JSON usable_hosts is string-encoded

- **WHEN** `calc info 10.1.2.0/30 --format json` runs
- **THEN** the JSON envelope carries `"usable_hosts": "2"` as a string value,
  not a bare number

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
