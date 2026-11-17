# Server Specification Delta

## Purpose

Run the tsipam registry API as a long-lived Tailscale Service so that
several people and CI jobs can share one registry over the tailnet without
holding its storage or Tailscale API credentials themselves.

## ADDED Requirements

### Requirement: Serve the registry API as a Tailscale Service

`tsipam serve` SHALL join the tailnet as its own node and serve the registry
API over HTTPS on port 443 as the Tailscale Service named by `--service`
(default `svc:tsipam`). It SHALL accept connections only through that
Service and SHALL NOT listen on any other tailnet or host address, except
the optional health listener.

#### Scenario: A tailnet client reaches the Service by name

- **WHEN** `tsipam serve` is running and the Service is approved, and a
  device on the tailnet requests `https://tsipam.example.ts.net/`
- **THEN** the request is served over TLS with a certificate for
  `tsipam.example.ts.net`

#### Scenario: Plain HTTP is not served on the tailnet

- **WHEN** a tailnet client connects to the Service on port 80
- **THEN** no registry API response is returned

### Requirement: Server nodes are tagged

`tsipam serve` SHALL advertise the tags given by `--advertise-tags`, and
SHALL refuse to start when none are given, because Tailscale Service hosts
must be tagged nodes. Joining SHALL use an auth key from `TS_AUTHKEY`. The
key SHALL NOT appear in any output.

#### Scenario: Missing tags fail fast

- **WHEN** `tsipam serve --registry sqlite:///data/tsipam.db` runs without
  `--advertise-tags`
- **THEN** it exits 2 before joining the tailnet, with a stderr message
  naming `--advertise-tags`

#### Scenario: An unapproved Service is reported

- **WHEN** the node is up but the control plane has not approved it as a
  host for the Service
- **THEN** the server logs a warning that names the Service and states that
  it needs approval or a matching auto-approver, and keeps running

### Requirement: Serve requires a SQLite registry

`tsipam serve` SHALL require `--registry` with a `sqlite://` URL and SHALL
refuse `file://` registries and `--registry-dir`, exiting 2 before joining
the tailnet. One server process SHALL serve every registry alias stored in
its database.

#### Scenario: A file registry is refused

- **WHEN** an operator runs `tsipam serve --registry file://./registries`
- **THEN** the command exits 2 with
  `tsipam serve requires a sqlite:// registry` on stderr

### Requirement: Persistent node state

`tsipam serve` SHALL keep its tailnet node state in the directory given by
`--state-dir`, and SHALL reuse that identity across restarts so the Service
host does not change on every restart. The directory SHALL be created with
owner-only permissions when absent.

#### Scenario: A restart keeps the node identity

- **WHEN** the server stops and starts again with the same `--state-dir`
- **THEN** it rejoins as the same tailnet node without using a new auth key

### Requirement: The server performs live reads

The server SHALL read live tailnet state with the same credential variables
`devices list` uses (`TAILSCALE_TAILNET`, then `TAILSCALE_API_KEY` or the
OAuth client pair), set in its own environment. When those are missing it
SHALL still serve registry-only methods and SHALL fail live-read methods
with a stderr-visible client error that names the missing configuration.

#### Scenario: Registry methods work without live credentials

- **WHEN** the server has no Tailscale API credentials and a client runs
  `subnet list --server https://tsipam.example.ts.net`
- **THEN** the list succeeds

#### Scenario: Live methods name the missing server configuration

- **WHEN** the same server receives `plan`
- **THEN** the client exits 2 with empty stdout, and stderr states that the
  server has no Tailscale API credentials configured

### Requirement: Health endpoint off the tailnet

When `--health-listen <addr>` is given, the server SHALL serve
`GET /healthz` on that host address, outside the tailnet. It SHALL return
200 once the registry opens and the tailnet node is running, and 503
otherwise. The endpoint SHALL expose no registry data.

#### Scenario: Health reflects readiness

- **WHEN** the server has opened its registry but the tailnet node is still
  starting
- **THEN** `GET /healthz` on the health address returns 503

### Requirement: Graceful shutdown

On SIGINT or SIGTERM the server SHALL stop accepting connections, finish
in-flight requests for up to 10 seconds, close the registry, and exit 0. A
request still running at the deadline SHALL be cancelled without leaving a
partial registry write.

#### Scenario: Termination during a reserve

- **WHEN** SIGTERM arrives while a reserve request is in flight
- **THEN** the reserve either completes and is recorded, or has no effect,
  and the process exits 0
