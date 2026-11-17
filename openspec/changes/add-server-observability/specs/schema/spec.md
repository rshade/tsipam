# Schema Server Drift Delta

## ADDED Requirements

### Requirement: Discover the scheduled drift command

The schema SHALL include `drift` with its `server` and `tailnet` flags. Help
and schema SHALL explain that `drift` reads the server's latest scheduled
report, needs `--server`, exits 0 whenever it prints a report even with
drift, and exits 2 when no report is available. They SHALL point to `check`
for gating.

#### Scenario: An agent discovers the drift command

- **WHEN** `tsipam --schema` runs
- **THEN** the commands array includes `drift` with `server` and `tailnet`
  flags
- **AND** its summary distinguishes reading the latest scheduled report from
  running a fresh `check`
