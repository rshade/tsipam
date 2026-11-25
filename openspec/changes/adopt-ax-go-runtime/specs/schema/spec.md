# Schema Specification Delta

## MODIFIED Requirements

### Requirement: Schema catalogs every command and its local flags

`tsipam -schema` SHALL print a JSON document listing every available
command as its full dotted path (for example `site-id reserve`), each
command's summary and usage line, and that command's own non-persistent
flags. Every registry-backed command (`site-id`, `subnet`, and `service`
subcommands, plus `calc aggregate` and `calc next-free`) SHALL include `registry-dir` and `tailnet` in its flag list.
Every flag that stands in for a positional argument SHALL be listed on its
command. Root-level flags (`dry-run`, `format`, `idempotency-key`, `sanitize`,
`schema`, `strict`, `verbose`, `version`, `yes`) SHALL appear in the document's
top-level flags list.

#### Scenario: Schema output lists the full command catalog

- **WHEN** an operator runs `tsipam -schema`
- **THEN** the JSON document's `commands` array includes `devices`,
  `devices list`, `site-id`, `site-id list`, `site-id reserve`,
  `site-id release`, `subnet`, `subnet list`, `subnet reserve`,
  `subnet release`, `subnet allocate`, `service`, `service list`,
  `service reserve`, `service release`, `calc`, `calc split`,
  `calc info`, `mcp-server`, and `__schema`

#### Scenario: Registry commands document their shared flags

- **WHEN** the schema document is inspected for any command whose name
  starts with `site-id`, `subnet`, or `service` followed by a subcommand
- **THEN** that command's flag list includes both `registry-dir` and
  `tailnet`

#### Scenario: Schema exposes registry aggregation

- **WHEN** the schema document is inspected for `calc aggregate`
- **THEN** it lists `cidr`, `from-registry`, `registry-dir`, and `tailnet`, and
  command help explains the mutually exclusive input sources

#### Scenario: Schema exposes subnet allocation

- **WHEN** the schema document is inspected for `subnet allocate`
- **THEN** it lists `pool`, `site`, `size`, `owner`, `description`, `ticket`,
  `reserved-at`, `registry-dir`, and `tailnet`, `size` is marked required,
  and its usage line reads `subnet allocate <pool-cidr> <site-name>`

#### Scenario: Input flags are cataloged

- **WHEN** the schema document is inspected for `site-id reserve` and
  `check-policy`
- **THEN** the first lists `id` and `site`, the second lists `policy-file`, and
  none of them is marked required

## ADDED Requirements

### Requirement: A second catalog follows the portfolio contract

`tsipam __schema` SHALL print the command tree with each flag's type,
default, and allowed values, each leaf command's non-deterministic fields and
side-effect class, and the error-envelope contract. `tsipam __schema --as=mcp`
SHALL print the MCP tool list. Both SHALL exit 0 with one JSON document.

#### Scenario: Every leaf is described

- **WHEN** `tsipam __schema` runs
- **THEN** every leaf command has non-deterministic fields and a side-effect
  class, and `format` lists `human` and `json` as its allowed values

#### Scenario: MCP adapter

- **WHEN** `tsipam __schema --as=mcp` runs
- **THEN** it lists the tools the MCP server serves

#### Scenario: Unknown adapter

- **WHEN** `tsipam __schema --as=bogus` runs
- **THEN** it exits 2 with empty stdout

### Requirement: The original catalog stays supported

`tsipam -schema` SHALL remain a supported, undeprecated surface with
`schema_version` `"1"`. It is the only catalog that carries `exit_codes` and
`sanitization`, and its document SHALL NOT be wrapped in the result envelope.

#### Scenario: The original catalog keeps its shape

- **WHEN** `tsipam -schema` runs with stdout piped
- **THEN** stdout is the unwrapped document with top-level `exit_codes` and
  `sanitization`
