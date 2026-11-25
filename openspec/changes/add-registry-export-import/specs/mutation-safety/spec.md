# Mutation Safety Registry Transfer Delta

## ADDED Requirements

### Requirement: Import can be rehearsed

`registry import` SHALL honor `--dry-run` as it honors `--plan`: the same
report, exit code, and empty set of changes, with `meta.dry_run` `true`. It
SHALL publish the side-effect class `mutate`, SHALL NOT require `--yes`, and
`registry export` SHALL publish `read-only`.

#### Scenario: Dry run equals plan

- **WHEN** `registry import snapshot.hujson --merge` runs once with `--plan` and
  once with `--dry-run`
- **THEN** `.data` is equal, both exit with the same code, and the registry
  bytes are unchanged
