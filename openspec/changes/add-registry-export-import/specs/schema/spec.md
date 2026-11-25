# Schema Registry Transfer Delta

## ADDED Requirements

### Requirement: Schema exposes registry export and import

`tsipam -schema` SHALL list `registry`, `registry export`, and `registry
import`. `registry export` SHALL list `output`, `registry`, `registry-dir`, and
`tailnet`. `registry import` SHALL list `source`, `merge`, `plan`,
`drop-unknown`, `registry`, `registry-dir`, and `tailnet`, with usage line
`registry import <file>`. The exit-code 1
description SHALL cover an import refused or planned as refused.

#### Scenario: An agent discovers registry transfer

- **WHEN** an operator runs `tsipam -schema`
- **THEN** the `commands` array includes `registry export` and `registry
  import` with the flags above
- **AND** the exit-code 1 description mentions import refusals

#### Scenario: Sanitization text covers transfer output

- **WHEN** the schema document's `sanitization` field is read
- **THEN** it states that exported documents and import reports are
  sanitized like registry list output
