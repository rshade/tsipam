# Design: Adopt the ax-go CLI runtime

## Context

See `proposal.md` for motivation. Issue #4 was written against the old
`tail-ipam` layout and `ax-go` v0.7.0. These facts were checked against this
repository and a prototype on v0.10.0, and they differ from the issue:

| Issue #4 says | Observed |
| --- | --- |
| Four error sources need conversion | Cobra also validates required flags and flag groups after every hook. With only a straight `ax.Execute`, `calc split` without `--prefix` moved from exit 2 to exit 1 `internal_error`, and so did `completion bash extra`. |
| Every leaf becomes an MCP tool | The live server drops a command whose `Args` rejects zero arguments. Seven tools remained, all read-only, while `__schema --as=mcp` listed 22. |
| Pre-register three flags | v0.9.0 added a fourth, `--strict`. It buffers stdout, so `check-policy --strict` on drift printed 0 bytes instead of 201, and a full output device exited 0. |
| `reserved_at` is caller-supplied, so nothing in `data` varies | `internal/registry/entry.go` stamps it from the clock when metadata is first added. |
| Thread `dry_run` through nine registry functions | `refactor-registry-store` replaced them with `registry.Ledger` over a `registry.Store`. |
| Every failure leaves stdout empty | `plan-check` and `policy-check` require the report on stdout before the drift failure. |
| goa pins differ | Both are 3.35.0. Renovate PRs #32 and #33 move them to 3.36.0 together. |
| `add-subnet-allocate` must be archived first | It is archived. |

Constraints that shape the approach:

- `CONTEXT.md` forbids new personal-repo dependencies, forbids a listener
  outside `serve`, and lists the outbound paths; telemetry export is not one.
- `ax.Execute` has no hook to map an error it did not create
  (rshade/ax-go#331, open).
- The MCP dispatcher runs the same Cobra tree repeatedly in one process and
  resets flags but not closures.

## Goals / Non-Goals

**Goals:**

- One place decides every failure's code, exit status, and redaction.
- The rehearsal path is the real path minus the commit, in every store.
- Nothing `ax-go` writes by itself can carry an unsanitized identity.
- Each workaround for an `ax-go` gap is isolated, tested, and removable.

**Non-Goals:**

- Exit codes 3, 4, or 10.
- Storing idempotency keys or minting reservation IDs.
- An MCP network transport, or MCP access to a remote `tsipam serve`.
- `ax.ParseConfig`/`ax.PatchConfig`: the patcher normalizes whitespace, which
  breaks the byte-exact comment guarantee.
- NDJSON, `ax.GRPCDial`, Loki, and `ax-go` configuration loading.
- Changing `tsacl`.

## Decisions

### D1. tsipam records its own dependency exception

`CONTEXT.md` gains a second deliberate exception, which this proposal's merge
approves. Proposed text for the existing bullet:

> **No new personal-repo dependencies.** There are two deliberate exceptions.
> `github.com/jaxxstorm/tscli` is the Tailscale API client (and through it
> `viper`). `github.com/rshade/ax-go` is the CLI runtime: the root package and
> `mcp` in `cmd/tsipam` only, and `axtest` in tests only. It brings the
> OpenTelemetry SDK, the MCP Go SDK, and zerolog, and it has one maintainer.
> `goa` is a deliberate framework dependency. Adding another needs the same
> explicit decision.

Only `cmd/tsipam` imports `ax-go`. `internal/` packages keep their own small
interfaces (`diagnostics.Logger`) and use the OpenTelemetry API directly for
spans, so replacing the runtime touches one package. An import test enforces
this.

*Alternative:* wait for rshade/tailscale-utils#394. Rejected: that gate applied
while this module lived there.

### D2. Pin v0.10.0

It is the latest release, requires Go 1.27.3 (this module uses 1.27.4), and
has capability classes, flag enums, and registered error codes. Pending
v0.11.0 (rshade/ax-go#322) keeps stdout on a non-zero MCP call and adds
per-flag exclusion. Until then D9 leaves `check` out of MCP. Renovate will
raise the bump; the pinned tool-set test makes revisiting D9 deliberate.

### D3. Lifecycle

`runCLI` keeps its signature (57 test call sites). It:

1. Builds the root and the whole tree, including `ax.NewSchemaCommand`,
   `mcp-server`, and Cobra's `help` and `completion` commands, so later steps
   see every command that can run.
2. Registers `dry-run`, `yes`, `strict`, and `idempotency-key` as persistent
   flags. `cliflags.Normalize` rewrites only flags it can see, and the
   prototype rejected `-dry-run` because `ax.Execute` registers too late.
3. Installs the error boundary (D4) and applies `cliflags.Normalize`.
4. Calls `ax.Execute` with the streams, the filtered environment (D10),
   `WithStdoutIsTTY` computed from the stdout passed in, the resolved version,
   and a logger flush.

Computing the terminal state from the writer, not `os.Stdout`, makes a test's
buffer behave like a pipe. Stdin and its terminal state are injected below
`runCLI`, whose signature has no stdin, for D7.

### D4. One error boundary, `toAxError`

`toAxError(cmd, state, err)` returns an `*ax.Error` with an explicit code and
exit status, and passes an existing one through. In order, it adds the known
identity inventory (skipped for the two drift sentinels, as today), unwraps
the transport fault, classifies, then sanitizes. A tree walk installs it on
every `RunE` and `Args`, and `SetFlagErrorFunc` and the root
`PersistentPreRunE` use it.

The root `PersistentPreRunE` also calls `ValidateRequiredFlags` and
`ValidateFlagGroups` on the invoked command and converts their errors. Cobra
repeats both checks later and they then pass. This closes the gap in the table
above without giving up `MarkFlagRequired`, which MCP input schemas read.

Classification uses types and goa error names, never message text, with
`command_failed` (exit 2) as the default. `internal_error` therefore appears
only if a path escaped the boundary, and a test fails on it.

| Source | Code |
| --- | --- |
| Cobra, flag, input, and `invalid_input` errors | `validation_error` |
| `collision`, and the four registry collision types | `registry_collision` |
| `ipcalc.ErrTooManyChildren` | `split_too_large` |
| `exhausted`, and the next-free exhaustion sentinel (new) | `pool_exhausted` |
| `errRegistryDrift`, `errPolicyDrift` | `drift_detected`, `policy_drift_detected` |
| Missing tailnet or credentials (new CLI error) | `credentials_missing` |
| API read in the CLI process (new CLI error) | `collection_failed` |
| `comparison_failed`, `policy_check_failed` | same names |
| Undeclared service fault from a registry method | `registry_failed` |
| Stdout write (new typed error from `emitOutput`) | `output_failed` |
| `context.Canceled` | `canceled` |

`plan` and `check` collect behind the loopback, where Go types do not survive.
They check credentials before the call, and `ipamsvc` sets goa's existing
`timeout` and `temporary` error fields so the CLI can fill `retryable`. No
design change is needed for that. `internal/devices` returns its own typed
errors; `devices.go` and `target.go`, the two command files allowed to import
it, turn them into the CLI errors in the table.

Failures before `PersistentPreRunE` (flags, argument counts) have no sanitizer
yet. When the environment or parsed flag asks for sanitization, the boundary
builds one and registers every argv value that is not a command or flag name,
so Cobra's `unknown command "hq-secret"` is swept like any other message.

*Alternative:* capture and rewrite `ax.Execute`'s stderr. Rejected: it parses
our own output and would hide the span status, which also carries the text.

### D5. Output mode and envelope

`--format` defaults to empty. The resolved mode is read from the context, and
`commandState.format` goes away. `ax.ParseMode` also accepts `agent`,
`machine`, and `text`; the existing `human|json` check stays and runs after it.

`renderOutput` writes `ax.NewEnvelope(ctx, *result)` through `ax.WriteJSON`.
`outputSchemaVersion` becomes `"2"`. `WithNonDeterministicFields` gets a small
marker type with `reserved_at` for allocate and for site-ID, subnet, and
service reserve, and an empty type elsewhere (port reserve has no metadata);
tagging `outputEnvelope` itself would mark stored `reserved_at`
values in lists as volatile, which they are not.

JSON when piped breaks the README's `$(tsipam calc next-free ...)`, so that
example gains `--format human`. `AGENT_MODE=human` restores the old default
for a whole script, and the smoke harness sets it so README commands stay
verbatim; one case runs without it.

*Alternative:* pass `WithStdoutIsTTY(true)` so human stays the default, as
rshade/tailscale-utils#394 recommends. Rejected by the owner in favor of full `ax-go` precedence.

### D6. Dry run is a store transaction without a commit

`registry.TxOptions` gains `DryRun`, and the `Store` contract gains a rule:

> Rule 9, rehearsal. Update with DryRun validates and runs fn as a real Update
> does, then discards every mutation. It persists nothing, creates nothing
> (CreateIfMissing supplies an empty in-memory registry), does not run the
> WithAfterSuccess hook, and need not serialize.

`Ledger.Rehearsal()` returns a copy that sets `DryRun` on every `Update`, so
no rule method changes. `ipamsvc` picks it when the payload's `dry_run` is
true. The HuJSON store skips the lock, reads the file (or an empty document if
the directory exists), runs `fn` on the same buffered transaction, and
returns. `storetest` gains the rule, so `add-sqlite-store` must meet it too. A
rolled-back `BEGIN IMMEDIATE` does not: it takes the write lock, can create
the database and its `-wal` and `-shm` files, and fails in a read-only
directory, all of which `mutation-safety` rules out.

In `design/`, the five mutating POST methods carry `dry_run` in the body, and
the four DELETE methods carry it as a query parameter.

The CLI calls `ax.PerformWithAudit` with an identity-free description and one
function for both callbacks, which sends `dry_run` from the context. The
commit callback returns an already converted error (D4), because the audit
line logs it verbatim. Auditing is on only with `--verbose`: by default it
adds two info lines to every write.

*Alternative:* rehearse under the lock, as the issue says. Rejected: it
creates and removes `.lock`, fails in a read-only directory, and still proves
nothing about a later run.

### D7. Confirmation

Release commands call `ax.Confirm` before the service call unless the run is a
rehearsal. `ConfirmationPromptRequired` leads to a prompt on stderr only when
stdin is a terminal; EOF, a decline, or no terminal yields
`confirmation_required`. `Args` and inputs are validated first, so a typo is
not hidden behind the prompt.

### D8. Inputs by flag

`cmd/tsipam/inputs.go` holds one helper. `bindInputs(cmd, specs...)` registers
the flags, sets an `Args` validator that accepts zero or the full count, and
`resolve(cmd, args)` returns the values as the positional slice the leaf code
already uses. The bodies of the leaf commands keep `args[0]` and `args[1]`.

Presence is checked in `resolve`, not in `Args`, because the MCP server
decides whether a command is a tool by calling `Args` with no arguments. The
flags are not Cobra-required for the same reason: a positional call would
fail. Mixed sources are refused, so there is never a precedence rule.

`calc aggregate` moves its source checks into the same place and gains
`--cidr` as a string array.

*Alternative:* ship the seven read-only tools and wait for `ax-go` to map
positional arguments. Rejected by the owner.

### D9. MCP server

`mcp-server` is tsipam's own command calling `mcp.Serve`, not
`mcp.NewCommand`, which would add `--transport http`, an unauthenticated
endpoint that could write the registry. `CONTEXT.md` allows no listener
outside `serve`. Its "No daemon" bullet gains:

> `tsipam mcp-server` is not a daemon: an MCP client starts it as a child,
> it speaks only over that client's stdio, opens no listener, and exits when
> the client closes the stream.

`mcp.Exclude` covers the root and `check`. With v0.10.0 a non-zero call
discards stdout, so `check` would return drift without the rows, and `plan`
returns the same report as data. `check-policy` has no such twin and stays; on
drift the agent gets `drift_count` only, until v0.11.0.

The root `PersistentPreRunE` resets `sanitizer`, `sanitizeResolution`, and
`logger` first, because `outputSanitizer` caches across calls. A pinned list of
tool names makes each new command a decision; `add-serve-tsnet` must exclude
`serve`.

The server gives its client the authority the user already has in a shell: it
can read any registry directory and policy file the user can, and write
registries. Tool annotations mark release and reserve as destructive (`mutate`
maps to that hint) and allocate as not, because a repeat reserve can overwrite
metadata.

### D10. Telemetry stays local and identity-free

`tsipam` passes `ax.Execute` an environment lookup that hides
`OTEL_EXPORTER_OTLP_ENDPOINT`, the only switch that builds an exporter. That
holds for `go install` builds too, which build tags would not cover. Release
archives also build with `ax_no_grpc,ax_no_otlp`, which saves 4 MB, and a
`go list -deps` test keeps the exporters and gRPC out of that configuration.

`otelhttp` is not used. Its client spans record `url.full`, and these URLs
carry the tailnet, CIDR, and site; `AX_OTEL_DEBUG` would print them past the
sanitizer. Instead `internal/ipamhttp` extracts `traceparent` and a goa
endpoint middleware there starts a span named for the method, and a round
tripper in `internal/ipamconn` injects `traceparent`. Local mode and `serve`
therefore share one implementation. API reads get a span named for the
operation, and no header is added to `api.tailscale.com` requests. This also
removes the need for a transport-wrapping option in `ax-go`.

### D11. Strict mode

Our `PersistentPreRunE` runs after `ax-go` swaps stdout for its buffer. When
`--strict` is set outside MCP, it puts the real writer back. Nothing emits
warnings, so there is nothing to withhold. The workaround has its own tests
and comes out when `ax-go` fixes the two bugs.

*Alternative:* reject `--strict`. Rejected: callers may pass it to every tool.

### D12. Version

`--version` prints `tsipam <version> (<revision>)`. The version is
`ax.ResolveVersion(moduleVersion(...))` without a leading `v`, which keeps the
archive and `go install` strings equal (PR #2). The revision suffix stays,
because the AX conventions page promises build provenance; it is dropped when
the version already is the revision.

### D13. `-schema` stays

It carries `exit_codes` and `sanitization`, which `__schema` cannot. Its
content changes only by the new commands and flags.

### D14. Delivery

Pull requests, stacked, in task-group order. Groups 1 to 3 change no public
output and can land alone. `refactor-client-server-boundary` lands before group
3, which works in the packages it adds. Groups 4 to 8 are breaking or additive
to the contract and land together in one release, so users see one migration.

## Points of contact with in-flight changes

| Change | Contact |
| --- | --- |
| `add-registry-export-import` | `import` must honor `--dry-run` (the same as `--plan`), declare its class, and take its source by flag too, and it joins the command lists in `mutation-safety` and `command-inputs`. Its refusals exit 1, and a refused `--plan` leaves its report on stdout, so whichever change lands second adds the exit-1 code `import_refused` and modifies "Only a verdict leaves a result on stdout", "Error codes are stable and fix the exit code", and "An unknown answer never exits 1". `export` writes the bare document in human mode and an envelope in JSON mode. A pipe or redirect now selects JSON, so its examples, its `export \| import -` scenario, and the migration plan in `add-sqlite-store` need `--format human` (or `--output` for a file). |
| `add-sqlite-store` | The store must pass rule 9 in `storetest` without the write lock, without migrating, and without creating the database or its `-wal` and `-shm` files. An ordinary read-only WAL connection creates both files and cannot open the database in a read-only directory, so that change must choose another read path or modify "Dry run rehearses without writing". `registry history` needs a class and a place in the pinned tool set. Its output comparisons mask the published run-varying fields and the error object's `trace_id`. |
| `refactor-client-server-boundary` | Lands first. New command files stay within its import test. |
| `add-serve-tsnet` | Exclude `serve` from MCP and decide `whoami`. `--server` and `TSIPAM_SERVER` would give every registry tool remote access, a non-goal here. That change lifts the non-goal for `TSIPAM_SERVER` only, set in the environment by whoever starts `mcp-server`, and refuses a per-call `server` input, so a client cannot choose the server. Its handler and client carry this trace propagation; a remote caller's `traceparent` is used only for correlation, never for identity or authorization. A `dry_run` request needs the same capability as the real action, and leaves no audit event. `forbidden` and server failures need codes. Its claim that the envelope does not change now means version 2. Its output comparisons mask the published run-varying fields and the error object's `trace_id`. |
| `add-server-observability` | `drift` needs a class and a place in the pinned tool set. |

## Risks / Trade-offs

- [A path escapes the boundary and exits 1] → Command-tree test over every
  command and failure class that fails on `internal_error`, plus the smoke
  cases.
- [JSON by default breaks scripts silently: `$(...)` captures a document
  instead of a CIDR] → `subnet reserve` rejects it as a malformed CIDR, so the
  registry is safe. Release notes lead with `--format human` and
  `AGENT_MODE=human`.
- [Human-mode users see JSON errors] → Accepted: `ax.Execute` has no other
  rendering.
- [An MCP client writes the registry unattended] → Reserve and allocate
  never take another holder's key, though a repeat reserve can overwrite
  metadata; release needs a per-call `yes`.
- [stdio responses can be lost if a client closes stdin right after a request
  (rshade/ax-go#298)] → Documented; affects one-shot pipes, not MCP clients.
- [Dependency and binary growth: about 30 modules, +18% with release tags] →
  Recorded in the PR. `govulncheck` was clean in the prototype.
- [`segmentio/asm` is MIT-0, which `go-licenses` does not classify] → A
  license exception file with its text, which `notice.sh` also ships. MIT-0 is
  more permissive than MIT.
- [Single-maintainer runtime] → D1 keeps imports to one package.

## Migration Plan

1. Land groups 1 to 3 behind no flag; behavior is unchanged.
2. Land groups 4 to 8 as `feat!:` with a migration table in the release notes:
   `.field` to `.data.field`, `--format human`, stderr JSON, and `--yes`.
3. Update `live-verify` and any internal automation that runs `release`.

Rollback is reverting to the previous release: the registry format is
untouched, so older binaries read what newer ones write.

## Open Questions

- Whether to add `check` to MCP when `ax-go` v0.11.0 is adopted. Deferrable:
  the tool-set test forces the decision at that bump.
