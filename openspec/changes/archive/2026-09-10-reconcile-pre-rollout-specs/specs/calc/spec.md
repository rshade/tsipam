# Calc Reconciliation Delta

## MODIFIED Requirements

### Requirement: Reporting block facts for a CIDR

`tail-ipam calc info <cidr>` SHALL report the network address, first/last
usable address, and usable-host count for IPv4 and IPv6 CIDRs, masking any
supplied host bits to the network address first. For IPv4, a broadcast address
SHALL be reported for prefixes shorter than `/31` and SHALL be omitted entirely
(not emitted as empty) for RFC 3021 `/31` and `/32` prefixes. For IPv6, no
broadcast address SHALL be reported, and the usable range SHALL include every
address from the masked network through the prefix's top address.

The JSON `usable_hosts` field SHALL remain a decimal string. IPv6 counts SHALL
be rendered as their exact decimal value without a notation cutover: the
largest possible count is `2^128`, only 39 decimal digits, so it is precise,
readable, and uniformly parseable.

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

#### Scenario: IPv6 block reports exact facts without broadcast

- **WHEN** an operator runs `calc info 2001:db8:1::42/64 --format json`
- **THEN** the envelope reports prefix `2001:db8:1::/64`, network and first
  usable `2001:db8:1::`, last usable `2001:db8:1:0:ffff:ffff:ffff:ffff`,
  usable hosts `"18446744073709551616"`, and no `broadcast` key

#### Scenario: IPv6 host route reports one usable host

- **WHEN** an operator runs `calc info 2001:db8::1/128`
- **THEN** it reports the address as network, first usable, and last usable,
  reports usable hosts `1`, and contains no broadcast line

#### Scenario: IPv6 input is rejected as unsupported

<!-- This legacy scenario identifier is retained so OpenSpec can replace the
current requirement during archive. The changed behavior is defined below. -->

- **WHEN** an operator runs `calc info 2001:db8::/64`
- **THEN** it exits successfully and reports IPv6 block facts rather than an
  unsupported-input error

## ADDED Requirements

### Requirement: IPv4 usable ranges include point-to-point and host routes

For IPv4 prefixes of length /30 or shorter, the usable range SHALL exclude
the network and broadcast addresses and the usable-host count SHALL be
2^(32-n) - 2. Ranges SHALL be computed after masking supplied host bits.
A /31 SHALL treat both addresses as usable, with count 2 and no broadcast.
A /32 SHALL treat its single address as the network and only usable host,
with count 1 and no broadcast. Neither size SHALL produce an inverted or
empty usable range.

#### Scenario: Host bits are masked to the containing block

- **WHEN** `tail-ipam calc info 192.168.1.37/24` runs
- **THEN** it reports the same facts as `192.168.1.0/24`, including usable
  range `192.168.1.1` through `192.168.1.254` and 254 usable hosts

#### Scenario: Point-to-point block has two usable addresses

- **WHEN** `tail-ipam calc info 10.0.0.0/31` runs
- **THEN** network and first usable are `10.0.0.0`, last usable is
  `10.0.0.1`, usable hosts is 2, broadcast is omitted, and the command exits 0

#### Scenario: Host route has one usable address

- **WHEN** `tail-ipam calc info 10.0.0.5/32` runs
- **THEN** network, first usable, and last usable are all `10.0.0.5`, usable
  hosts is 1, broadcast is omitted, and the command exits 0

### Requirement: Calc info normalizes IPv4-mapped input

An IPv4-mapped IPv6 CIDR with prefix length at least /96 SHALL normalize to
its plain IPv4 equivalent before block-fact reporting, consistent with
`calc split`. A mapped-looking prefix shorter than /96 SHALL remain IPv6.

#### Scenario: Mapped input has the same facts as plain IPv4

- **WHEN** `tail-ipam calc info ::ffff:10.0.0.0/120` runs
- **THEN** it reports the facts for `10.0.0.0/24` and exits 0

### Requirement: Calc info accepts one CIDR and rejects malformed input

`calc info` SHALL require exactly one CIDR argument and compute its facts
without registry or control-plane access. It SHALL accept the shared output
and sanitization flags without introducing command-specific flags.
An argument that does not parse as a CIDR SHALL fail with usage exit code 2,
an error on stderr naming the invalid input, and nothing on stdout.

#### Scenario: Malformed CIDR is a usage error

- **WHEN** `tail-ipam calc info not-a-cidr` runs
- **THEN** it exits 2, stdout is empty, and stderr names `not-a-cidr`

### Requirement: Calc info renders block facts in the versioned envelope

In JSON format, `calc info` SHALL put its facts in the versioned envelope's
`block_info` object, with `prefix`, `network`, `first_usable`,
`last_usable`, and `usable_hosts` fields. The prefix SHALL be the masked,
normalized CIDR. Host counts SHALL follow the decimal-string encoding
requirement. The `broadcast` field SHALL be omitted when inapplicable.
Human format SHALL contain one tab-separated `fact<TAB>value` line per
reported fact, using the same field names. Results SHALL go to stdout with
no diagnostic or banner text; diagnostics SHALL go to stderr.

#### Scenario: JSON envelope contains the complete block payload

- **WHEN** `tail-ipam calc info 10.1.2.0/30 --format json` runs
- **THEN** stdout is one JSON envelope whose `block_info` reports prefix
  `10.1.2.0/30`, network `10.1.2.0`, broadcast `10.1.2.3`, first usable
  `10.1.2.1`, last usable `10.1.2.2`, and usable hosts `"2"`

#### Scenario: Human output uses tab-separated facts

- **WHEN** `tail-ipam calc info 192.168.0.0/16` runs with human format
- **THEN** stdout contains one `fact<TAB>value` line per reported fact,
  including usable hosts 65534, with no log or banner text

### Requirement: Calc info is discoverable through schema

The `calc info` subcommand SHALL appear in `tail-ipam -schema` alongside
the other commands. The schema's sanitization description SHALL document
that stateless calc output is exempt from sanitization.

#### Scenario: Schema lists calc info and its sanitization exemption

- **WHEN** `tail-ipam -schema` runs
- **THEN** the catalog includes `calc info` and its sanitization description
  explains that stateless calc output remains unredacted
