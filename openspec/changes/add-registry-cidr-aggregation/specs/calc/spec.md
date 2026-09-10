# Calc Registry Aggregation Delta

## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Aggregate subnet reservations from the selected registry

`tail-ipam calc aggregate --from-registry` SHALL summarize all subnet-pool
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

`--from-registry` SHALL accept no positional CIDRs. Without registry mode,
aggregate SHALL require at least one positional CIDR and SHALL reject
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
