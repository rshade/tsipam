# Schema Specification Delta

## ADDED Requirements

### Requirement: Schema documents the server and remote selection

`tsipam -schema` SHALL list `serve` with its `registry`, `service`,
`hostname`, `advertise-tags`, `state-dir`, and `health-listen` flags. Every
registry-backed command SHALL list a `server` flag beside its registry
selectors. Help and schema SHALL state that `--server` excludes
`--registry` and `--registry-dir`, and SHALL name `TSIPAM_SERVER`.

#### Scenario: The serve command is discoverable

- **WHEN** an operator runs `tsipam -schema`
- **THEN** the `commands` array includes `serve`, and its flag list
  includes `registry`, `advertise-tags`, and `state-dir`

#### Scenario: Registry commands advertise the server flag

- **WHEN** the schema document is inspected for `subnet allocate`, `plan`,
  or `check-policy`
- **THEN** each flag list includes `server`

### Requirement: Exit code 2 covers remote failures

The schema SHALL keep exactly three exit codes. The description of exit
code 2 SHALL include denied requests, an unreachable server, TLS failures,
and missing server-side live-read configuration. The description of exit
code 1 SHALL be unchanged.

#### Scenario: Exit-code catalog stays at three entries

- **WHEN** `tsipam -schema` runs after this change
- **THEN** `exit_codes` holds entries for 0, 1, and 2 only, and the entry
  for 2 mentions access denial and an unreachable server
