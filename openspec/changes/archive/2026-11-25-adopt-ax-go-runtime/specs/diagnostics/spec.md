# Diagnostics Specification Delta

## Purpose

Defines the structured logs, trace correlation, and version reporting that let
an operator follow one run, without exposing identities or leaving the machine.

## ADDED Requirements

### Requirement: Logs are structured and quiet by default

Log events SHALL be JSON lines on stderr carrying the run's trace ID.
Without `--verbose` only warnings and errors SHALL be logged, plus one notice
when a dry run suppresses a write; with it, debug events and the before and
after lines for each registry write SHALL be logged. Log events SHALL NOT
contain identities when sanitization is active.

#### Scenario: A quiet success

- **WHEN** `site-id reserve 1 hq` succeeds without `--verbose` or `--dry-run`
- **THEN** stderr is empty

#### Scenario: Verbose writes are audited without identities

- **WHEN** a collision is refused with `--verbose` and
  `TAILSCALE_SANITIZE=identity`
- **THEN** stderr has the before and failure lines and none of them contains a
  site, service, CIDR, or tailnet value

### Requirement: A caller's trace is continued

When `TRACEPARENT` holds a valid W3C trace context, results and errors SHALL
report its trace ID. Otherwise each run SHALL have a new one.

#### Scenario: The trace ID is echoed

- **WHEN** `site-id list --format json` runs with `TRACEPARENT` set to a valid
  context
- **THEN** `.meta.trace_id` is the trace ID from that context

#### Scenario: A failure carries the same trace

- **WHEN** a command fails with `TRACEPARENT` set
- **THEN** the error object's `trace_id` is the trace ID from that context

### Requirement: Spans cover service calls and API reads

Each service call SHALL be a child span of the command's span, named for the
service method, and each Tailscale API read SHALL be a child span of the call
or command that made it. Span names and attributes SHALL contain no URL and no
identity.

#### Scenario: A service call is a child span

- **WHEN** `site-id list` runs with `AX_OTEL_DEBUG=1`
- **THEN** stderr shows a span for `list_site_ids` whose parent is the
  command's span, and stdout is unchanged

#### Scenario: A debug dump hides identities

- **WHEN** a collision is refused with `AX_OTEL_DEBUG=1` and
  `TAILSCALE_SANITIZE=identity`
- **THEN** the dump contains no site, service, CIDR, owner, path, or tailnet
  value

### Requirement: Telemetry never leaves the machine

`tsipam` SHALL NOT export traces, metrics, or logs to any network endpoint,
whatever the environment sets, and SHALL NOT send trace headers to the
Tailscale API.

#### Scenario: An exporter endpoint is ignored

- **WHEN** a command runs with `OTEL_EXPORTER_OTLP_ENDPOINT` set to a local
  listener
- **THEN** the listener receives nothing

#### Scenario: API requests carry no trace header

- **WHEN** `devices list` runs with `TRACEPARENT` set
- **THEN** the API requests have no `traceparent` header

### Requirement: Versions are real and consistent

`--version` SHALL print `tsipam`, the resolved version without a leading `v`,
and the embedded revision when the build records one. The resolved version
SHALL NOT be `dev` or empty. `__schema`, the error object, and the MCP
handshake SHALL report the same resolved version.

#### Scenario: A source build reports a version

- **WHEN** a binary built with `go build` and no injected version runs
  `--version`
- **THEN** the output names a version other than `dev`

#### Scenario: Release and module installs agree

- **WHEN** a release archive and `go install` of the same tag run `--version`
- **THEN** both print the same version
