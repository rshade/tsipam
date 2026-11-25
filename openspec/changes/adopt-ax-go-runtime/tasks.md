# Tasks: Adopt the ax-go CLI runtime

## 1. Policy and baseline

- [x] 1.1 Amend `CONTEXT.md` with the D1 exception, the D9 `mcp-server` text,
      and the inbound MCP path, and update ROADMAP item #4 and its "beyond
      `tscli`" dependency rule; verify `mise run lint-md` passes and the text matches the design
- [x] 1.2 Record the stripped linux/amd64 size of `main`; verify the number is
      in the PR body

## 2. Rehearsal in the registry

- [x] 2.1 Add `TxOptions.DryRun` and contract rule 9 to `registry.Store`, and
      conformance cases to `storetest` (no persistence, no creation, no
      after-success hook, collision still refused, read-your-writes inside
      `fn`); verify they pass on the in-memory store
- [x] 2.2 Implement rule 9 in `hujsonstore` without the lock; verify the suite
      passes, and tests show an unchanged directory listing and bytes for a
      present, absent, and read-only registry and a missing directory error
- [x] 2.3 Add `Ledger.Rehearsal()`; verify table tests over all nine mutating
      methods match the real outcome, including ticket replay, exhaustion, and
      release repair, and emit no "written" or "removed" event

## 3. Service contract and spans

- [x] 3.1 Prove the unchanged design regenerates byte-stable, then add
      `dry_run` to nine payloads (body for POST, query for DELETE) and run
      `mise run gen`; verify `mise run gen-check` passes and the diff holds
      only that attribute
- [x] 3.2 Use the rehearsing ledger in `ipamsvc` when `dry_run` is set; verify
      `ipamsvc` tests and tests over the loopback for all nine methods
- [x] 3.3 Set goa `timeout`/`temporary` on comparison failures and add typed
      credential and collection errors in `internal/devices`; verify tests for
      timeout, 503, 403, and missing credentials across the loopback
- [x] 3.4 Add the OpenTelemetry API module, the endpoint span middleware and
      `traceparent` extraction in `internal/ipamhttp`, injection in
      `internal/ipamconn`, and the API read spans of D10; verify with a span
      recorder over both the loopback and `httptest` that parents are right,
      no attribute holds a URL or identity, and API requests carry no
      `traceparent`

## 4. Runtime, error boundary, and logging

- [x] 4.1 Add `github.com/rshade/ax-go v0.10.0`, imported by `cmd/tsipam`, and
      a license exception file for `github.com/segmentio/asm` (adjusting the
      `license-check` comment); verify `go mod tidy` leaves no diff,
      `go mod verify` passes, and `mise run license-check govulncheck` and
      `scripts/notice.sh` succeed
- [x] 4.2 Add an import test that allows `ax-go` only in `cmd/tsipam` and
      `axtest` only in tests; verify it fails when an `internal/` package
      imports `ax-go`
- [x] 4.3 Add `toAxError` and the typed `output_failed`, exhaustion,
      credential, and collection errors, converting `internal/devices` errors
      in `devices.go` and `target.go`; verify the boundary import test still
      passes with no new entry, and a table test of every row in D4 for code,
      exit status, `actionable_fix`, `retryable`, and sanitized message
- [x] 4.4 Rewrite `runCLI` per D3, except `mcp-server` (8.2), with the
      tree-walk boundary, required-flag validation in `PersistentPreRunE`, and
      state reset, and regenerate `schema.golden.json`; verify its diff adds
      only the four root flags and `__schema`, and a test that drives every
      command through an unknown flag, extra argument, and
      missing input and finds no `internal_error` and no exit 1, 3, or 4
- [x] 4.5 Sanitize failures raised before the command body; verify new
      `error_sanitize_test.go` cases for stray arguments, unknown commands, and
      flag values under `TAILSCALE_SANITIZE=identity`, and that an
      `AX_OTEL_DEBUG=1` dump at that level holds no site, service, CIDR, owner,
      path, or tailnet value
- [x] 4.6 Replace the slog logger with an `ax.NewLogger` adapter for
      `diagnostics.Logger`, flushed on exit; verify the existing verbose-event
      tests pass on decoded JSON lines and default runs leave stderr empty
- [x] 4.7 Hide the OTLP endpoint (D10) and restore stdout under `--strict`
      (D11); verify a listener receives nothing, and drift and full-device
      tests pass with and without `--strict`
- [x] 4.8 Resolve the version per D12; verify tests for injected, module,
      revision-only, and unstamped builds
- [x] 4.9 Update existing stderr assertions to decoded error objects and
      rewrite `docs/reference/exit-codes.md` with the code table; verify
      `mise run test lint-md` passes

## 5. Output mode and envelope

- [x] 5.1 Default `--format` to empty, read the mode from the context, and
      declare its enum; verify tests for every row of flag, `AGENT_MODE`, and
      terminal, using `axtest` and `runCLI`
- [x] 5.2 Emit the envelope with `schema_version` `"2"` and publish
      non-deterministic fields per leaf; verify regenerated goldens, reviewed
      one by one, masked with the published list, and `TRACEPARENT` echo tests
- [x] 5.3 Set `AGENT_MODE=human` in the smoke harness, add one case without
      it, and update JSON cases; verify `mise run smoke` passes
- [x] 5.4 Rewrite `docs/reference/json-envelope.md`, the AX conventions page,
      `adopt-existing-tailnet.md`, `gate-address-space-in-ci.md`, the tutorial,
      and README examples (`.data.*`, `--format human` in substitutions, error
      objects on stderr); verify the smoke cases that pin them and
      `mise run lint-md` pass

## 6. Inputs by flag

- [x] 6.1 Add `inputs.go` and convert all fourteen commands with positional
      arguments, including the `calc aggregate` source checks; verify a
      table test that positional and flag forms give equal output, bytes, and
      exit codes, that mixed, missing, and empty inputs behave as specified,
      and that `-id 1 -site hq` behaves as the double-dash spelling
- [x] 6.2 Extend the sanitization tests to flag inputs; verify no raw value
      reaches stdout or stderr
- [x] 6.3 Document the flags in README and regenerate the `-schema` golden;
      verify the smoke metadata cases pass

## 7. Dry run, confirmation, and idempotency key

- [x] 7.1 Wire `PerformWithAudit` with identity-free descriptions into the nine
      commands and the `dry run: would` messages; verify per-command tests for
      exit code, `changed`, `meta.dry_run`, unchanged directory, and audit
      lines only with `--verbose` and without identities
- [x] 7.2 Gate the four releases per D7, and add `--yes` to every smoke
      `release` call, including `smoke/live_test.go`; verify tests for JSON,
      human with and without a terminal, `y`, `n`, EOF, `--yes`, and
      `--dry-run`, and that `mise run smoke` and `mise run vet` pass
- [x] 7.3 Declare each leaf's capability class and test that every class
      other than `read-only` is in the dry-run table; verify the test fails
      when a class is removed, and that `--dry-run` on a read-only leaf leaves
      `.data` unchanged
- [x] 7.4 Test the idempotency-key scenarios, and document dry run, approval,
      and keys in README and a smoke case; verify `mise run smoke lint-md`
      passes

## 8. Discovery and MCP

- [x] 8.1 Add goldens for `__schema` (mounted in 4.4) and `--as=mcp`; verify the
      schema scenarios and that `-schema` stays unwrapped
- [x] 8.2 Add `mcp-server` per D9 with the pinned 20-tool list, and regenerate
      `schema.golden.json` and the `__schema` goldens; verify a stdio test for
      `tools/list`, parity with `--as=mcp`, `--transport` rejected, exit on
      stdin close, and the same version from `__schema`, the error object, and
      the initialize handshake
- [x] 8.3 Test tool calls: `site-id list` byte parity, `calc split`, release
      with and without `yes`, approval not carried over, two tailnets with
      different sanitization, `sanitize: off` refused by the environment, and
      policy drift with `drift_count`; verify with `-race`
- [x] 8.4 Write `docs/how-to/use-from-an-mcp-client.md` covering authority,
      approval, redaction, and why `check` is absent; verify its commands run
      and `mise run lint-md` passes

## 9. Release verification

- [x] 9.1 Add the release build tags to `.goreleaser.yaml` and a dependency
      test for that configuration; verify `mise run release-check` passes and
      the exporters and gRPC are absent
- [x] 9.2 Record stripped linux/amd64 sizes after the change, with and without
      the tags; verify the delta is in the PR body
- [x] 9.3 Run `mise run test vet lint cross smoke` and `go test -race ./...`;
      verify all pass
- [x] 9.4 File `ax-go` issues for the `--strict` stdout and write-error bugs
      and for mapping positional arguments in MCP, and note on rshade/ax-go#331
      the pre-`RunE` paths found here; verify the links are in the PR body
- [x] 9.5 Update the four in-flight changes named in the design's contact
      table; verify `openspec validate --all --strict` reports no new problem

## Workflow follow-up

- After the last PR merges, run `Live verify` from `main`; it must pass before
  the release PR merges.
- Archive this change after its final PR merges and close issue #4.
- When `ax-go` v0.11.0 is adopted, decide whether `check` becomes an MCP tool.
