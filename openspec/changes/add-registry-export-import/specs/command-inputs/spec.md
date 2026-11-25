# Command Inputs Registry Transfer Delta

## ADDED Requirements

### Requirement: The import source has a flag

`registry import` SHALL accept its source by position or as `--source`, under
the same rules as every other required input. `-` SHALL mean stdin in both
forms.

#### Scenario: Source by flag

- **WHEN** `registry import --source snapshot.hujson` runs
- **THEN** its result, registry bytes, and exit code equal those of
  `registry import snapshot.hujson`
