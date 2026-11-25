# UDP Port Specification Delta

## MODIFIED Requirements

### Requirement: Idempotent per-site release

`tsipam port release <port> --site <site>` and the corresponding API
operation SHALL remove only the canonical `(site, port)` reservation and
report `changed=true` when present. Releasing an unreserved port or an unknown
site from an existing valid registry SHALL succeed as a no-op with
`changed=false` and SHALL not rewrite the file. A missing registry file SHALL
remain an unreadable-registry error and SHALL not be created by release. The
command SHALL require approval (see mutation-safety).

#### Scenario: Existing reservation is released

- **WHEN** an operator releases port `41642` from `branch-office` with
  `--yes`
- **THEN** that reservation is removed, `changed=true` is reported, and
  reservations in other ports and sites remain unchanged

#### Scenario: Absent site-port pair is a no-op

- **WHEN** an operator releases, with `--yes`, a valid `(site, port)` pair that
  is not reserved in an existing registry
- **THEN** the operation exits 0, reports `changed=false`, and leaves the file
  byte-for-byte unchanged

#### Scenario: Release does not create a registry

- **WHEN** an operator releases a port with `--yes` and the selected tailnet
  registry does not exist
- **THEN** the operation fails as an unreadable-registry error and no file is
  created

#### Scenario: Unapproved release changes nothing

- **WHEN** `port release 41642 --site branch-office --format json` runs without
  `--yes`
- **THEN** it exits 2 and the registry bytes are unchanged

## ADDED Requirements

### Requirement: Port commands can be rehearsed

`--dry-run` on `port reserve` and `port release` SHALL apply
every rule of the real command and report its outcome, and SHALL leave the
registry file, and its absence, unchanged.

#### Scenario: Rehearsed reserve

- **WHEN** `port reserve 41642 fw01 --site hq --dry-run` runs against a registry without that entry
- **THEN** it reports `changed=true`, exits 0, and the registry bytes are
  unchanged

#### Scenario: Rehearsed no-op

- **WHEN** the same reservation already exists and `port reserve 41642 fw01 --site hq --dry-run` runs
- **THEN** it reports `changed=false` and exits 0

#### Scenario: Rehearsed release of a missing registry

- **WHEN** `port release 41642 --site hq --dry-run` runs and no registry file exists
- **THEN** it exits 2 and no file is created
