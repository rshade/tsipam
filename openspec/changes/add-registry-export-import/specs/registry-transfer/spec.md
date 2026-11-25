# Registry Transfer Delta

## Purpose

Moves reservations between registries: export renders the selected
tailnet's reservations as one canonical HuJSON document, and import applies
such a document to the selected registry atomically under the same rules as
the reserve commands.

## ADDED Requirements

### Requirement: Export renders a canonical registry document

`tsipam registry export` SHALL render the selected tailnet's reservations as
one HuJSON document in the registry format: canonical keys, entries sorted as
the list commands sort them, the bare string form for entries without
metadata, empty maps omitted, and `{}` for a registry with no reservations.
Comments and raw key spellings from a file registry SHALL NOT appear.

#### Scenario: Export of a hand-edited file is canonical

- **WHEN** a registry file holds site ID key `"01"` for `hq-vpc` under a
  comment, and an operator runs `tsipam registry export`
- **THEN** stdout is a document whose `siteIDs` map holds `"1": "hq-vpc"`
  with no comment, and the command exits 0

#### Scenario: Empty registry exports as an empty object

- **WHEN** the selected registry exists and holds no reservations
- **THEN** export writes the document `{}` followed by a newline

### Requirement: Export is deterministic and round-trips

Exporting the same set of reservations SHALL always produce identical bytes,
whichever store holds them. Importing an exported document into an empty
registry and exporting again SHALL reproduce the first export byte for byte.

#### Scenario: Round trip through an empty registry

- **WHEN** registry A is exported, the document is imported into an empty
  registry B, and registry B is exported
- **THEN** both exported documents are byte-identical

### Requirement: Export destination and output format

Without `--output`, export SHALL write the document itself to stdout in human
mode, and in JSON mode an envelope whose `document` field holds the document
text. With `--output <file>`, export SHALL write the document to
that file atomically with mode `0600` and print only a summary result.

#### Scenario: JSON export embeds the document

- **WHEN** an operator runs `tsipam registry export --format json`
- **THEN** stdout is one envelope with `command` `"registry export"`,
  `status` `"ok"`, a `document` string, and a `counts` object with
  `site_ids`, `subnet_pools`, `service_addresses`, and `udp_ports`

#### Scenario: Export to a file

- **WHEN** an operator runs `tsipam registry export --output snapshot.hujson`
- **THEN** `snapshot.hujson` holds the document with mode `0600`, stdout holds
  only the summary result, and no partially written file is ever visible

### Requirement: Export requires a complete, valid registry

Export SHALL fail with exit 2 and empty stdout when the selected registry is
missing, unreadable, or when any of its four maps fails validation. It SHALL
never emit a partial document.

#### Scenario: Malformed map blocks export

- **WHEN** the registry's `udpPorts` map is malformed and an operator runs
  `tsipam registry export`
- **THEN** the command exits 2, stdout is empty, and stderr names the problem

### Requirement: Import validates the whole source document first

`tsipam registry import <file>` SHALL read the source from a file, or from
stdin when the argument is `-`, and SHALL validate it with every rule the
registry loader applies, requiring all four maps to validate. A source that
is not valid UTF-8 or fails validation SHALL exit 2 with nothing written.

#### Scenario: Invalid source is rejected before any write

- **WHEN** the source document holds subnet pool `10.0.0.1/24` (host bits
  set)
- **THEN** import exits 2, stdout is empty, and the target registry is
  unchanged

#### Scenario: Import from stdin

- **WHEN** an operator runs `tsipam registry export --tailnet a --format human
  | tsipam registry import - --tailnet b` and registry `b` is empty
- **THEN** registry `b` holds the same reservations as registry `a`

### Requirement: Import requires an empty target unless merging

Without `--merge`, import SHALL refuse with exit 1, writing nothing, when the
target registry holds any reservation. A missing target registry or one with
no reservations SHALL count as empty, and import SHALL create it when it is
missing.

#### Scenario: Non-empty target is refused

- **WHEN** the target registry holds site ID `1` and an operator runs
  `tsipam registry import snapshot.hujson` without `--merge`
- **THEN** the command exits 1, stdout is empty, stderr suggests `--merge`,
  and the target is unchanged

#### Scenario: Missing target is created

- **WHEN** no registry exists for the selected tailnet and a valid document
  is imported
- **THEN** the registry is created holding the document's reservations

### Requirement: Merge adds and never deletes

With `--merge`, each source entry SHALL be `add` when its key is absent,
`unchanged` when key, target, and metadata already match, `update` when key
and target match but metadata differs, and `conflict` when the reserve rules
would refuse it. Metadata updates SHALL follow the reserve merge rules.
Import SHALL never remove an existing reservation.

#### Scenario: Merge adds a new pool beside existing ones

- **WHEN** the target holds `10.0.0.0/24` for `hq-vpc` and the source holds
  `10.0.0.0/24` for `hq-vpc` and `10.1.0.0/24` for `branch-office`
- **THEN** import with `--merge` reports one `unchanged` and one `add`,
  exits 0, and the target holds both pools

#### Scenario: Existing reservations absent from the source survive

- **WHEN** the target holds site ID `9` and the source does not mention it
- **THEN** after a successful `--merge` import, site ID `9` is still reserved

### Requirement: Import is atomic

Import SHALL apply every source entry or none. Any `conflict`, including a
subnet pool overlapping a pool held by a different site, SHALL refuse the
whole import with exit 1 and leave the target unchanged. Import SHALL be
serialized with concurrent reserve, release, and allocate commands on the
same registry.

#### Scenario: One conflict refuses every entry

- **WHEN** a `--merge` source holds ten new site IDs and one site ID already
  reserved for a different site
- **THEN** import exits 1, stdout is empty, stderr names the conflicting
  entry and the number of conflicts, and none of the ten IDs is written

### Requirement: Import copies metadata verbatim

Import SHALL store each source entry's `owner`, `description`, `ticket`, and
`reservedAt` as given, after the same validation as the reserve flags. It
SHALL NOT stamp `reservedAt` on an entry that lacks one.

#### Scenario: Missing reservedAt stays missing

- **WHEN** the source holds an entry with `owner` but no `reservedAt`
- **THEN** the imported entry has the same `owner` and no `reservedAt`

### Requirement: Import plan reports without writing

With `--plan`, import SHALL compute the same per-entry actions as a real
import, print the report, and write nothing. It SHALL exit 0 when the import
would apply and 1 when it would be refused for a non-empty target, a
conflict, or unknown top-level keys.

#### Scenario: Plan lists every conflict

- **WHEN** an operator runs `tsipam registry import snapshot.hujson --merge
  --plan --format json` and two source entries conflict
- **THEN** stdout holds the full report with both entries marked `conflict`,
  the command exits 1 with `error_code` `import_refused`, and the target
  registry is unchanged

### Requirement: Import reports content it does not carry over

Import SHALL report source comments and every non-canonical key spelling with
its canonical form, then proceed without them. Unknown top-level keys SHALL
refuse the import with exit 1, naming them, unless `--drop-unknown` is given,
in which case they SHALL be reported as dropped.

#### Scenario: Raw spelling is reported

- **WHEN** the source holds site ID key `"007"`
- **THEN** the report lists raw key `"007"` with canonical key `"7"`, and the
  imported reservation is keyed `7`

#### Scenario: Unknown top-level key needs explicit consent

- **WHEN** the source holds a top-level key `"vlanPools"`
- **THEN** import without `--drop-unknown` exits 1 naming `vlanPools`, and
  with `--drop-unknown` it imports the four known maps and lists
  `vlanPools` as dropped

### Requirement: Import result envelope

Import SHALL emit an envelope with `command` `"registry import"`, `status`
`"ok"` when it wrote, or `"no_changes"` when it did not, and an `import`
object holding `mode` (`empty` or `merge`), `planned`, `changed`, per-kind
action counts, the sorted `entries`, a `dropped` object, and `refusal` when a
plan would be refused.

#### Scenario: Entry rows identify each reservation

- **WHEN** an import result is rendered with `--format json`
- **THEN** each `entries` row carries `kind` (`site_id`, `subnet_pool`,
  `service_address`, or `udp_port`), `key`, `target`, and `action`, plus
  `site` for `udp_port` rows and `existing` for `unchanged`, `update`, and
  `conflict` rows

#### Scenario: Refused plan names its reason

- **WHEN** `--plan` runs against a non-empty target without `--merge`
- **THEN** the `import` object carries `refusal` `"non_empty_target"`; the
  other values are `"conflict"` and `"unknown_keys"`

### Requirement: Transfer output is sanitized

Under `--sanitize identity` or `strict`, export SHALL redact identities in the
document and import SHALL redact identities in its report, using the same
rules as the list commands. Values from the import source SHALL be redacted
in error messages too, including before validation finishes.

#### Scenario: Sanitized export hides site names

- **WHEN** an operator runs `tsipam registry export --sanitize identity`
- **THEN** site names, owners, CIDRs, service identities, service addresses,
  and UDP-port site and device labels in the document are placeholders,
  and numeric site IDs and port numbers stay visible
