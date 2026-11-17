# Remote Client Specification Delta

## Purpose

Let every registry-backed command run against a shared `tsipam serve`
instead of local storage, with the same output, exit codes, and
sanitization as local mode.

## ADDED Requirements

### Requirement: Select a server with --server or TSIPAM_SERVER

Every registry-backed command SHALL accept `--server <url>`, and SHALL read
`TSIPAM_SERVER` when the flag is absent. The URL SHALL use the `https`
scheme and SHALL NOT carry a path, query, or user info. When a server is
selected, the command SHALL send its request to that server instead of
running the in-process loopback.

#### Scenario: The flag selects remote mode

- **WHEN** an operator runs
  `tsipam subnet list --server https://tsipam.example.ts.net --tailnet production`
- **THEN** the command lists the `production` registry held by that server

#### Scenario: A non-HTTPS URL is a usage error

- **WHEN** an operator passes `--server http://tsipam.example.ts.net`
- **THEN** the command exits 2 before any network request, with
  `--server must be an https:// URL` on stderr

### Requirement: Remote and local storage selection are exclusive

`--server` together with an explicit `--registry` or `--registry-dir` flag
SHALL be a usage error that exits 2 before any request. An explicit local
flag SHALL take precedence over `TSIPAM_SERVER`. `TSIPAM_SERVER` and
`TSIPAM_REGISTRY` both set, with neither flag given, SHALL be a usage error.

#### Scenario: Both flags are refused

- **WHEN** an operator runs `site-id list --server https://tsipam.example.ts.net --registry-dir .`
- **THEN** the command exits 2 with
  `--server cannot be combined with --registry or --registry-dir; the server owns its storage`
  on stderr and writes nothing to stdout

#### Scenario: An explicit local flag overrides the environment

- **WHEN** `TSIPAM_SERVER` is set and an operator runs
  `site-id list --registry-dir ./registries`
- **THEN** the command reads the local file registry and sends no request
  to the server

#### Scenario: Conflicting environment is refused

- **WHEN** `TSIPAM_SERVER` and `TSIPAM_REGISTRY` are both set and no
  storage flag is given
- **THEN** the command exits 2 with a stderr message naming both variables

### Requirement: Remote results match local results

Remote mode SHALL produce the same human output, JSON envelope, and exit
code as local mode for the same registry state and request, including
collisions (exit 1), drift (exit 1 for `check` and `check-policy`), no-op
reserves and releases (`status: "no_changes"`), and invalid input (exit 2).

#### Scenario: A remote collision exits 1

- **WHEN** site ID 7 is reserved for `hq` on the server and a client runs
  `site-id reserve 7 branch --server https://tsipam.example.ts.net`
- **THEN** the client exits 1 with the same stderr message local mode
  prints, and stdout is empty

### Requirement: Transport failures exit 2

A remote command that cannot complete because the server is unreachable,
the TLS handshake or certificate check fails, the server answers with a
response that is not part of the API, or the request is denied SHALL exit 2
with empty stdout and a stderr message naming the kind of failure.
Credentials and response bodies SHALL NOT be printed.

#### Scenario: The server is unreachable

- **WHEN** the device running the client is not connected to the tailnet
  and it runs `subnet list --server https://tsipam.example.ts.net`
- **THEN** the command exits 2, stdout is empty, and stderr says the
  server could not be reached

#### Scenario: A certificate that does not match is refused

- **WHEN** the server presents a certificate whose name does not match the
  URL host
- **THEN** the command exits 2 and sends no request body

### Requirement: Remote commands need no Tailscale API credentials

In remote mode, `plan`, `check`, `subnet allocate`, and `calc next-free`
SHALL NOT read `TAILSCALE_API_KEY`, `TAILSCALE_OAUTH_CLIENT_ID`, or
`TAILSCALE_OAUTH_CLIENT_SECRET`. The server SHALL perform their live reads.

#### Scenario: CI allocates without API credentials

- **WHEN** a CI job with no Tailscale API variables runs
  `subnet allocate 10.40.0.0/16 --size 24 preview --ticket PR-12 --server https://tsipam.example.ts.net`
- **THEN** the allocation succeeds using the server's live route read

### Requirement: Sanitization in remote mode

When sanitization is active, a remote command SHALL first fetch the
registry's identity values from the server and register them exactly as
local mode registers values read from the file. It SHALL also register the
server URL's host and its MagicDNS domain. If the identity values cannot be
fetched, the command SHALL fail with exit 2 before running the operation.

#### Scenario: The server name is redacted

- **WHEN** `TAILSCALE_SANITIZE=identity` is set and a remote command fails
  with an error that mentions `tsipam.example.ts.net`
- **THEN** stderr shows a placeholder instead of that host name

#### Scenario: Sanitization fails closed

- **WHEN** sanitization is active and the server denies the identity-value
  request
- **THEN** the command exits 2 with empty stdout before the operation runs,
  and stderr says that sanitized remote output requires `read` access to
  the registry alias
