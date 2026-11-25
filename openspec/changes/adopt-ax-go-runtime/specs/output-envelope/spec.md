# Output Envelope Specification Delta

## Purpose

Defines the JSON success document every leaf command writes, so a consumer can
parse any result, correlate it, and know which fields vary between runs.

## ADDED Requirements

### Requirement: JSON results are one enveloped line

In JSON mode a successful leaf command SHALL write exactly one minified JSON
line to stdout, an object with `data` and `meta`. `data` SHALL hold the
command's result object, with `schema_version` `"2"` and otherwise the field
names and meanings the command's specification defines.

#### Scenario: A list result is enveloped

- **WHEN** `tsipam site-id list --format json` runs against a registry holding
  ID 1 for `hq-vpc`
- **THEN** stdout is one line whose `.data.site_ids[0]` has `id` `"1"` and
  `site` `"hq-vpc"`, and `.data.schema_version` is `"2"`

#### Scenario: Other specifications name fields inside data

- **WHEN** another specification says a JSON result carries a field, such as
  `supernets`
- **THEN** that field is under `data`

### Requirement: Only leaf commands are enveloped

Here and in the mcp-server, mutation-safety, and schema specifications, a leaf
command SHALL mean a runnable command other than the root, `__schema`,
`mcp-server`, `help`, and `completion` with its subcommands. Those, and
`-schema`, `--version`, and `--help`, SHALL keep their own output forms.

#### Scenario: A completion script is not enveloped

- **WHEN** `tsipam completion bash` runs with stdout not a terminal
- **THEN** stdout is the shell script, with no `data` or `meta` object

### Requirement: Metadata identifies the run

`meta` SHALL carry `trace_id` and `span_id` for the run and
`idempotency_key`, and SHALL carry `dry_run: true` when the run is a
rehearsal and omit it otherwise.

#### Scenario: A rehearsal is marked

- **WHEN** `tsipam site-id reserve 1 hq --dry-run --format json` runs
- **THEN** `.meta.dry_run` is `true`

#### Scenario: A real run is not marked as a rehearsal

- **WHEN** `tsipam site-id reserve 1 hq --format json` runs
- **THEN** `.meta` has no `dry_run` member and has non-empty `trace_id` and
  `idempotency_key`

### Requirement: Run-varying fields are published

`__schema` SHALL list, for every leaf command, the result fields that vary
between identical runs: `meta.trace_id`, `meta.span_id`, and
`meta.idempotency_key` on all of them, and `data.reserved_at` on the commands
that can stamp it.

#### Scenario: Reserve publishes its timestamp

- **WHEN** the `__schema` entry for `site-id reserve` is read
- **THEN** its non-deterministic fields include `data.reserved_at` and the
  three `meta` fields

#### Scenario: Identical reads match after masking

- **WHEN** `tsipam site-id list --format json` runs twice against an unchanged
  registry and the published fields are masked
- **THEN** the two results are byte-identical

### Requirement: Warning escalation never hides a result

`--strict` SHALL NOT change a command's result, exit code, or failure
handling while the command has no warnings to escalate. No command emits a
warning today.

#### Scenario: Drift keeps its report under strict

- **WHEN** `tsipam check-policy policy.hujson --strict` finds drift
- **THEN** the report is on stdout and the command exits 1

#### Scenario: An output failure stays a failure under strict

- **WHEN** `tsipam subnet list --strict` cannot write to stdout
- **THEN** the command exits 2
