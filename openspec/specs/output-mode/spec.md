# output-mode Specification

## Purpose

Defines how a command chooses between human text and JSON, so people, scripts,
and agents each get a predictable format from the same invocation.

## Requirements

### Requirement: Output mode resolves by flag, environment, then terminal

The output mode SHALL come from `--format` when it is set, else from
`AGENT_MODE` when it is set, else from stdout: human when stdout is a terminal
and JSON when it is not.

#### Scenario: Piped output defaults to JSON

- **WHEN** `tsipam site-id list` runs with stdout piped and neither `--format`
  nor `AGENT_MODE` set
- **THEN** stdout is one JSON document

#### Scenario: A terminal defaults to human output

- **WHEN** `tsipam site-id list` runs with stdout on a terminal and neither
  `--format` nor `AGENT_MODE` set
- **THEN** stdout is human text

#### Scenario: Agent mode selects JSON on a terminal

- **WHEN** a command runs on a terminal with `AGENT_MODE=1`
- **THEN** stdout is one JSON document

#### Scenario: Agent mode can keep human output in scripts

- **WHEN** a command runs with stdout piped and `AGENT_MODE=human`
- **THEN** stdout is human text

#### Scenario: The flag always wins

- **WHEN** a command runs with `--format human`, `AGENT_MODE=1`, and stdout
  piped
- **THEN** stdout is human text

### Requirement: Unknown output modes are rejected

`--format` SHALL accept `human` and `json` only. An unknown `--format` or
`AGENT_MODE` value SHALL fail with exit 2 and empty stdout before the command
reads or writes anything.

#### Scenario: Unknown format value

- **WHEN** `tsipam site-id reserve 1 hq --format bogus` runs
- **THEN** it exits 2 with empty stdout and creates no registry file

#### Scenario: Unknown agent-mode value

- **WHEN** a command runs with `AGENT_MODE=sometimes` and no `--format`
- **THEN** it exits 2 with empty stdout

### Requirement: Human rendering is independent of the runtime

Human output SHALL keep the text each command's own specification defines. A
scenario in any specification that shows human output without naming a mode
SHALL be read as running in human mode.

#### Scenario: Human list output is unchanged

- **WHEN** `tsipam subnet list --format human` runs against a registry holding
  `10.0.0.0/24` for `hq-vpc`
- **THEN** stdout is exactly `10.0.0.0/24<TAB>hq-vpc` and a newline
