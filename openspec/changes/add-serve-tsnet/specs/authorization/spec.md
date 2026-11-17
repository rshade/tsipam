# Authorization Specification Delta

## Purpose

Decide who may read and change which reservations on a tsipam server, using
tailnet identity and tailnet policy grants instead of a separate login or
token system.

## ADDED Requirements

### Requirement: Identify callers by tailnet identity

The server SHALL identify the caller of each request from the connection's
tailnet source address, as a user (login name) or a tagged node (node name
and tags), using a lookup scoped to the server's Service. It SHALL resolve
identity and capabilities again for every request and SHALL NOT reuse a
previous request's result. It SHALL NOT trust identity carried in HTTP
request headers or bodies. A request whose caller cannot be resolved SHALL
be denied.

#### Scenario: A tagged CI node is identified

- **WHEN** a node tagged `tag:ci` with no user calls the server
- **THEN** the server identifies the caller as that node and its tags

#### Scenario: A spoofed header is ignored

- **WHEN** a request carries a `Tailscale-User-Login` header naming a
  different user
- **THEN** authorization uses the connection's resolved identity, not the
  header

#### Scenario: A revoked grant applies on the next request

- **WHEN** a caller holding `{"actions": ["reserve"]}` keeps one HTTP
  connection open, the grant is removed from the tailnet policy, and the
  caller sends another reserve on the same connection after the policy
  change reaches the server's node
- **THEN** that reserve is denied and nothing is written

### Requirement: Capability grants authorize actions

The server SHALL authorize from the values of the peer capability named by
`--capability` (default `github.com/rshade/tsipam/cap/ipam`) granted to the
caller for the Service. Each value is an object with `actions` (required:
`read`, `reserve`, `release`, `allocate`, `admin`) and optional `tailnets`,
`sites`, and `services` glob lists. Values are combined as a union.

#### Scenario: A grant to the Service is honored

- **WHEN** the only app grant naming the capability has
  `dst: ["svc:tsipam"]` and value `{"actions": ["read"]}`, and its source
  runs `subnet list`
- **THEN** the list succeeds

#### Scenario: A grant allows listing

- **WHEN** the policy grants `autogroup:member` the capability value
  `{"actions": ["read"]}` on `svc:tsipam` and a member runs `subnet list`
- **THEN** the list succeeds

#### Scenario: Two grants combine

- **WHEN** a caller holds `{"actions": ["read"]}` from one grant and
  `{"actions": ["reserve"], "sites": ["lab-*"]}` from another
- **THEN** the caller may list everything and reserve for site `lab-east`

### Requirement: Deny by default

A request SHALL be denied unless at least one capability value grants its
action and matches every scope that applies to it. `admin` SHALL grant
every action. A value that does not parse SHALL grant nothing, SHALL NOT
prevent the caller's other values from granting, and SHALL be logged with
the capability name but without the caller's identity.

#### Scenario: No grant means no access

- **WHEN** a caller with no tsipam capability runs `site-id list`
- **THEN** the client exits 2 with empty stdout and stderr reports that
  access was denied

#### Scenario: A malformed value grants nothing

- **WHEN** a caller's only value is `{"actions": "read"}` (a string, not a
  list)
- **THEN** every request from that caller is denied

#### Scenario: A malformed value does not cancel a valid one

- **WHEN** a caller holds `{"actions": "read"}` from one grant and
  `{"actions": ["read"]}` from another, and runs `subnet list`
- **THEN** the list succeeds

### Requirement: Method actions and scopes

Each method SHALL require one action: lists, history, identity values, live
routes, `plan`, `check`, and `check-policy` need `read`; reserves need
`reserve`; releases need `release`; allocations need `allocate`. `tailnets`
SHALL match the registry alias. `sites` SHALL match the site a site-ID,
subnet-pool, or UDP-port change affects; `services` SHALL match the
service a service-address change affects.

#### Scenario: Site scope limits reserves

- **WHEN** a caller holds `{"actions": ["reserve"], "sites": ["preview-*"]}`
  and runs `subnet reserve 10.50.0.0/24 hq`
- **THEN** the request is denied and nothing is written

#### Scenario: Alias scope limits every action

- **WHEN** a caller holds `{"actions": ["read"], "tailnets": ["staging"]}`
  and lists with `--tailnet production`
- **THEN** the request is denied

### Requirement: Releases are authorized against the stored reservation

A release SHALL be authorized against the site or service of the
reservation as stored at the moment it is removed, checked inside the same
transaction as the removal. Releasing a key that is not reserved SHALL need
only the `release` action and the matching alias scope.

#### Scenario: A scoped caller cannot release another site's pool

- **WHEN** `10.20.0.0/16` is reserved for `hq` and a caller holding
  `{"actions": ["release"], "sites": ["lab-*"]}` releases it
- **THEN** the request is denied and the reservation remains

### Requirement: Mutating actions imply read

`reserve`, `release`, and `allocate` SHALL each also grant `read` within the
same `tailnets` scope, because their collision, replay, and no-op results
already reveal stored reservations. `sites` and `services` scopes SHALL NOT
narrow that implied `read`.

#### Scenario: An allocate-only CI caller can list

- **WHEN** a caller holds only `{"actions": ["allocate"], "sites": ["preview-*"]}`
  and runs `subnet list`
- **THEN** the list succeeds and includes pools of every site

### Requirement: Denied requests change nothing and reveal nothing

A denied request SHALL return the API error `forbidden` and SHALL NOT
modify the registry. A caller holding no action on the registry alias SHALL
be denied before the registry is read, so the response is the same whether
or not the requested key exists. The message SHALL name only the missing
action and the scope kind, not the caller or the stored reservation.

#### Scenario: Denial does not disclose state

- **WHEN** a caller with no tsipam action on alias `production` releases a
  site ID there
- **THEN** the error is identical whether or not that site ID is reserved

### Requirement: Changes record the caller as actor

Every change a server applies SHALL record the caller in the audit event's
actor: `user:<login>` for users and `node:<node-name>` for tagged nodes.
Actor values SHALL be redacted under sanitization as owner values are.

#### Scenario: A CI allocation names its node

- **WHEN** node `ci-runner-7` tagged `tag:ci` allocates a subnet through
  the server
- **THEN** `registry history` shows the event with actor
  `node:ci-runner-7`

### Requirement: Callers can see their own identity and grants

`tsipam whoami` SHALL report, as a JSON result, what the server resolved for
the calling request: `identified`, `login` (empty for a tagged node),
`node`, `tags`, `capability`, the decoded capability values as `grants`,
and `ignored_values`, the count of values that did not parse. Under
sanitization, `login` and `node` SHALL be redacted as owner values are.

#### Scenario: A caller with no grant sees why

- **WHEN** a member with no tsipam capability runs
  `tsipam whoami --server https://tsipam.example.ts.net`
- **THEN** it exits 0 and reports `identified: true`, the member's login,
  and an empty `grants` list

### Requirement: Whoami is never denied and needs a server

`tsipam whoami` SHALL need no action. It SHALL return `identified: false`
with exit 0 when the caller cannot be resolved, instead of being denied. It
SHALL require `--server` or `TSIPAM_SERVER` and exit 2 without one.

#### Scenario: An unresolved caller is reported, not denied

- **WHEN** the server's identity lookup fails for a `whoami` request
- **THEN** the command exits 0 and reports `identified: false`

#### Scenario: Whoami needs a server

- **WHEN** `tsipam whoami` runs with neither `--server` nor `TSIPAM_SERVER`
- **THEN** it exits 2 with empty stdout and stderr naming `--server`
