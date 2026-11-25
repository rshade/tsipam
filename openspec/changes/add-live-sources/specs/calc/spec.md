# Calc Specification Delta

## ADDED Requirements

### Requirement: Calc next-free reads a space's sources

In a space that declares sources, `calc next-free` SHALL exclude their same-family
prefixes and SHALL exit 2 with empty stdout when any of them cannot be read.

#### Scenario: The suggestion matches the allocation

- **WHEN** `calc next-free` and then `subnet allocate` run with the same pool,
  size, and space, and nothing else changes
- **THEN** both name the same block
