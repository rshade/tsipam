# Error Envelope Registry Transfer Delta

## MODIFIED Requirements

### Requirement: Only a verdict leaves a result on stdout

A failed command SHALL write nothing to stdout, except that `check` and
`check-policy` SHALL write their complete report before failing on drift, and
`registry import` with `--plan` or `--dry-run` SHALL write its complete report
before failing on a refusal.

#### Scenario: A collision leaves stdout empty

- **WHEN** a reserve is refused as a collision
- **THEN** stdout is empty and the command exits 1

#### Scenario: Drift keeps its report

- **WHEN** `check` finds drift
- **THEN** the report is on stdout, the error object is on stderr, and the
  command exits 1

#### Scenario: A refused plan keeps its report

- **WHEN** `registry import snapshot.hujson` would be refused, and runs with
  `--plan` or with `--dry-run`
- **THEN** the report is on stdout, the error object, with `error_code`
  `import_refused`, is on stderr, and the command exits 1

#### Scenario: A refused import leaves stdout empty

- **WHEN** `registry import snapshot.hujson` is refused
- **THEN** stdout is empty and the command exits 1

### Requirement: Error codes are stable and fix the exit code

`error_code` SHALL be one of a documented set. `registry_collision`,
`split_too_large`, `drift_detected`, `policy_drift_detected`, and
`import_refused` SHALL exit 1.
`validation_error`, `confirmation_required`, `pool_exhausted`,
`registry_failed`, `credentials_missing`, `collection_failed`,
`comparison_failed`, `policy_check_failed`, `output_failed`, `canceled`, and
`command_failed` SHALL exit 2.

#### Scenario: Each failure class has its code

- **WHEN** each of these runs: an unknown flag, an unknown command, a wrong
  argument count, a missing required flag, a malformed CIDR, a collision, an
  oversized split, an exhausted pool, a missing registry, missing
  credentials, a failed API read, `check` drift, `check-policy` drift, an
  unresolvable policy, a refused import, an unconfirmed release, and a failed
  stdout write
- **THEN** each reports the code and exit status listed above for that class

### Requirement: An unknown answer never exits 1

A failure that is not one of the five verdict codes SHALL exit 2, whichever
stage of command processing raised it. No failure SHALL report the generic
code `internal_error`, and no failure SHALL exit 3 or 4.

#### Scenario: A missing required flag is a usage error

- **WHEN** `tsipam calc split 10.0.0.0/24` runs without `--prefix`
- **THEN** it exits 2 with `error_code` `validation_error`

#### Scenario: A built-in command's usage error is covered

- **WHEN** `tsipam completion bash extra` runs
- **THEN** it exits 2 with `error_code` `validation_error`

#### Scenario: An API timeout is not a distinct exit code

- **WHEN** `devices list` times out
- **THEN** it exits 2 with `error_code` `collection_failed`

#### Scenario: An unreadable import source is not a refusal

- **WHEN** `registry import` reads a source that fails validation
- **THEN** it exits 2 with `error_code` `validation_error`
