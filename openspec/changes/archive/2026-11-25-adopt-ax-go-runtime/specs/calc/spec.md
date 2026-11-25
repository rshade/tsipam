# Calc Specification Delta

## MODIFIED Requirements

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
