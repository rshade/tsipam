# Schema Registry Aggregation Delta

## MODIFIED Requirements

### Requirement: Schema catalogs every command and its local flags

`tail-ipam -schema` SHALL print a JSON document listing every available
command as its full dotted path (for example `site-id reserve`), each
command's summary and usage line, and that command's own non-persistent
flags. Every registry-backed command (`site-id`, `subnet`, and `service`
subcommands, plus `calc aggregate` and `calc next-free`) SHALL include `registry-dir` and `tailnet` in its flag list.
Root-level flags (`format`, `sanitize`, `schema`, `verbose`, `version`) SHALL
appear in the document's top-level flags list.

#### Scenario: Schema output lists the full command catalog

- **WHEN** an operator runs `tail-ipam -schema`
- **THEN** the JSON document's `commands` array includes `devices`,
  `devices list`, `site-id`, `site-id list`, `site-id reserve`,
  `site-id release`, `subnet`, `subnet list`, `subnet reserve`,
  `subnet release`, `service`, `service list`, `service reserve`,
  `service release`, `calc`, `calc split`, and `calc info`

#### Scenario: Registry commands document their shared flags

- **WHEN** the schema document is inspected for any command whose name
  starts with `site-id`, `subnet`, or `service` followed by a subcommand
- **THEN** that command's flag list includes both `registry-dir` and
  `tailnet`

#### Scenario: Schema exposes registry aggregation

- **WHEN** the schema document is inspected for `calc aggregate`
- **THEN** it lists `from-registry`, `registry-dir`, and `tailnet`, and
  command help explains the mutually exclusive input sources

### Requirement: Schema documents exit codes and sanitization behavior

The schema document SHALL include exactly three exit-code entries (0, 1, 2)
with human-readable descriptions matching the tool's documented exit-code
scheme, and a `sanitization` field describing what `TAILSCALE_SANITIZE`
redacts. It SHALL distinguish exempt stateless calc output from sanitized
registry-derived aggregation and allocation-planning output.

#### Scenario: Exit codes are fully enumerated

- **WHEN** `tail-ipam -schema` runs
- **THEN** the `exit_codes` array contains one entry each for codes 0, 1, and
  2, with no duplicates and no gaps
