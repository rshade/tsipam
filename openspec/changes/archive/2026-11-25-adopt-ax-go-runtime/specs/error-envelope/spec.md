# Error Envelope Specification Delta

## Purpose

Defines the JSON failure document and its exit code, so automation can branch
on a stable code and never mistake an unknown answer for a verdict.

## ADDED Requirements

### Requirement: Every failure is one error object on stderr

A command that exits non-zero SHALL write exactly one error object, as one
JSON line, to stderr in both output modes. The object SHALL carry
`error_code`, `message`, `trace_id`, `tool`, `version`, and `schema_version`.
Other stderr lines, such as logs and the sanitization notice, MAY appear.

#### Scenario: A human-mode failure is still JSON

- **WHEN** `tsipam calc info not-a-cidr --format human` runs
- **THEN** it exits 2, stdout is empty, and stderr has one error object whose
  `message` names `not-a-cidr`

### Requirement: Only a verdict leaves a result on stdout

A failed command SHALL write nothing to stdout, except that `check` and
`check-policy` SHALL write their complete report before failing on drift.

#### Scenario: A collision leaves stdout empty

- **WHEN** a reserve is refused as a collision
- **THEN** stdout is empty and the command exits 1

#### Scenario: Drift keeps its report

- **WHEN** `check` finds drift
- **THEN** the report is on stdout, the error object is on stderr, and the
  command exits 1

### Requirement: Error codes are stable and fix the exit code

`error_code` SHALL be one of a documented set. `registry_collision`,
`split_too_large`, `drift_detected`, and `policy_drift_detected` SHALL exit 1.
`validation_error`, `confirmation_required`, `pool_exhausted`,
`registry_failed`, `credentials_missing`, `collection_failed`,
`comparison_failed`, `policy_check_failed`, `output_failed`, `canceled`, and
`command_failed` SHALL exit 2.

#### Scenario: Each failure class has its code

- **WHEN** each of these runs: an unknown flag, an unknown command, a wrong
  argument count, a missing required flag, a malformed CIDR, a collision, an
  oversized split, an exhausted pool, a missing registry, missing
  credentials, a failed API read, `check` drift, `check-policy` drift, an
  unresolvable policy, an unconfirmed release, and a failed stdout write
- **THEN** each reports the code and exit status listed above for that class

### Requirement: An unknown answer never exits 1

A failure that is not one of the four verdict codes SHALL exit 2, whichever
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

### Requirement: Errors carry recovery guidance

A collision, an exhausted pool, missing credentials, and an unconfirmed
release SHALL carry `actionable_fix`. A drift verdict SHALL carry
`drift_count` in `context`. `retryable` SHALL be `true` only when a live read
failed by timeout or with an HTTP 5xx status.

#### Scenario: A collision suggests a next step

- **WHEN** `site-id reserve` is refused as a collision
- **THEN** the error's `actionable_fix` names releasing the ID or choosing
  another

#### Scenario: Server errors are retryable

- **WHEN** `devices list` fails with HTTP 503
- **THEN** the error's `retryable` is `true`

#### Scenario: Authorization failures are not retryable

- **WHEN** `devices list` fails with HTTP 403
- **THEN** the error has no `retryable: true`

### Requirement: Error objects are sanitized

With sanitization active, no site, service, device, owner, CIDR, address,
path, or tailnet value SHALL appear in any field of the error object. This
SHALL hold for a failure raised before the command body runs, including one
that would echo a raw argument. CIDRs supplied to `calc split`, `calc info`,
and `calc aggregate` without `--from-registry` are exempt once the command
body runs, because the calc specification exempts those commands.

#### Scenario: A sanitized collision hides both sites

- **WHEN** a site-ID collision between `hq-secret` and `branch-secret` runs
  with `TAILSCALE_SANITIZE=identity`
- **THEN** stderr contains neither name, and `error_code` is
  `registry_collision`

#### Scenario: A usage error hides a stray argument

- **WHEN** `tsipam site-id list hq-secret` runs with
  `TAILSCALE_SANITIZE=identity`
- **THEN** it exits 2 and stderr does not contain `hq-secret`

### Requirement: An unknown tool argument's name is not redacted

The MCP runtime refuses a call that passes an argument the tool does not have
before `tsipam` sees the call. Its error object MAY repeat that argument's name
with sanitization active, and SHALL NOT repeat the argument's value.

#### Scenario: Unknown argument

- **WHEN** a client calls `tsipam-calc-info` with an argument named `hq-secret`
  and value `branch-secret` under `TAILSCALE_SANITIZE=identity`
- **THEN** the call fails with `validation_error` and the error object does not
  contain `branch-secret`
