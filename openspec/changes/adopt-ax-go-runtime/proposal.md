# Proposal: Adopt the ax-go CLI runtime

## Why

`tsipam` ports the AX conventions by hand, so an agent gets plain-text
failures it has to scrape, no preview before a registry write, no MCP access,
and no way to tie a failed command to its API calls. `ax-go` supplies that
contract for the rest of the portfolio. Issue #4 was gated on
rshade/tailscale-utils#394 only while this module lived in that monorepo;
`tsipam` now makes the dependency decision for itself.

## What Changes

- Run every command through `github.com/rshade/ax-go` v0.10.0, under a scoped
  `CONTEXT.md` exception to "No new personal-repo dependencies". Merging this
  proposal is that decision.
- **BREAKING** JSON becomes the default when stdout is not a terminal. The
  order is `--format`, then `AGENT_MODE`, then terminal detection. Scripts
  that read human output need `--format human` or `AGENT_MODE=human`.
- **BREAKING** JSON results become one minified
  `{"data":{...},"meta":{...}}` line. `data` holds today's object with
  `schema_version` `"2"`; `meta` carries trace, span, and idempotency IDs and
  `dry_run`.
- **BREAKING** A failure is one JSON error object on stderr, in both output
  modes, with `error_code`, `message`, and recovery fields, in place of the
  `tsipam: <message>` line.
- **BREAKING** The four `release` commands need `--yes`, or a typed
  confirmation at a terminal.
- Add `--dry-run`: the nine mutating commands report what they would do and
  create or change no file.
- Add a flag for every positional argument (`--id`, `--cidr`, `--pool`, and
  so on), so a command can be called with flags alone. Positional arguments
  keep working.
- Add `tsipam mcp-server`, over stdio only: every leaf command except `check`
  is a tool, including reserve and release.
- Add `__schema` and `__schema --as=mcp`. Keep `-schema` as a supported
  catalog.
- Add `--idempotency-key`, accepted and echoed but not stored.
- Accept `TRACEPARENT`, echo its trace ID, and record identity-free spans for
  each service call and Tailscale API read. No telemetry leaves the machine.
- Verbose logs become structured JSON lines that carry the trace ID.

The exit codes stay `0`, `1`, and `2` with today's meanings. The registry
format does not change.

## Capabilities

### New Capabilities

- `output-mode`: how a command chooses between human and JSON output.
- `output-envelope`: the JSON success document, its metadata, and which
  fields vary between runs.
- `error-envelope`: the JSON failure document, its stable codes, its exit
  codes, and its redaction.
- `mutation-safety`: rehearsing a registry write, confirming a release, and
  what an idempotency key does.
- `command-inputs`: supplying a command's required values by position or by
  flag.
- `mcp-server`: serving the CLI to an MCP client over stdio.
- `diagnostics`: structured logs, trace correlation, the no-export rule, and
  version reporting.

### Modified Capabilities

- `schema`: the catalog gains the new commands and flags, and `__schema`
  joins `-schema`.
- `siteid`, `subnet`, `service-address-reservations`, `udpport`: release
  requires approval, and reserve, allocate, and release support `--dry-run`.
- `calc`: `info` gains `--cidr`, `aggregate` gains a repeatable `--cidr`, and
  the `next-free` shell composition selects human output.
- `plan-check`, `policy-check`: the report keeps stdout, and the drift verdict
  is an error object on stderr.

## Impact

- **Code:** `cmd/tsipam` (lifecycle, error boundary, inputs, rendering, MCP),
  `internal/registry` and both stores (`TxOptions.DryRun`), `internal/ipamsvc`,
  `internal/ipamhttp`, `internal/ipamconn`, `internal/devices`, and
  `internal/diagnostics`.
- **Depends on:** `refactor-client-server-boundary`, from task group 3 on.
- **API:** `design/` gains a `dry_run` attribute on nine payloads; `gen/` is
  regenerated. No other service change.
- **Dependencies:** `ax-go` v0.10.0 (Apache-2.0) and about 30 transitive
  modules, including OpenTelemetry, the MCP Go SDK, and zerolog. A prototype
  took the stripped linux/amd64 binary from 17.1 MB to 24.2 MB, or 20.1 MB
  with the release build tags. `github.com/segmentio/asm` (MIT-0) fails
  `license-check` until it gets a license exception file.
- **Policy:** `CONTEXT.md` gains the `ax-go` exception and describes
  `mcp-server` as a client-owned stdio process with no listener.
- **Docs and smoke:** README, the exit-code, JSON-envelope, and AX-convention
  pages, two how-to guides, the tutorial, a new MCP how-to guide, and every
  smoke case that reads JSON or a failure.
- **In-flight changes:** `add-registry-export-import`, `add-sqlite-store`,
  `add-serve-tsnet`, and `add-server-observability` add commands and stores
  that must meet these contracts. Whichever lands second reconciles; the
  design lists the points of contact.
- **Release:** `feat!:`. release-please bumps the minor version before 1.0.
