# Subnet Specification Delta

## MODIFIED Requirements

### Requirement: Idempotent subnet-pool release

`tsipam subnet release <cidr>` SHALL remove the canonical CIDR's
reservation when present and report `changed=true`. Releasing an unreserved
CIDR from an existing registry SHALL succeed as a no-op with
`changed=false`. A missing registry file SHALL remain an unreadable-registry
error rather than being created by release. The
command SHALL require approval (see mutation-safety).

#### Scenario: Reserve, list, then release round-trips

- **WHEN** an operator reserves `10.0.0.0/24` for `hq-vpc`, lists subnet
  pools, then releases `10.0.0.0/24` with `--yes`
- **THEN** list shows the reservation before release, and release reports
  `changed=true` and confirms the CIDR is released

#### Scenario: Unapproved release changes nothing

- **WHEN** `subnet release 10.0.0.0/24 --format json` runs without `--yes`
- **THEN** it exits 2 and the registry bytes are unchanged

## ADDED Requirements

### Requirement: Subnet commands can be rehearsed

`--dry-run` on `subnet reserve`, `subnet allocate`, and `subnet release` SHALL apply
every rule of the real command and report its outcome, and SHALL leave the
registry file, and its absence, unchanged.

#### Scenario: Rehearsed reserve

- **WHEN** `subnet reserve 10.0.0.0/24 hq --dry-run` runs against a registry without that entry
- **THEN** it reports `changed=true`, exits 0, and the registry bytes are
  unchanged

#### Scenario: Rehearsed no-op

- **WHEN** the same reservation already exists and `subnet reserve 10.0.0.0/24 hq --dry-run` runs
- **THEN** it reports `changed=false` and exits 0

#### Scenario: Rehearsed release of a missing registry

- **WHEN** `subnet release 10.0.0.0/24 --dry-run` runs and no registry file exists
- **THEN** it exits 2 and no file is created

#### Scenario: Rehearsed allocation

- **WHEN** `subnet allocate 10.0.0.0/16 --size 24 hq --dry-run` runs with a
  live device read available
- **THEN** it reports the block a real run would reserve from the same registry
  and routes, and no reservation is written

#### Scenario: Rehearsed ticket replay

- **WHEN** a reservation for `hq` with ticket `OPS-1` exists in the pool and the
  same allocation runs with `--ticket OPS-1 --dry-run`
- **THEN** it reports that reservation with `changed=false`

#### Scenario: Rehearsed exhaustion creates nothing

- **WHEN** a rehearsed allocation finds no free block and no registry exists
- **THEN** it exits 2 and no registry file is created
