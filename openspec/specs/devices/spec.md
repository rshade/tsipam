# Devices Specification

## Purpose

Read-only inspection of tailnet devices through the official Tailscale v2 API
client, as a proof that control-plane collection works; it does not read or
write the local registry.

## Requirements

### Requirement: Listing devices requires explicit credentials

`tsipam devices list` SHALL require `TAILSCALE_TAILNET` and either
`TAILSCALE_API_KEY` or both `TAILSCALE_OAUTH_CLIENT_ID` and
`TAILSCALE_OAUTH_CLIENT_SECRET` to be set in the environment. A missing
tailnet or missing/incomplete credentials SHALL fail the command as a usage
error (exit 2) before any API call is attempted, rather than proceeding with
a partial or anonymous request.

#### Scenario: Missing tailnet is a hard failure

- **WHEN** `devices list` runs with `TAILSCALE_API_KEY` set but
  `TAILSCALE_TAILNET` unset
- **THEN** the command exits 2, writes nothing to stdout, and stderr states
  that `TAILSCALE_TAILNET is required`

#### Scenario: Incomplete OAuth credentials are a hard failure

- **WHEN** `devices list` runs with `TAILSCALE_TAILNET` and
  `TAILSCALE_OAUTH_CLIENT_ID` set but `TAILSCALE_OAUTH_CLIENT_SECRET` unset,
  and no `TAILSCALE_API_KEY`
- **THEN** the command exits 2, writes nothing to stdout, and stderr states
  that an API key or both OAuth client credentials are required

### Requirement: Device listing authenticates with either credential style

`devices list` SHALL authenticate against the Tailscale v2 API using either a
plain API key (HTTP Basic, password empty) or an OAuth 2.0 client-credentials
exchange, and SHALL request all available device fields.

#### Scenario: API-key round trip lists every returned device

- **WHEN** `devices list` runs with a valid `TAILSCALE_TAILNET` and
  `TAILSCALE_API_KEY` against a tailnet with two devices
- **THEN** stdout lists both devices, one per line, each with its addresses
  and advertised routes

#### Scenario: OAuth client-credentials round trip succeeds

- **WHEN** `devices list` runs with `TAILSCALE_TAILNET`,
  `TAILSCALE_OAUTH_CLIENT_ID`, and `TAILSCALE_OAUTH_CLIENT_SECRET` all set
- **THEN** the tool exchanges the client credentials for a bearer token and
  lists the returned device(s) exactly as the API-key path would

### Requirement: A control-plane API error is surfaced, not swallowed

An error response from the Tailscale API SHALL fail the command (exit 2) with
the API's status and message wrapped into the diagnostic on stderr, and SHALL
NOT produce any stdout output.

#### Scenario: 503 from the control plane is wrapped and reported

- **WHEN** the configured tailnet's device-list endpoint returns HTTP 503
  with body `{"message":"control plane unavailable"}`
- **THEN** the command exits 2, stdout is empty, and stderr contains
  `listing devices: control plane unavailable (503)`

### Requirement: Device output is sanitized like registry identities

Under an active `--sanitize` level, device names, addresses, advertised
routes, and the tailnet itself SHALL be redacted from both stdout and any
error text, using the same placeholder scheme as the registry commands.

#### Scenario: Sanitized collection failure never echoes the tailnet

- **WHEN** `devices list` fails against a tailnet whose control-plane error
  message echoes that tailnet's name back, with `--sanitize identity` active
- **THEN** stderr's wrapped diagnostic contains a tailnet placeholder instead
  of the raw tailnet name

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
