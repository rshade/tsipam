# Plan and Check Schema Delta

## ADDED Requirements

### Requirement: Discover registry comparisons and drift semantics

The schema SHALL include `plan` and `check` with their `registry-dir` and
`tailnet` flags. Help and schema SHALL explain that check drift returns exit
1, collection failures return exit 2, and comparison identities are sanitized.
Both single- and double-dash long flags SHALL work for these commands.

#### Scenario: An agent discovers comparison commands

- **WHEN** `tail-ipam --schema` runs
- **THEN** the commands array includes plan and check with both registry selectors
- **AND** exit-code 1 describes check drift as well as existing refusals
