# Schema Specification Delta

## ADDED Requirements

### Requirement: Schema catalogs address spaces

`tsipam -schema` SHALL list `space`, `space list`, `space reserve`,
`space release`, `space map`, and `space unmap`, each leaf with `registry-dir`
and `tailnet`. It SHALL list `space` on the four `subnet` leaves, `calc next-free`,
and `calc aggregate`, and `usage` on `subnet reserve` and `subnet allocate`.

#### Scenario: Space commands are discoverable

- **WHEN** the schema document is inspected
- **THEN** it lists the six space entries, and `subnet reserve` lists `space` and
  `usage`

### Requirement: Schema explains refusals and external space

The catalog's exit-code text SHALL say that releasing a space or site ID in use
exits 1, and the help of `plan`, `check`, and `check-policy` SHALL say that
external pools are not drift.

#### Scenario: Refusals are documented

- **WHEN** the schema's `exit_codes` are read
- **THEN** the description of 1 covers a space or site ID in use
