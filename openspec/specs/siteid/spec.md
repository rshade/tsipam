# Site-ID Specification

## Purpose

Provides a comment-preserving, tailnet-scoped registry for reserving 4via6
site IDs (0-65535) to site names, ahead of those sites being configured in
Tailscale policy.

## Requirements

### Requirement: Site-ID and site-name normalization

A site ID SHALL be accepted as a decimal, non-negative integer in the range
0-65535 and canonicalized through integer parsing, so `"01"` and `"1"` resolve
to the same registry key. A site name SHALL be accepted as any non-empty,
valid-UTF-8 string free of control characters after trimming whitespace; no
further hostname or Tailscale-selector grammar is imposed.

#### Scenario: Padded and bare site IDs are equivalent

- **WHEN** an operator reserves site ID `01` for `hq-vpc` and separately
  reserves `1` for `hq-vpc`
- **THEN** both resolve to the same canonical key `1`, and the second
  operation is an idempotent no-op

#### Scenario: Out-of-range or non-numeric ID is rejected

- **WHEN** an operator attempts to reserve a site ID that is not a
  non-negative base-10 integer, or exceeds 65535
- **THEN** the operation fails as invalid input and does not mutate the
  registry

#### Scenario: Invalid UTF-8 site name is rejected before any write

- **WHEN** an operator attempts to reserve a site ID for a site name
  containing invalid UTF-8 (for example a raw `\xff` byte)
- **THEN** the operation fails naming UTF-8 as the problem, and no registry
  file is created

### Requirement: Collision-safe and idempotent site-ID reservation

`tail-ipam site-id reserve <id> <site-name>` SHALL create the tailnet
registry when absent and reserve the canonical ID for the canonical site
name. An ID already reserved for a different site SHALL be refused without
overwriting the existing entry, reported through CLI exit 1 and HTTP conflict
status. An exact repeat with no metadata change SHALL succeed with
`changed=false`.

#### Scenario: New site ID is reserved

- **WHEN** an operator reserves site ID `1` for `hq-vpc` in a missing
  registry
- **THEN** the registry is created, ID `1` is owned by `hq-vpc`, the result
  reports `changed=true`, and the command exits 0

#### Scenario: A different site cannot take a reserved ID

- **WHEN** ID `1` belongs to `hq-vpc` and an operator attempts to reserve it
  for `branch-office`
- **THEN** the operation is refused as a collision, the existing reservation
  is preserved byte-for-byte, and the CLI exits 1 (a `*goa.ServiceError`
  named `collision` distinguishes this from a usage error)

### Requirement: Comment-preserving registry persistence

Site-ID reservations SHALL persist under the top-level `siteIDs` map. Edits
SHALL preserve HuJSON comments, formatting, unknown top-level members, and
untouched entries. A duplicate entry key, a canonically duplicate ID key, a
near-miss or duplicate `siteIDs` top-level key, and an empty or null site
value SHALL be rejected. A malformed `siteIDs` map SHALL NOT block operations
on the other registry maps (subnet pools, service addresses, UDP ports).

#### Scenario: Hand-written comment survives a second reservation

- **WHEN** a hand-written comment is attached to one `siteIDs` entry and an
  operator reserves a different site ID in the same registry
- **THEN** the original entry and its comment remain byte-for-byte unchanged

### Requirement: Idempotent site-ID release

`tail-ipam site-id release <id>` SHALL remove the canonical ID's reservation
when present and report `changed=true`. Releasing an unreserved ID from an
existing registry SHALL succeed as a no-op with `changed=false`. A missing
registry file SHALL remain an unreadable-registry error rather than being
created by release.

#### Scenario: Releasing a reserved ID removes it

- **WHEN** an operator releases a reserved site ID
- **THEN** the entry is removed, the result reports `changed=true`, and
  unrelated entries and comments remain unchanged

### Requirement: Release best-effort repairs an undecodable registry

When `site-id release <id>` cannot build any canonical view of `siteIDs` at
all (a decode or validation failure, not a benign absence), it SHALL NOT
fail closed immediately. It SHALL search the raw on-file `siteIDs` keys for
spellings that normalize to the canonical ID, remove each candidate in
memory, and keep only a removal that makes `siteIDs` decode cleanly with
that ID now absent; the file is written at most once, and only for a
verified candidate. When no candidate repairs the map, release SHALL fail
with an error and leave the file byte-for-byte unchanged.

#### Scenario: A malformed duplicate key is removed and release succeeds

- **WHEN** `siteIDs` fails to decode because two raw keys both canonicalize
  to the ID an operator releases
- **THEN** release removes the offending raw key, `siteIDs` now decodes
  cleanly without that ID, and the operation reports `changed=true`

### Requirement: Deterministic site-ID listing

`tail-ipam site-id list` SHALL return every site-ID reservation. Human output
SHALL contain one tab-separated `id<TAB>site` line per reservation, with the
metadata columns from `owner`/`ticket`/`description`/`reserved` appended when
present. JSON output SHALL carry a `site_ids` array in the versioned
envelope, with an empty registry map represented as an empty array rather
than an absent field.

#### Scenario: Reserve then list round-trips through JSON

- **WHEN** an operator reserves site ID `2` for `branch-office` and then runs
  `site-id list --format json`
- **THEN** the JSON envelope's `site_ids` array contains exactly one entry
  with `id: "2"` and `site: "branch-office"`
