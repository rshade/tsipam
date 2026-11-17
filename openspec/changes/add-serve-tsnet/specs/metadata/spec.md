# Metadata Specification Delta

## ADDED Requirements

### Requirement: Remote reservations default the owner to the caller

When a server creates a new reservation (a reserve or allocate whose key
was not already held) and the request omits `--owner`, the server SHALL
record the caller as `owner`: the login name for a user, or the node name
for a tagged node. It SHALL then stamp `reserved_at` as for any first
metadata. An explicit `--owner` SHALL be stored as given. A repeat reserve
of an existing reservation SHALL NOT change its owner unless `--owner` is
supplied. Local mode SHALL be unchanged.

#### Scenario: A bare remote reserve records its caller

- **WHEN** user `alice@example.com` runs
  `site-id reserve 9 lab --server https://tsipam.example.ts.net`
  and ID 9 was not reserved
- **THEN** the result carries `owner: "alice@example.com"` and a
  `reserved_at` timestamp

#### Scenario: An explicit team owner is kept

- **WHEN** a caller with the `reserve` action runs
  `subnet reserve 10.60.0.0/24 lab --owner platform-team --server https://tsipam.example.ts.net`
- **THEN** the stored owner is `platform-team`, and the audit event's actor
  names the caller

#### Scenario: A repeat by another caller keeps the original owner

- **WHEN** ID 9 is owned by `alice@example.com` and user `bob@example.com`
  repeats `site-id reserve 9 lab` remotely without `--owner`
- **THEN** the command reports no change and the owner stays
  `alice@example.com`
