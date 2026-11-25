# Calc Specification

## Purpose

Provides stateless IPv4/IPv6 subnet and CIDR arithmetic (`calc split`,
`calc info`) with no registry, tailnet, or control-plane dependency.

## Requirements

### Requirement: Splitting a CIDR into equal child prefixes

`tsipam calc split <cidr> --prefix <bits>` SHALL divide the given CIDR into
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

`tsipam calc info <cidr>` SHALL report the network address, first/last
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

Stateless `calc split`, `calc info`, and positional `calc aggregate` operate
only on CLI-supplied CIDRs with no tailnet-derived identity. Their output
SHALL be emitted unredacted regardless of the active `--sanitize` level.
This exemption SHALL NOT apply to registry-derived aggregate results or
`calc next-free` output.

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

### Requirement: Calc suggests available IPv4 and IPv6 child blocks

`tsipam calc next-free <pool-cidr> --size <prefix>` SHALL return the first
available IPv4 or IPv6 child CIDR of the requested size within the canonical
pool. It SHALL exclude every overlapping same-family subnet-pool reservation
for the selected registry tailnet and every overlapping same-family route
advertised by a live device. Other-family and outside-pool prefixes SHALL be
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

### Requirement: Calc next-free renders composable CIDR output

A successful single suggestion SHALL write exactly one bare CIDR followed by a
newline in human format. `--format json` SHALL expose the suggested CIDRs in
the versioned envelope's `free_blocks` array. `--all` SHALL return every
available child and `--count` (short form `-n`) SHALL return up to its positive
requested count, with one CIDR per human-output line in ascending address
order. These rules SHALL apply to both families. Configured sanitization
SHALL replace IPv6 suggestions and pool identifiers in exhaustion messages.

#### Scenario: Single suggestion is usable in shell substitution

- **WHEN** an operator runs
  `tsipam calc next-free 10.0.0.0/24 --size 26 --format human` with stdout
  captured, and the first child is available
- **THEN** human stdout is exactly `10.0.0.0/26` followed by a newline

#### Scenario: Count limits returned suggestions

- **WHEN** more than two children are available and the operator passes
  `--count 2`
- **THEN** the command returns the first two available CIDRs only

#### Scenario: IPv6 single suggestion is a bare CIDR

- **WHEN** `calc next-free 2001:db8::/32 --size 48` finds the first child free
- **THEN** stdout is exactly `2001:db8::/48` followed by a newline, with no
  diagnostic text on stderr

#### Scenario: IPv6 host children are ordered

- **WHEN** `calc next-free 2001:db8::/127 --size 128 --all` has no exclusions
- **THEN** it returns `2001:db8::/128` and `2001:db8::1/128` in that order

#### Scenario: JSON and sanitized output support IPv6

- **WHEN** an IPv6 search uses JSON output with identity sanitization
- **THEN** `free_blocks` contains CIDR placeholders rather than raw addresses

### Requirement: Aggregate supplied CIDRs

`tsipam calc aggregate <cidr...>` SHALL accept one or more supplied IPv4
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
CIDRs as human output. Positional aggregate CIDRs SHALL remain unredacted at every
sanitize level. Registry-derived supernets and the resolved tailnet SHALL
be redacted under identity and strict sanitization; off SHALL emit the
original values. Registry mode SHALL include the resolved tailnet without
adding internal source markers to the JSON envelope.

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

- **WHEN** `tsipam calc info 192.168.1.37/24` runs
- **THEN** it reports the same facts as `192.168.1.0/24`, including usable
  range `192.168.1.1` through `192.168.1.254` and 254 usable hosts

#### Scenario: Point-to-point block has two usable addresses

- **WHEN** `tsipam calc info 10.0.0.0/31` runs
- **THEN** network and first usable are `10.0.0.0`, last usable is
  `10.0.0.1`, usable hosts is 2, broadcast is omitted, and the command exits 0

#### Scenario: Host route has one usable address

- **WHEN** `tsipam calc info 10.0.0.5/32` runs
- **THEN** network, first usable, and last usable are all `10.0.0.5`, usable
  hosts is 1, broadcast is omitted, and the command exits 0

### Requirement: Calc info normalizes IPv4-mapped input

An IPv4-mapped IPv6 CIDR with prefix length at least /96 SHALL normalize to
its plain IPv4 equivalent before block-fact reporting, consistent with
`calc split`. A mapped-looking prefix shorter than /96 SHALL remain IPv6.

#### Scenario: Mapped input has the same facts as plain IPv4

- **WHEN** `tsipam calc info ::ffff:10.0.0.0/120` runs
- **THEN** it reports the facts for `10.0.0.0/24` and exits 0

### Requirement: Calc info accepts one CIDR and rejects malformed input

`calc info` SHALL require exactly one CIDR, given by position or by `--cidr`,
and compute its facts without registry or control-plane access. It SHALL
accept the shared output and sanitization flags and no other command-specific
flag.
An argument that does not parse as a CIDR SHALL fail with usage exit code 2,
an error on stderr naming the invalid input, and nothing on stdout.

#### Scenario: Malformed CIDR is a usage error

- **WHEN** `tsipam calc info not-a-cidr` runs
- **THEN** it exits 2, stdout is empty, and stderr names `not-a-cidr`

#### Scenario: CIDR given by flag

- **WHEN** `tsipam calc info --cidr 10.0.0.0/24` runs
- **THEN** its result equals that of `tsipam calc info 10.0.0.0/24`

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

- **WHEN** `tsipam calc info 10.1.2.0/30 --format json` runs
- **THEN** stdout is one JSON envelope whose `block_info` reports prefix
  `10.1.2.0/30`, network `10.1.2.0`, broadcast `10.1.2.3`, first usable
  `10.1.2.1`, last usable `10.1.2.2`, and usable hosts `"2"`

#### Scenario: Human output uses tab-separated facts

- **WHEN** `tsipam calc info 192.168.0.0/16` runs with human format
- **THEN** stdout contains one `fact<TAB>value` line per reported fact,
  including usable hosts 65534, with no log or banner text

### Requirement: Calc info is discoverable through schema

The `calc info` subcommand SHALL appear in `tsipam -schema` alongside
the other commands. The schema's sanitization description SHALL document
that stateless calc output is exempt from sanitization.

#### Scenario: Schema lists calc info and its sanitization exemption

- **WHEN** `tsipam -schema` runs
- **THEN** the catalog includes `calc info` and its sanitization description
  explains that stateless calc output remains unredacted

### Requirement: Aggregate subnet reservations from the selected registry

`tsipam calc aggregate --from-registry` SHALL summarize all subnet-pool
reservations in the selected registry using the same normalization and
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

### Requirement: Aggregate sources are mutually exclusive

`--from-registry` SHALL accept no positional CIDRs and no `--cidr`. Without
registry mode, aggregate SHALL require at least one CIDR, given all by
position or all by `--cidr`, and SHALL reject
explicit `--registry-dir` or `--tailnet` flags. Invalid source combinations
SHALL fail with exit 2 and empty stdout before registry or live-state access.
Explicit `--from-registry=false` SHALL retain positional mode. Single-dash
long flags SHALL behave identically to their double-dash spellings.

#### Scenario: Registry mode rejects positional CIDRs

- **WHEN** `calc aggregate --from-registry 10.0.0.0/24` runs
- **THEN** it exits 2 with an error identifying the conflicting sources
  and performs no registry read

#### Scenario: Positional mode still requires input

- **WHEN** `calc aggregate` runs without positional CIDRs or registry mode
- **THEN** it exits 2 and stdout is empty

#### Scenario: Registry selectors require registry mode

- **WHEN** positional aggregation includes an explicit registry selector
  without enabling `--from-registry`
- **THEN** it exits 2 with an error naming `--from-registry`

#### Scenario: Explicit false preserves stateless aggregation

- **WHEN** `calc aggregate --from-registry=false 10.0.0.0/24` runs
- **THEN** it returns the positional aggregate without registry access

#### Scenario: Registry mode rejects CIDR flags

- **WHEN** `calc aggregate --from-registry --cidr 10.0.0.0/24` runs
- **THEN** it exits 2 with an error identifying the conflicting sources
  and performs no registry read

### Requirement: Calc next-free rejects exhausted pools and invalid target sizes

When no requested IPv4 or IPv6 child is available, `calc next-free` SHALL
exit 2, write the exhaustion message to stderr, and write nothing to stdout
in either output format, so the documented composition
`tsipam subnet reserve "$(tsipam calc next-free ... --format human)"` fails
at the composing command instead of passing text as a CIDR. Target widths shorter
than the canonical pool or greater than the address-family width SHALL be
usage errors.

#### Scenario: Exhausted pool exits 2 with nothing on stdout

- **WHEN** exclusions cover every requested child of the pool
- **THEN** the command exits 2 in both human and JSON formats, stdout is
  empty, and stderr names the missing block size and the pool

#### Scenario: Shell composition fails at the composing command

- **WHEN** the documented composition runs against an exhausted pool
- **THEN** `subnet reserve` receives an empty argument and fails with
  `subnet CIDR must not be empty`, with the exhaustion reason already on
  stderr from `calc next-free`

#### Scenario: IPv6 host pool can be exhausted

- **WHEN** a `/128` pool is entirely excluded
- **THEN** the command exits 2 with no suggestions

#### Scenario: IPv6 target width exceeds its address width

- **WHEN** an IPv6 pool is searched with `--size 129`
- **THEN** the command rejects the target with a usage error
