# Policy Check Specification

## Purpose

Compare locally authored ipset address space with declared subnet pools before deployment, independently of live state.

## Requirements

### Requirement: Offline comparison

The tool SHALL provide `check-policy <acl-file>` with the existing registry directory and tailnet selection flags.
It SHALL read local HuJSON policy and one subnet-pool registry snapshot without live credentials or API collection,
and SHALL leave policy and registry bytes unchanged. Unrelated registry allocation maps SHALL not affect comparison.
All ipset definitions SHALL participate, regardless of whether ACLs or grants reference them.

#### Scenario: No live configuration

- **WHEN** valid local policy and registry files are supplied without Tailscale credentials
- **THEN** the command completes the comparison without live requests or file mutation

### Requirement: Explicit static resolution

The tool SHALL resolve IPv4 and IPv6 addresses, CIDRs, address ranges, policy `host:` aliases, nested `ipset:`
references, and ordered add/remove operations. It SHALL canonicalize effective CIDRs and remove exact duplicate
memberships without merging adjacent authored additions. When removals occur, every member SHALL have an explicit
operation. Only effective addresses remaining after all operations SHALL contribute references.
An absent ipsets object SHALL mean no authored references; an empty set SHALL be valid.
Unsupported selectors including `autogroup:internet`, malformed relevant definitions, ambiguous duplicates,
missing references, cycles, or exceeded expansion limits SHALL fail the entire check without a partial report.

#### Scenario: Ordered subtraction and re-addition

- **WHEN** a set adds a subnet, removes it, and then adds a host inside it
- **THEN** only the final host prefix participates in that set's comparison

#### Scenario: Unresolvable selector

- **WHEN** any ipset contains `autogroup:internet` or references a missing host or ipset
- **THEN** the command exits 2, explains the unresolved input on stderr, and emits no report

### Requirement: Bidirectional matching

An effective authored CIDR SHALL be `matched` if at least one reserved pool contains its entire address space;
otherwise it SHALL be `authored_but_unreserved`. Adjacent reservations SHALL not combine to satisfy containment.
A pool SHALL be `matched` if any effective authored CIDR overlaps it; otherwise it SHALL be
`reserved_but_unreferenced`. IPv4-mapped prefixes SHALL compare with their IPv4 equivalents while retaining native
IPv6 containment. The report SHALL identify the defining ipset, canonical CIDR, and containing pools for each
authored row, and the CIDR, site, and referencing ipsets for each reserved row.

#### Scenario: Contained subnet

- **WHEN** `ipset:office` contains `192.0.2.0/25` and the registry reserves `192.0.2.0/24`
- **THEN** both the authored CIDR and reserved pool are matched

#### Scenario: Broader authored prefix

- **WHEN** a set contains `192.0.2.0/24` and two adjacent `/25` reservations cover it
- **THEN** the authored `/24` is unreserved and both reservations are referenced

#### Scenario: Drift in both directions

- **WHEN** a set contains `198.51.100.0/24` and the registry only reserves `192.0.2.0/24`
- **THEN** one authored row is unreserved and one reserved row is unreferenced

### Requirement: Deterministic output and exit contract

Human and JSON output SHALL include matched and unmatched entries and a count of unmatched rows across both
directions. JSON SHALL use the existing envelope with an additive `policy_comparison` object containing
`drift_count`, `authored`, and `reserved`; evidence arrays SHALL always be present, including when empty.
Output SHALL be deterministic across input map ordering. The command SHALL exit 0 for a consistent comparison,
1 for drift after rendering the report, and 2 for invocation, read, parse, comparison, or output errors.
Help and schema SHALL describe the command, scope, and exit behavior.

#### Scenario: Empty comparison

- **WHEN** neither input contains relevant allocations
- **THEN** output has empty authored and reserved arrays, zero drift, and exit status 0

### Requirement: Identity sanitization

The command SHALL honor existing environment and flag sanitization precedence, replacing registry aliases,
ipset names, CIDRs, and site labels in both output forms. Counts and classifications SHALL remain visible.
Diagnostics SHALL not disclose raw policy members or definition names when sanitization is enabled.

#### Scenario: Sanitized drift

- **WHEN** drift is rendered with identity or strict sanitization
- **THEN** all identities in rows and evidence are replaced consistently and drift remains detectable
