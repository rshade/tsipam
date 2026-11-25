# MCP Server Specification Delta

## Purpose

Exposes the CLI to an MCP client on the same machine, with the same rules,
redaction, and approval as the command line and no network listener.

## ADDED Requirements

### Requirement: Serve the CLI over stdio

`tsipam mcp-server` SHALL serve the Model Context Protocol on stdin and
stdout until the client closes the stream or the process is interrupted. It
SHALL open no network listener, offer no network transport, and start only
when run explicitly. Logs SHALL go to stderr.

#### Scenario: A client lists tools

- **WHEN** a client initializes and sends `tools/list`
- **THEN** the server answers on stdout and stdout carries only protocol
  messages

#### Scenario: No network transport

- **WHEN** `tsipam mcp-server --transport http` runs
- **THEN** it exits 2 as an unknown flag

#### Scenario: Nothing outlives the client

- **WHEN** the client closes stdin
- **THEN** the process exits and no listener or file remains

### Requirement: Every leaf command except check is a tool

The tool list SHALL hold one tool per leaf command, named by its path with
hyphens, except `check`, whose report `plan` returns. It SHALL NOT hold the
root, a command group, `__schema`, `mcp-server`, `help`, or `completion`.
`__schema --as=mcp` SHALL list the same tools.

#### Scenario: The tool set is pinned

- **WHEN** a client sends `tools/list`
- **THEN** the result is exactly 20 tools, including `tsipam-site-id-release`,
  `tsipam-calc-split`, `tsipam-plan`, and `tsipam-check-policy`, and not
  `tsipam-check` or `tsipam`

#### Scenario: The static and live lists agree

- **WHEN** `__schema --as=mcp` and `tools/list` are compared
- **THEN** they name the same tools

#### Scenario: A new command must be decided

- **WHEN** a leaf command is added without updating the pinned tool set
- **THEN** the test suite fails

### Requirement: A tool call matches the command line

A successful call SHALL return the bytes the same command writes to stdout in
JSON mode, after masking the published run-varying fields. A failed call SHALL
return the error object and SHALL NOT stop the server.

#### Scenario: List matches the CLI

- **WHEN** a client calls `tsipam-site-id-list` with a registry directory
- **THEN** the masked result equals the masked CLI output

#### Scenario: Calculation through flags

- **WHEN** a client calls `tsipam-calc-split` with `cidr` `10.0.0.0/24` and
  `prefix` 26
- **THEN** the result's `.data.subnets` holds four CIDRs

#### Scenario: Policy drift is an error with a count

- **WHEN** `tsipam-check-policy` finds drift
- **THEN** the call fails with `policy_drift_detected` and its `context` holds
  `drift_count`

### Requirement: Approval is per call

A `release` tool call SHALL proceed only when that call passes `yes: true`, or
passes `dry-run: true`, which writes nothing even beside `yes: true`. The
server SHALL NOT supply approval and SHALL NOT prompt.

#### Scenario: Release without approval

- **WHEN** a client calls `tsipam-site-id-release` with `id` and no `yes`
- **THEN** the call fails with `confirmation_required` and the registry bytes
  are unchanged

#### Scenario: Approval does not carry over

- **WHEN** one call passes `yes: true` and the next release call omits it
- **THEN** the second call fails with `confirmation_required`

### Requirement: Calls share no redaction state

Each call SHALL resolve its tailnet and sanitization afresh. No placeholder
or identity from one call SHALL appear in, or affect, another. A call SHALL
NOT lower the level that `TAILSCALE_SANITIZE` sets.

#### Scenario: Two tailnets in one session

- **WHEN** a client calls `tsipam-site-id-list` for tailnet `a` and then for
  tailnet `b`, both sanitized
- **THEN** each result equals what a separate process returns for that tailnet
  with the same key

#### Scenario: A client cannot disable redaction

- **WHEN** the server runs with `TAILSCALE_SANITIZE=identity` and a call
  passes `sanitize: off`
- **THEN** the result is sanitized
