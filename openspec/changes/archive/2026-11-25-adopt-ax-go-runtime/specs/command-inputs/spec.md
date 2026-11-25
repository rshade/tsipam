# Command Inputs Specification Delta

## Purpose

Lets every command be called with flags alone, so a caller that can only pass
named values, such as an MCP client, reaches the whole CLI.

## ADDED Requirements

### Requirement: Every positional argument has a flag

Each required positional argument SHALL also be accepted as a flag: `--id`
and `--site` (`site-id`); `--cidr`, `--pool`, and `--site` (`subnet`);
`--address` and `--service` (`service`); `--port` and `--device` (`port`);
`--cidr` (`calc split`, `calc info`); `--pool` (`calc next-free`); a
repeatable `--cidr` (`calc aggregate`); and `--policy-file` (`check-policy`).

#### Scenario: Flags alone are equivalent to positions

- **WHEN** `subnet reserve --cidr 10.0.0.0/24 --site hq` runs
- **THEN** its result, registry bytes, and exit code equal those of
  `subnet reserve 10.0.0.0/24 hq`

#### Scenario: Repeated values

- **WHEN** `calc aggregate --cidr 10.0.0.0/25 --cidr 10.0.0.128/25` runs
- **THEN** it returns `10.0.0.0/24`

#### Scenario: Single-dash spelling

- **WHEN** `site-id reserve -id 1 -site hq` runs
- **THEN** it behaves as the double-dash spelling does

### Requirement: Inputs come from one source

A command SHALL take its required values all by position or all by flag.
Supplying some of each SHALL fail with exit 2 before any registry, policy, or
live access.

#### Scenario: Mixed sources are refused

- **WHEN** `site-id reserve 1 --site hq` runs
- **THEN** it exits 2 with `error_code` `validation_error` and creates no file

### Requirement: Missing inputs are named

When a required value is missing, the command SHALL exit 2 with empty stdout
and an error that names each missing input and its flag, before any registry,
policy, or live access. An empty value SHALL reach the command's own
validation.

#### Scenario: One input missing

- **WHEN** `site-id reserve --id 1` runs
- **THEN** it exits 2 and the message names `--site`

#### Scenario: Empty positional value

- **WHEN** `subnet reserve "" hq` runs
- **THEN** it exits 2 and the message is `subnet CIDR must not be empty`

### Requirement: Flag inputs are sanitized like positions

A value supplied by flag SHALL be validated and redacted exactly as the same
value supplied by position.

#### Scenario: A sanitized flag collision

- **WHEN** `site-id reserve --id 1 --site branch-secret` collides under
  `--sanitize identity`
- **THEN** stderr does not contain `branch-secret`
