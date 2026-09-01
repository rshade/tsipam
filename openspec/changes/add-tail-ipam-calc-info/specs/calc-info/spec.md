# calc-info Spec Delta

## Purpose

Stateless IPv4 block-fact reporting: given a CIDR, `tail-ipam calc info`
reports the network address, broadcast address, usable host range, and
usable-host count — classic ipcalc output — without touching the registry,
the network, or any tailnet state.

## ADDED Requirements

### Requirement: IPv4 block facts

`calc info` SHALL accept exactly one IPv4 CIDR argument and report the
block's network address, broadcast address, first usable host, last usable
host, and usable-host count, computed purely from the argument. The command
SHALL NOT read the registry, contact the control plane, or take any flags.

For a prefix length of /30 or shorter, the usable range SHALL exclude the
network and broadcast addresses, and the usable-host count SHALL be
2^(32−n) − 2.

An argument whose host bits are set SHALL be treated as an address inside
the block: facts are reported for the containing (masked) network.

#### Scenario: Classic /24 block

- **WHEN** `tail-ipam calc info 192.168.1.0/24` runs
- **THEN** it reports network `192.168.1.0`, broadcast `192.168.1.255`,
  first usable `192.168.1.1`, last usable `192.168.1.254`, and 254 usable
  hosts, and exits 0

#### Scenario: Host bits are masked to the containing block

- **WHEN** `tail-ipam calc info 192.168.1.37/24` runs
- **THEN** it reports the facts for `192.168.1.0/24`, identical to the
  masked invocation

### Requirement: RFC 3021 point-to-point and host-route blocks

A `/31` SHALL be reported per RFC 3021: both addresses usable (first usable
is the lower address, last usable the upper), usable-host count 2, and no
broadcast address reported. A `/32` SHALL be reported as a host route: its
single address is the network and the only usable host, usable-host count
1, and no broadcast address reported. Neither size SHALL error or report an
inverted or empty range.

#### Scenario: /31 point-to-point

- **WHEN** `tail-ipam calc info 10.0.0.0/31` runs
- **THEN** it reports network `10.0.0.0`, usable range `10.0.0.0`–`10.0.0.1`,
  2 usable hosts, omits the broadcast address, and exits 0

#### Scenario: /32 host route

- **WHEN** `tail-ipam calc info 10.0.0.5/32` runs
- **THEN** it reports network `10.0.0.5`, usable range `10.0.0.5`–`10.0.0.5`,
  1 usable host, omits the broadcast address, and exits 0

### Requirement: IPv6 input is rejected with a follow-up pointer

A native IPv6 CIDR SHALL be rejected as an invalid invocation (usage exit
code): the error message SHALL state that `calc info` does not yet support
IPv6 and SHALL point at the IPv6 follow-up issue
(rshade/tailscale-utils#251). The error SHALL go to stderr with nothing on
stdout.

#### Scenario: Native IPv6 rejected

- **WHEN** `tail-ipam calc info 2001:db8::/64` runs
- **THEN** it exits with the usage exit code, prints nothing on stdout, and
  the stderr message names IPv6 as unsupported and references issue #251

### Requirement: IPv4-mapped input is normalized

An IPv4-mapped IPv6 CIDR (`::ffff:a.b.c.d/n`, prefix length ≥ 96) SHALL be
normalized to its plain IPv4 equivalent and reported as IPv4, consistent
with `calc split`'s existing normalization.

#### Scenario: 4-in-6 prefix normalized to IPv4

- **WHEN** `tail-ipam calc info ::ffff:10.0.0.0/120` runs
- **THEN** it reports the facts for `10.0.0.0/24` and exits 0

### Requirement: Output contract

In JSON format the block facts SHALL be carried as a dedicated payload
object in the versioned output envelope, with the usable-host count encoded
as a decimal string (so the IPv6 follow-up can exceed integer-safe JSON
range without changing the field's type). The broadcast field SHALL be
omitted, not empty, when the block has none. In human format the command
SHALL print one tab-separated `fact<TAB>value` line per reported fact.
Results go to stdout and stdout SHALL remain parse-clean. `calc info`
output SHALL NOT be sanitized (it contains no tailnet-derived data), and
the tool's schema description SHALL document that exemption.

#### Scenario: JSON envelope payload

- **WHEN** `tail-ipam calc info 10.1.2.0/30 --format json` runs
- **THEN** stdout is a single JSON envelope whose block payload reports
  prefix `10.1.2.0/30`, network `10.1.2.0`, broadcast `10.1.2.3`, usable
  range `10.1.2.1`–`10.1.2.2`, and usable-host count `"2"` as a string

#### Scenario: Human tab-separated facts

- **WHEN** `tail-ipam calc info 192.168.0.0/16` runs with human format
- **THEN** stdout contains one `fact<TAB>value` line per fact, including a
  usable-host count of 65534, and no log or banner text

### Requirement: Invalid input is a usage error

An argument that does not parse as a CIDR SHALL be rejected with the usage
exit code, an error on stderr naming the invalid input, and nothing on
stdout.

#### Scenario: Malformed CIDR rejected

- **WHEN** `tail-ipam calc info not-a-cidr` runs
- **THEN** it exits with the usage exit code, prints nothing on stdout, and
  stderr names the rejected argument

### Requirement: Agent discoverability

The `calc info` subcommand SHALL appear in the machine-readable tool schema
(`tail-ipam -schema`) alongside the other commands.

#### Scenario: Schema lists calc info

- **WHEN** `tail-ipam -schema` runs
- **THEN** the command catalog includes an entry for `calc info`
