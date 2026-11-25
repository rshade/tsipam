# Live Sources Specification Delta

## Purpose

Lets a space declare read-only views of the network it plans, so that its pools
can be compared with what is in use there and allocation can avoid it.

## ADDED Requirements

### Requirement: Sources are read-only

A source SHALL only read from the network it observes. No command SHALL create,
change, or delete anything in it.

#### Scenario: A comparison only reads

- **WHEN** `check` runs against a space with a source
- **THEN** every request the source makes is a read operation

### Requirement: A space declares named sources

`source add <space> <name> --type <type> --set key=value` SHALL declare a source
on an existing named space. Source names SHALL follow the space-name grammar and
be unique within the space. An exact repeat SHALL succeed with `changed=false`; a
repeat with another type or other settings SHALL be refused as a collision.

#### Scenario: Declaring a source

- **WHEN** `source add prod use1 --type aws-vpc --set region=us-east-1` runs
- **THEN** it reports `changed=true` and `source list prod` shows it

#### Scenario: A different region under the same name

- **WHEN** the same command runs with `region=us-west-2`
- **THEN** it exits 1 as a collision and the registry is unchanged

#### Scenario: The default space has no sources

- **WHEN** `source add tailnet use1 --type aws-vpc --set region=us-east-1` runs
- **THEN** it exits 2 as invalid input

### Requirement: Declaring a source contacts nothing

`source add` SHALL validate the type and settings without credentials or network
access. An unknown type, an unknown or repeated setting, and a missing required
setting SHALL be refused as invalid input.

#### Scenario: A credential is not a setting

- **WHEN** `source add prod use1 --type aws-vpc --set region=us-east-1 --set secret_access_key=x`
  runs
- **THEN** it exits 2, stderr does not contain the value, and the registry is
  unchanged

### Requirement: Sources are removed explicitly

`source remove <space> <name>` SHALL remove a declaration, require approval, and
honor `--dry-run`. Removing an absent source SHALL succeed with `changed=false`.
Pools SHALL be unaffected.

#### Scenario: Removing a source

- **WHEN** `source remove prod use1 --yes` runs
- **THEN** it reports `changed=true` and the pools of `prod` are unchanged

### Requirement: A space with sources is in use

`space release` SHALL be refused with exit 1 while the space declares a source.

#### Scenario: A declared source blocks release

- **WHEN** `prod` has no pool, no mapping, and one source, and
  `space release prod --yes` runs
- **THEN** it exits 1 and the registry is unchanged

### Requirement: Sources persist in their space

Declarations SHALL persist under the space's `sources` map with their type and
settings. Comments and untouched entries SHALL be preserved. A declaration that
`source add` would refuse SHALL make the space map invalid for every command that
reads it, and the error SHALL NOT contain a setting value.

#### Scenario: A hand-written setting is validated

- **WHEN** a hand edit adds an unknown setting and `check` runs
- **THEN** it exits 2 with empty stdout before any source is contacted

#### Scenario: A hand-written credential is not listed

- **WHEN** a hand edit adds `secret_access_key` to the settings and `source list` or
  `space list` runs
- **THEN** it exits 2 with empty stdout, and stderr does not contain the value

### Requirement: Compare a space with its sources

`plan` and `check` SHALL compare the union of a space's source prefixes with its
pools of the same family. A routed pool that contains a whole prefix SHALL be
`matched`, and one that contains none SHALL be `reserved_but_unadvertised`. A
prefix that no pool contains SHALL be `unreserved_but_live`.

#### Scenario: Two regions, one space

- **WHEN** `prod` declares sources in two regions, holds one pool per region, and
  each region uses its pool
- **THEN** both pools are `matched`

#### Scenario: An unmapped space with a source

- **WHEN** `prod` is not mapped and its source reports a block inside its pool
- **THEN** the pool has one row, which is `matched` and not `external`

#### Scenario: An unplanned network

- **WHEN** a source reports `172.31.0.0/16` and no pool of `prod` contains it
- **THEN** one row in `prod` is `unreserved_but_live` and `check` exits 1

### Requirement: External pools conflict with source prefixes

A pool with usage `external` that overlaps a source prefix of its space SHALL be
`conflict`, and that prefix SHALL NOT also be `unreserved_but_live`.

#### Scenario: Held space is used

- **WHEN** `10.60.0.0/16` is external in `prod` and a source reports `10.60.4.0/24`
- **THEN** one row is `conflict` and `check` exits 1

### Requirement: Source rows name their origin

A row produced from sources SHALL carry `origin` with the value `sources`, its
`space`, and `observed` values of the form `<source>/<resource>`. Rows produced
from the tailnet SHALL NOT carry `origin`.

#### Scenario: Evidence identifies the resource

- **WHEN** source `use1` reports a VPC inside a pool
- **THEN** `observed` holds `use1/` followed by the VPC ID

### Requirement: A mapped space with sources is compared twice

A space that is mapped and declares sources SHALL produce one row per origin for
each IPv4 pool and one `sources` row for each IPv6 pool. Each row SHALL count toward
drift on its own.

#### Scenario: In use but not routed

- **WHEN** a mapped `Boston` declares a source that reports `10.0.0.0/24`, and no
  via route for it is advertised
- **THEN** its `sources` row is `matched` and its tailnet row is
  `reserved_but_unadvertised`

### Requirement: Incomplete source data fails the command

If any source to be read fails, times out, or returns malformed data, the command
SHALL exit 2 with empty stdout and write no reservation. An empty result from a
source SHALL be valid.

#### Scenario: One source of two fails

- **WHEN** one of two sources in `prod` is denied access
- **THEN** `check` exits 2 with empty stdout and stderr names the space and source

#### Scenario: Missing credentials are distinguishable

- **WHEN** `check`, `subnet allocate`, or `calc next-free` needs a source that has no
  credentials
- **THEN** it exits 2 with `error_code` `credentials_missing` and an `actionable_fix`

#### Scenario: Two failures report the same one each time

- **WHEN** two sources fail, in either order
- **THEN** stderr names the first by space and then source name

#### Scenario: An empty network

- **WHEN** every source returns no prefixes
- **THEN** the comparison completes and unused pools are drift

### Requirement: Credentials stay out of the registry

A source SHALL take credentials only from its provider's standard environment. The
registry SHALL hold no credential, and no output or error SHALL contain one.

#### Scenario: Declarations are safe to commit

- **WHEN** a registry with sources is written
- **THEN** it holds only the source types and their validated settings

### Requirement: Source identities are sanitized

Identity and strict sanitization SHALL replace source names, setting values, and
resource identifiers in stdout and stderr. Source types and setting names SHALL
remain visible. Provider response bodies SHALL NOT reach stdout or stderr.

#### Scenario: A sanitized failure

- **WHEN** a source is denied access under `--sanitize identity`
- **THEN** stderr keeps the source type and reason, and contains no space, source,
  account, or resource identifier

### Requirement: Source commands are listed deterministically

`source list [space]` SHALL return declarations sorted by space and then name, with
type and settings, and SHALL read nothing but the registry. JSON output SHALL carry
a `sources` array that is empty, not absent, when there are none.

#### Scenario: No sources

- **WHEN** no space declares a source and `source list --format json` runs
- **THEN** `sources` is `[]` and the command exits 0
