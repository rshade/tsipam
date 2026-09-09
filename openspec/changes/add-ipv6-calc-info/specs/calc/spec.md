# Calc Spec Delta

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
