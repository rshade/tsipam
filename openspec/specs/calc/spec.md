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

### Requirement: Aggregate JSON output carries supernets

Successful aggregate JSON output SHALL use the versioned output envelope with
the command identifier and a `supernets` array containing the same ordered
CIDRs as human output. Aggregate CIDRs SHALL remain unredacted at every
sanitize level, consistent with other stateless calc output.

#### Scenario: JSON preserves the aggregate result

- **WHEN** an operator runs `calc aggregate 10.0.0.0/25 10.0.0.128/25 --format json`
- **THEN** the JSON envelope identifies the aggregate command and contains
  `"supernets": ["10.0.0.0/24"]`

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
