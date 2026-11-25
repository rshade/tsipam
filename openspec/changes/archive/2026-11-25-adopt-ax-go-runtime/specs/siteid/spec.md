# Site ID Specification Delta

## MODIFIED Requirements

### Requirement: Idempotent site-ID release

`tsipam site-id release <id>` SHALL remove the canonical ID's reservation
when present and report `changed=true`. Releasing an unreserved ID from an
existing registry SHALL succeed as a no-op with `changed=false`. A missing
registry file SHALL remain an unreadable-registry error rather than being
created by release. The
command SHALL require approval (see mutation-safety).

#### Scenario: Releasing a reserved ID removes it

- **WHEN** an operator releases a reserved site ID with `--yes`
- **THEN** the entry is removed, the result reports `changed=true`, and
  unrelated entries and comments remain unchanged

#### Scenario: Unapproved release changes nothing

- **WHEN** `site-id release 1 --format json` runs without `--yes`
- **THEN** it exits 2 and the registry bytes are unchanged

## ADDED Requirements

### Requirement: Site-ID commands can be rehearsed

`--dry-run` on `site-id reserve` and `site-id release` SHALL apply
every rule of the real command and report its outcome, and SHALL leave the
registry file, and its absence, unchanged.

#### Scenario: Rehearsed reserve

- **WHEN** `site-id reserve 1 hq --dry-run` runs against a registry without that entry
- **THEN** it reports `changed=true`, exits 0, and the registry bytes are
  unchanged

#### Scenario: Rehearsed no-op

- **WHEN** the same reservation already exists and `site-id reserve 1 hq --dry-run` runs
- **THEN** it reports `changed=false` and exits 0

#### Scenario: Rehearsed release of a missing registry

- **WHEN** `site-id release 1 --dry-run` runs and no registry file exists
- **THEN** it exits 2 and no file is created
