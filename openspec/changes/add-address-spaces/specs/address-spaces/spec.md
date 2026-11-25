# Address Spaces Specification Delta

## Purpose

Lets one registry plan several networks whose addresses may overlap, and
records which 4via6 site ID, if any, makes each of them reachable from the
tailnet.

## ADDED Requirements

### Requirement: The default space is the tailnet

Every registry SHALL have a default space named `tailnet` that holds the pools
routed directly on the tailnet. Omitting `--space` and passing
`--space tailnet` SHALL be equivalent. The default space SHALL NOT be reserved,
released, or mapped.

#### Scenario: Naming the default space changes nothing

- **WHEN** `subnet list` and `subnet list --space tailnet` run on one registry
- **THEN** both write the same stdout and exit 0

#### Scenario: The default name cannot be taken

- **WHEN** `space reserve tailnet` or `space reserve Tailnet` runs
- **THEN** it exits 2 and no registry is created or changed

#### Scenario: The default space cannot be released or mapped

- **WHEN** `space release tailnet --yes` or `space map tailnet 3` runs
- **THEN** it exits 2 as invalid input and the registry is unchanged

### Requirement: A registry without spaces is unaffected

When a registry has no named space and no external pool, every existing command
SHALL keep its stdout, stderr, exit code, and registry bytes, in both output
formats.

#### Scenario: Existing fixtures are unchanged

- **WHEN** the existing golden and smoke cases run
- **THEN** they pass with no fixture changed except the schema catalog

### Requirement: Space names follow one grammar

A space name SHALL be 1 to 63 ASCII letters, digits, `.`, `_`, or `-`, starting
with a letter or digit. Case SHALL be preserved. Two names that differ only in
case SHALL NOT coexist.

#### Scenario: A separator is refused

- **WHEN** `space reserve bos:ton` runs
- **THEN** it exits 2 as invalid input and the registry is unchanged

#### Scenario: A case variant is refused

- **WHEN** `Boston` exists and `space reserve boston` runs
- **THEN** it exits 1 as a collision and the registry is unchanged

### Requirement: Reserving a space is idempotent

`space reserve <name>` SHALL create the registry when absent and add the space.
It SHALL accept the `--owner`, `--description`, `--ticket`, and `--reserved-at`
flags with the merge and stamping rules of `subnet reserve`. An exact repeat
SHALL succeed with `changed=false`.

#### Scenario: A new space

- **WHEN** `space reserve Boston` runs and no registry exists
- **THEN** it creates the registry, reports `changed=true`, and exits 0

#### Scenario: A repeat keeps metadata

- **WHEN** `Boston` has owner `alice` and `space reserve Boston` runs
- **THEN** it reports `changed=false` and the registry bytes are unchanged

### Requirement: Releasing a space never removes pools

`space release <name>` SHALL remove an empty, unmapped space. A space that holds
a pool or maps a site ID SHALL be refused with exit 1 and SHALL be left
unchanged. Releasing an absent space in an existing registry SHALL succeed with
`changed=false`.

#### Scenario: A space in use is kept

- **WHEN** `Boston` holds `10.0.0.0/24` and `space release Boston --yes` runs
- **THEN** it exits 1, stderr names the reason, and the registry is unchanged

#### Scenario: An absent space is a no-op

- **WHEN** no space `Missing` exists and `space release Missing --yes` runs on an
  existing registry
- **THEN** it reports `changed=false` and exits 0

#### Scenario: An empty space is removed

- **WHEN** `Boston` has no pool and no mapping and `space release Boston --yes`
  runs
- **THEN** it reports `changed=true` and `space list` omits it

### Requirement: A space maps at most one site ID

`space map <space> <site-id>` SHALL record the site ID that reaches the space.
The ID SHALL already be reserved, SHALL map at most one space, and a space SHALL
have at most one ID. A different ID or space SHALL be refused as a collision. An
exact repeat SHALL succeed with `changed=false`.

#### Scenario: Mapping a reserved ID

- **WHEN** site ID 3 is reserved and `space map Boston 3` runs
- **THEN** it reports `changed=true`, and `space list` shows site ID 3 for
  `Boston`

#### Scenario: An unreserved ID is refused

- **WHEN** site ID 9 is not reserved and `space map Boston 9` runs
- **THEN** it exits 2 as invalid input and the registry is unchanged

#### Scenario: An ID already in use is refused

- **WHEN** site ID 3 maps `Boston` and `space map Chicago 3` runs
- **THEN** it exits 1 as a collision naming `Boston`

### Requirement: Mapping accepts a via prefix

In place of a decimal site ID, `space map` SHALL accept a 4via6 prefix and use
the site ID it encodes. The result SHALL report the ID in decimal. A bare value
SHALL always be read as decimal.

#### Scenario: A prefix pasted from a policy

- **WHEN** site IDs 14 and 20 are reserved and
  `space map Boston fd7a:115c:a1e0:b1a:0:14:a00:0/120` runs
- **THEN** it maps site ID 20, and the result reports `20`

#### Scenario: A bare number is decimal

- **WHEN** `space map Boston 14` runs
- **THEN** it maps site ID 14

#### Scenario: A prefix that is not 4via6

- **WHEN** `space map Boston 2001:db8::/48` runs
- **THEN** it exits 2 as invalid input and the registry is unchanged

### Requirement: Unmapping is explicit

`space unmap <space>` SHALL remove the space's site-ID mapping and keep its
pools and the site-ID reservation. Unmapping a space with no mapping SHALL
succeed with `changed=false`.

#### Scenario: Pools survive an unmap

- **WHEN** `Boston` maps site ID 3 and `space unmap Boston --yes` runs
- **THEN** `subnet list --space Boston` is unchanged apart from its mapping
  fields, and `site-id list` still shows ID 3

### Requirement: Mapped pools show their 4via6 identity

An IPv4 pool in a mapped space SHALL be listed with the label
`4via6:<site-id>:<space>:<cidr>` and the via route that encodes the pair, in a
`mappings` array in JSON output. Both SHALL be derived from the registry and
SHALL NOT be stored.

#### Scenario: Label and route

- **WHEN** site ID 3 maps `Boston`, which holds `10.0.0.0/24`, and
  `subnet list --space Boston --format json` runs
- **THEN** that pool carries the label `4via6:3:Boston:10.0.0.0/24` and the
  route `fd7a:115c:a1e0:b1a:0:3:a00:0/120`

#### Scenario: IPv6 pools have no 4via6 identity

- **WHEN** a mapped space holds `2001:db8::/48`
- **THEN** that pool is listed without a label or via route

### Requirement: Spaces are listed deterministically

`space list` SHALL return every named space sorted by name, with its site ID
when mapped, its pool count, and its metadata. JSON output SHALL carry a
`spaces` array that is empty, not absent, when there are none.

#### Scenario: Two spaces

- **WHEN** `Chicago` and `Boston` exist and `space list` runs
- **THEN** `Boston` precedes `Chicago`

#### Scenario: No spaces

- **WHEN** a registry holds no named space and `space list --format json` runs
- **THEN** `spaces` is `[]` and the command exits 0

### Requirement: An unknown space is an error

A known space is `tailnet` or a reserved space. Every command given an unknown
space, except `space reserve` and `space release`, SHALL exit 2 with empty stdout
and leave the registry unchanged. It SHALL NOT create the space.

#### Scenario: A typo creates nothing

- **WHEN** `subnet reserve 10.0.0.0/24 hq --space Bostn` runs
- **THEN** it exits 2, stderr names the space, and `space list` is unchanged

### Requirement: Spaces persist in their own map

Spaces SHALL persist under the top-level `addressSpaces` map, each holding its
optional metadata, `siteID`, and `subnetPools`. Edits SHALL preserve comments,
formatting, and untouched entries. Duplicate, near-miss, or invalid names, a
repeated site ID, and an invalid or cross-site overlapping pool within one space
SHALL be rejected.

#### Scenario: A comment survives

- **WHEN** a comment precedes a `Boston` pool and another pool is reserved there
- **THEN** the comment and the first pool are byte-for-byte unchanged

#### Scenario: A repeated site ID is invalid

- **WHEN** a hand edit maps site ID 3 to two spaces and `space list` runs
- **THEN** it exits 2 with empty stdout

### Requirement: A malformed space map stays isolated

A malformed `addressSpaces` map SHALL NOT block commands that use only other
maps. Commands that read it SHALL exit 2 without a partial result, and a release
inside a named space SHALL NOT attempt a repair.

#### Scenario: The default space still works

- **WHEN** `addressSpaces` is malformed and `subnet reserve 10.9.0.0/24 hq` runs
- **THEN** it succeeds and leaves `addressSpaces` byte-for-byte unchanged

#### Scenario: A comparison fails closed

- **WHEN** `addressSpaces` is malformed and `check` runs
- **THEN** it exits 2 with empty stdout

### Requirement: A dangling mapping fails a comparison

When a space maps a site ID that is not reserved, `plan` and `check` SHALL exit
2 with empty stdout and name the space and ID on stderr.

#### Scenario: A hand edit removes the site ID

- **WHEN** `Boston` maps site ID 3, `siteIDs` has no 3, and `check` runs
- **THEN** it exits 2 with empty stdout

### Requirement: Space commands follow mutation safety

`space reserve`, `release`, `map`, and `unmap` SHALL honor `--dry-run`. `space
release` and `space unmap` SHALL require approval. `__schema` SHALL publish
`mutate` for reserve and map, `delete` for release and unmap, and `read-only`
for list.

#### Scenario: A rehearsed mapping

- **WHEN** `space map Boston 3 --dry-run` runs
- **THEN** it reports `changed=true` and the registry bytes are unchanged

#### Scenario: An unapproved release

- **WHEN** `space release Boston --format json` runs without `--yes`
- **THEN** it exits 2 with `error_code` `confirmation_required` and the registry
  is unchanged

### Requirement: Space identities are sanitized

Identity and strict sanitization SHALL replace space names, and the CIDRs,
sites, and owners inside spaces, in stdout and stderr. A 4via6 label SHALL keep
its prefix and numeric site ID and replace its other parts consistently with the
rest of the output.

#### Scenario: A sanitized label

- **WHEN** `subnet list --space Boston --sanitize identity` lists a mapped pool
- **THEN** the label begins `4via6:3:` and contains neither `Boston` nor
  `10.0.0.0/24`
