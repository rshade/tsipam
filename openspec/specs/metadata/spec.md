# Metadata Specification

## Purpose

Defines the optional ownership/tracking metadata (`owner`, `description`,
`ticket`, `reserved_at`) shared by `site-id reserve`, `subnet reserve`, and
`service reserve`, and how it is rendered and merged. This is a cross-cutting
behavior of those three reserve commands, not a standalone subcommand.

## Requirements

### Requirement: Four optional metadata flags on every reserve command

`--owner`, `--description`, `--ticket`, and `--reserved-at` SHALL be accepted
on `site-id reserve`, `subnet reserve`, and `service reserve`. Each is
optional; a blank (unprovided) flag SHALL leave the corresponding field
unset rather than writing an empty value. `--reserved-at` SHALL be validated
as an RFC 3339 timestamp when supplied.

#### Scenario: Reserve with metadata flags stores all four fields

- **WHEN** an operator runs
  `site-id reserve 1 hq-vpc --owner alice --ticket OPS-1234 --description "HQ gateway"`
- **THEN** the reservation records owner `alice`, ticket `OPS-1234`,
  description `HQ gateway`, and an auto-stamped `reserved_at`

#### Scenario: Reserve without metadata flags omits metadata entirely

- **WHEN** an operator runs `site-id reserve 1 hq-vpc` with no metadata flags
- **THEN** the `--format json` result carries no `owner`, `description`,
  `ticket`, or `reserved_at` fields at all

### Requirement: Metadata-only updates preserve omitted fields

A repeat reservation of the same key that supplies only some metadata fields
SHALL merge those fields onto the existing stored metadata: a supplied field
overwrites, and an omitted field SHALL retain its previously stored value
rather than being cleared.

#### Scenario: Updating description leaves owner and ticket intact

- **WHEN** a site-ID reservation already has owner and ticket metadata, and a
  repeat reserve for the same ID/site supplies only a new `--description`
- **THEN** the description is updated, and the stored owner and ticket remain
  unchanged

### Requirement: Reservation time auto-stamps when metadata is first added

Introducing owner, description, or ticket metadata for the first time on a
reservation that has no `reserved_at` yet SHALL auto-stamp `reserved_at` to
the current time (UTC, RFC 3339) when `--reserved-at` is not explicitly
supplied. A reservation that already carries a `reserved_at` SHALL NOT have
it overwritten by a later metadata-only update, and a bare repeat with no
metadata flags at all SHALL NOT introduce a timestamp.

#### Scenario: First metadata addition stamps reserved_at

- **WHEN** an operator reserves a site ID with `--owner alice` and no
  `--reserved-at`, and the reservation had no prior metadata
- **THEN** the stored and returned `reserved_at` is a non-empty RFC 3339
  timestamp

### Requirement: Metadata renders consistently across human and JSON output

Human output SHALL render present metadata as trailing space-separated
`key=value` pairs in the fixed order owner, ticket, description, then
`reserved=<timestamp>`, appended after the reservation's own tab-separated
fields. JSON output SHALL carry each present field under its own key
(`owner`, `description`, `ticket`, `reserved_at`) and omit absent fields
rather than emitting them as empty strings.

#### Scenario: List renders metadata in the fixed key order

- **WHEN** `site-id list` is run against a reservation with owner, ticket,
  and description all set
- **THEN** the human line ends with `owner=<value> ticket=<value>
  description=<value>`, in that order
