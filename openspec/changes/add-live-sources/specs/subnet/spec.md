# Subnet Specification Delta

## ADDED Requirements

### Requirement: Allocation in a space reads its sources

In a space that declares sources, `subnet allocate` SHALL read all of them and
count their same-family prefixes as taken, in addition to the space's pools and,
when it is mapped, its decoded via routes. It SHALL refuse to allocate when any
source cannot be read. The registry lock SHALL NOT be held during the reads.

#### Scenario: A live VPC is skipped

- **WHEN** a source of `prod` reports `10.0.0.0/16` and
  `subnet allocate 10.0.0.0/8 --size 16 prod --space prod` runs
- **THEN** it reserves `10.1.0.0/16`

#### Scenario: A failed source writes nothing

- **WHEN** a source cannot be read
- **THEN** the command exits 2 and no reservation is written

#### Scenario: Sources change during the request

- **WHEN** the space's source declarations change after they were read and before
  the registry transaction
- **THEN** the command exits 2 and no reservation is written
