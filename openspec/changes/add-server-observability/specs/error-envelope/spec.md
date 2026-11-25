# Error Envelope Drift Delta

## ADDED Requirements

### Requirement: An unavailable drift report has a code

The documented set of `error_code` values SHALL also include
`drift_unavailable`, which SHALL exit 2.

#### Scenario: No completed run

- **WHEN** `tsipam drift` runs before the server has completed a drift run
- **THEN** it exits 2 with `error_code` `drift_unavailable`
