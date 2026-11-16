# Spec Delta

## ADDED Requirements

### Requirement: Device records carry the inventory fields the list already fetches

`devices list` SHALL include `tags`, `enabled_routes`, `authorized`,
`ephemeral`, `user`, `os`, and `hostname` on every device in JSON and human
output, beside the existing name, addresses, and advertised routes. JSON
SHALL emit empty slices as `[]` and SHALL emit false and empty strings.
Human output SHALL keep the current three columns first and then show
enabled routes, advertised routes that are not enabled, and the new fields.

#### Scenario: Advertised routes with none enabled are distinguishable

- **WHEN** a device advertises `10.0.0.0/24` and `10.1.0.0/24` and only
  `10.0.0.0/24` is enabled
- **THEN** JSON `advertised_routes` lists both, JSON `enabled_routes` lists
  only `10.0.0.0/24`, and human output marks `10.1.0.0/24` as not enabled

#### Scenario: Missing slices are empty arrays

- **WHEN** the control plane omits tags and enabled routes for a device
- **THEN** JSON `tags` and `enabled_routes` are `[]`, not null

### Requirement: A tag filter narrows the device list

`devices list --tag` SHALL accept one tag and SHALL emit only devices whose
tags include it. A value that does not start with `tag:` SHALL be treated as
`tag:` plus that value. Omitting `--tag` SHALL list every device. A filter
that matches nothing SHALL exit 0 and list zero devices.

#### Scenario: Prefix-less tag matches the control-plane tag

- **WHEN** `devices list --tag subnet` runs against a tailnet where one
  device carries `tag:subnet` and another does not
- **THEN** stdout contains only the tagged device and the command exits 0

### Requirement: Device identity fields follow the tag vocabulary rule

With sanitization active, `devices list` SHALL redact `user` and `hostname`
at both `identity` and `strict`, SHALL leave tags visible at `identity`, and
SHALL redact tags at `strict`. `os`, `authorized`, and `ephemeral` SHALL
remain visible at both levels. `enabled_routes` SHALL be redacted the same
way `advertised_routes` already are.

#### Scenario: Identity redacts logins and hostnames and keeps tags

- **WHEN** `devices list --sanitize identity` prints a device with a tag, a
  user login, and a hostname
- **THEN** the login and hostname appear only as placeholders and the tag
  appears unchanged

#### Scenario: Strict also redacts tags

- **WHEN** `devices list --sanitize strict` prints that same device
- **THEN** the tag appears only as a placeholder
