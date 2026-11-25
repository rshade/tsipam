# Mutation Safety Specification Delta

## Purpose

Lets people and agents preview a registry write, and requires approval before
a reservation is removed, without weakening the registry's own guarantees.

## ADDED Requirements

### Requirement: Dry run rehearses without writing

`--dry-run` on `site-id`, `subnet`, `service`, and `port` `reserve` and
`release`, and on `subnet allocate`, SHALL apply the same validation,
collision, allocation, and ticket-replay rules as a real run and report the
outcome it would have. It SHALL create, change, or remove no file, including
the registry, its lock, and an absent registry.

#### Scenario: Rehearsing a new reservation

- **WHEN** `site-id reserve 1 hq --dry-run` runs and no registry file exists
- **THEN** it exits 0, reports `changed=true`, and the directory is unchanged

#### Scenario: A rehearsed collision is still refused

- **WHEN** ID 1 belongs to `hq` and `site-id reserve 1 branch --dry-run` runs
- **THEN** it exits 1 with `error_code` `registry_collision`

#### Scenario: A rehearsed release reports what it would remove

- **WHEN** ID 1 is reserved and `site-id release 1 --dry-run` runs
- **THEN** it exits 0, reports `changed=true`, and the registry bytes are
  unchanged

#### Scenario: A registry directory that cannot be written can be rehearsed

- **WHEN** a reserve runs with `--dry-run` against a readable registry in a
  read-only directory
- **THEN** it reports the would-be outcome

### Requirement: A rehearsal says so

In human mode a rehearsed message SHALL begin with `dry run: would`. In JSON
mode `meta.dry_run` SHALL be `true`, and `data` SHALL have the fields a real
run would.

#### Scenario: Human rehearsal message

- **WHEN** `site-id reserve 1 hq --dry-run --format human` runs
- **THEN** stdout begins with `dry run: would reserve site ID 1`

### Requirement: A rehearsal is advisory

A rehearsal SHALL NOT take the registry lock or hold an allocation. A later
real run SHALL decide again from the registry it reads.

#### Scenario: A rehearsed block is not held

- **WHEN** `subnet allocate` is rehearsed, another caller allocates the same
  block, and the first caller then runs it for real
- **THEN** the real run returns a different block

### Requirement: Commands that write declare it

Every leaf command that can change the registry SHALL honor `--dry-run` and
SHALL publish a side-effect class in `__schema`: `create` for allocate,
`mutate` for reserve, and `delete` for release. Every other leaf command SHALL
publish `read-only`, and `--dry-run` SHALL NOT change its result beyond
`meta.dry_run`.

#### Scenario: Release is published as a delete

- **WHEN** the `__schema` entry for `subnet release` is read
- **THEN** its capability class is `delete`

#### Scenario: Dry run on a read-only command

- **WHEN** `site-id list --dry-run --format json` runs
- **THEN** `.data` equals the result without the flag

### Requirement: Release requires approval

The four `release` commands SHALL require `--yes`, except under `--dry-run`.
Without it they SHALL prompt on stderr when the mode is human and stdin is a
terminal, and proceed only on an answer of `y` or `yes`. Otherwise they SHALL
exit 2 with `confirmation_required` and leave the registry unchanged.

#### Scenario: JSON mode without approval

- **WHEN** `site-id release 1 --format json` runs without `--yes`
- **THEN** it exits 2 with `error_code` `confirmation_required`, stdout is
  empty, and the registry bytes are unchanged

#### Scenario: Approved release

- **WHEN** `site-id release 1 --yes` runs
- **THEN** the reservation is removed as the command's specification defines

#### Scenario: Declined prompt

- **WHEN** a person answers `n`, or stdin ends, at the prompt
- **THEN** the command exits 2 with `confirmation_required` and the registry
  bytes are unchanged

#### Scenario: No prompt without a terminal

- **WHEN** `site-id release 1 --format human` runs with stdin not a terminal
- **THEN** it exits 2 with `confirmation_required` without prompting

### Requirement: Additive commands need no approval

`reserve` and `allocate` SHALL NOT require `--yes`.

#### Scenario: Unattended reserve

- **WHEN** `site-id reserve 1 hq --format json` runs without `--yes`
- **THEN** it reserves the ID and exits 0

### Requirement: The idempotency key is echoed, not stored

`--idempotency-key` SHALL be returned in `meta.idempotency_key`, and a fresh
key SHALL be generated when it is absent. It SHALL NOT be written to the
registry or change an outcome. Retry safety SHALL come from the reservation's
own key, and from `--ticket` for `subnet allocate`.

#### Scenario: Supplied key is echoed

- **WHEN** a reserve runs with `--idempotency-key job-42 --format json`
- **THEN** `.meta.idempotency_key` is `job-42` and the registry does not
  contain `job-42`

#### Scenario: The same key does not replay an allocation

- **WHEN** `subnet allocate` runs twice with the same `--idempotency-key` and
  no `--ticket`
- **THEN** the two runs reserve different blocks
