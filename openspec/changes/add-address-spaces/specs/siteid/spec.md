# Site ID Specification Delta

## ADDED Requirements

### Requirement: A mapped site ID cannot be released

`site-id release <id>` SHALL be refused with exit 1 when a space maps the ID, and
SHALL leave the registry unchanged. A malformed `addressSpaces` map SHALL NOT
block the release.

#### Scenario: A mapped ID is kept

- **WHEN** site ID 3 maps `Boston` and `site-id release 3 --yes` runs
- **THEN** it exits 1, stderr names the space, and the registry is unchanged

#### Scenario: An unmapped ID is released

- **WHEN** `space unmap Boston --yes` and then `site-id release 3 --yes` run
- **THEN** the release reports `changed=true`

#### Scenario: A malformed space map does not block

- **WHEN** `addressSpaces` is malformed and `site-id release 3 --yes` runs
- **THEN** the reservation is removed and the command exits 0
