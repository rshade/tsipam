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
