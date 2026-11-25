# Service Address Reservations Specification Delta

## MODIFIED Requirements

### Requirement: Idempotent service-address release

`tsipam service release <addr>` and the corresponding API operation SHALL
remove the canonical address reservation when present and report
`changed=true`. Releasing an unreserved address from an existing registry
SHALL succeed as a no-op with `changed=false`. A missing registry file SHALL
remain an unreadable-registry error rather than being created by release. The
command SHALL require approval (see mutation-safety).

#### Scenario: Existing reservation is released

- **WHEN** an operator releases a reserved address with `--yes`, using either
  its bare or exact `/32` spelling
- **THEN** the canonical entry is removed, the result reports `changed=true`,
  and unrelated entries and comments remain unchanged

#### Scenario: Absent reservation is a no-op

- **WHEN** an operator releases, with `--yes`, a valid address that is not
  reserved in an existing registry
- **THEN** the operation exits 0, reports `changed=false`, and leaves the file
  unchanged

#### Scenario: Unapproved release changes nothing

- **WHEN** `service release 100.100.100.7 --format json` runs without `--yes`
- **THEN** it exits 2 and the registry bytes are unchanged

## ADDED Requirements

### Requirement: Service commands can be rehearsed

`--dry-run` on `service reserve` and `service release` SHALL apply
every rule of the real command and report its outcome, and SHALL leave the
registry file, and its absence, unchanged.

#### Scenario: Rehearsed reserve

- **WHEN** `service reserve 100.100.100.7 payments --dry-run` runs against a registry without that entry
- **THEN** it reports `changed=true`, exits 0, and the registry bytes are
  unchanged

#### Scenario: Rehearsed no-op

- **WHEN** the same reservation already exists and `service reserve 100.100.100.7 payments --dry-run` runs
- **THEN** it reports `changed=false` and exits 0

#### Scenario: Rehearsed release of a missing registry

- **WHEN** `service release 100.100.100.7 --dry-run` runs and no registry file exists
- **THEN** it exits 2 and no file is created
