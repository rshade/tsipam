# Policy Check Specification Delta

## MODIFIED Requirements

### Requirement: Deterministic output and exit contract

Human and JSON output SHALL include matched and unmatched entries and a count of unmatched rows across both
directions. JSON SHALL use the existing envelope with an additive `policy_comparison` object containing
`drift_count`, `authored`, and `reserved`; evidence arrays SHALL always be present, including when empty.
Output SHALL be deterministic across input map ordering. The command SHALL exit 0 for a consistent comparison,
1 for drift after rendering the report, and 2 for invocation, read, parse, comparison, or output errors.
The drift failure SHALL be an error object on stderr with `error_code` `policy_drift_detected` and the drift count in
its `context`. Help and schema SHALL describe the command, scope, and exit behavior.

#### Scenario: Empty comparison

- **WHEN** neither input contains relevant allocations
- **THEN** output has empty authored and reserved arrays, zero drift, and exit status 0

#### Scenario: The verdict is machine-readable

- **WHEN** `check-policy --format json` finds two drifting rows
- **THEN** stdout holds the report, the error object's `context.drift_count` is 2, and the command exits 1
