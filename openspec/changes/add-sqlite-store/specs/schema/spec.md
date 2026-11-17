# Schema SQLite Store Delta

## ADDED Requirements

### Requirement: Schema documents the registry backend selector

Every registry-backed command in the `tsipam -schema` document SHALL list a
`registry` flag alongside `registry-dir` and `tailnet`. Its description SHALL
name the `file://` and `sqlite://` schemes, the `TSIPAM_REGISTRY`
environment variable, and that `--registry` and `--registry-dir` are mutually
exclusive.

#### Scenario: Registry commands list the backend selector

- **WHEN** the schema document is inspected for `site-id reserve`,
  `subnet allocate`, `service list`, `plan`, and `check-policy`
- **THEN** each command's flag list includes `registry`, `registry-dir`, and
  `tailnet`

### Requirement: Schema exposes registry history

The `tsipam -schema` command catalog SHALL include `registry history` with its
`kind`, `key`, `since`, `limit`, `registry`, `registry-dir`, and `tailnet`
flags, and its help SHALL state that it requires the sqlite backend.

#### Scenario: An agent discovers history

- **WHEN** `tsipam -schema` runs
- **THEN** the `commands` array includes `registry history` with the `since`
  and `limit` flags
