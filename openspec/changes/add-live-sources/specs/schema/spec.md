# Schema Specification Delta

## ADDED Requirements

### Requirement: Schema catalogs live sources

`tsipam -schema` SHALL list `source`, `source add`, `source remove`, and
`source list`, each leaf with `registry-dir` and `tailnet`, and SHALL list `only`
on `plan` and `check`. `__schema` SHALL publish `mutate` for add, `delete` for
remove, and `read-only` for list.

#### Scenario: Source commands are discoverable

- **WHEN** the schema document is inspected
- **THEN** it lists the four source entries, and `check` lists `only`
