# Tasks: Server observability

## 1. Shared foundations

- [ ] 1.1 Create `internal/redact`, with the shared role in
      `cmd/tsipam/boundary_test.go`, holding the sanitizer kind constants and
      the reservation-field-to-kind mapping now private to
      `cmd/tsipam/main.go`, and switch the CLI to it; verify
      `go test ./cmd/...` passes with no golden edits and no test edits other
      than the new role
- [ ] 1.2 Create `internal/usage` (pure), with the shared role in the boundary
      test, so it imports no server package: root-pool detection, utilization
      via `netipx.IPSet`, free aligned blocks per size counted by range
      arithmetic as `float64`, and the taken-set builder moved out of
      `cmd/tsipam/calc.go:nextFreeTaken`; verify table tests (nested pools,
      IPv4 and IPv6, sizes smaller than the pool, a `/48` with `/64` blocks)
      plus a cross-check against `ipcalc.NextFree` enumeration on small pools,
      and that the boundary tests pass and `calc next-free` tests pass
      unchanged
- [ ] 1.3 If `Ledger.Tailnets(ctx)` does not already exist from
      `add-serve-tsnet` or `add-sqlite-store`, add alias enumeration to the
      `Store` contract and both stores, and verify a new `storetest.Run` case
      passes against every store

## 2. Dependency gate

- [ ] 2.1 Add `github.com/prometheus/client_golang` and run
      `mise run license-check` and `mise run govulncheck`; verify both pass,
      or record the fallback (a hand-written text-format encoder) in
      design.md before continuing

## 3. Scheduled drift

- [ ] 3.1 Extract the body of `ipamsvc.Plan` into an unexported
      `compare(ctx, alias)` used by `Plan`; verify
      `go test ./internal/ipamsvc/ ./cmd/...` passes and
      `allocations.report.golden.json` is unchanged
- [ ] 3.2 Implement `internal/drift`, with the server role in the boundary
      test (`Holder` with per-alias latest complete report, live taken set,
      and run status; `Scheduler` with a startup run, a per-target ticker,
      skip-if-running, a `min(interval, 5m)` timeout, and cancellation on
      shutdown); verify the boundary tests pass and `go test -race
      ./internal/drift/` covers a failed run keeping the previous report, a
      skipped overlapping tick, and identical
      `entries`/`drift_count`/`excluded` versus a direct `Plan` call
- [ ] 3.3 Add `--drift-tailnet` (repeatable) and `--drift-interval` (default
      `15m`) to `serve`, refusing an interval below `1m` and targets without
      live credentials with exit 2 before listening. Here and in groups 4 and
      5, add each new server package that `serve.go` imports to its entry in
      `commandExceptions`. Verify the boundary tests pass, and `cmd` tests for
      each refusal and for no targets meaning no live calls
- [ ] 3.4 Add `latest_drift` (`GET /tailnet/{tailnet}/ipam/drift`), the
      `DriftRun` type, and the `drift_unavailable` (503) error to `design/`,
      run `mise run gen`, and implement it in `ipamsvc` behind `read` on the
      alias; verify service tests for configured, not configured, before the
      first run, and forbidden
- [ ] 3.5 Add the `tsipam drift` command (requires `--server`, accepts
      `--tailnet`), with the envelope's `run` object, the human `Last run:`
      line, client-side sanitization, and exit 0 or 2 as specified, and add it
      to the schema catalog. It reaches the server through `target.go` and
      needs no entry in `commandExceptions`. Declare it `read-only` in
      `leafContracts`, add it to the pinned MCP tool set, and give
      `drift_unavailable` its error code in `classify`. Verify the boundary,
      leaf-contract, and tool-set tests pass, `cmd` tests, the updated schema
      goldens, and that `-schema` shows
      the summary that points to `check` for gating
- [ ] 3.6 Add a "Scheduled comparisons" section to
      `docs/explanation/registry-and-live-state.md` covering in-memory
      retention, the consecutive-read window, and why `drift` does not gate;
      verify `mise run lint-md` passes

## 4. Metrics

- [ ] 4.1 Mount `/metrics` on the `svc:tsipam` mux, `GET`/`HEAD` only,
      requiring `read` with alias scope `*` (extract scope evaluation from
      the `add-serve-tsnet` middleware into a callable function if it is not
      one yet); verify tests for granted, ungranted, and scoped callers (403
      with no samples)
- [ ] 4.2 Implement the scrape-time collector in `internal/metrics`, with the
      server role in the boundary test, for the ten families in design.md,
      with server-side sanitization of identity labels and per-pool series
      dropped (one startup warning) when sanitization is on without a key;
      verify the boundary tests pass, and with
      `client_golang/prometheus/testutil` comparisons against a fixture
      registry for off, keyed identity, and keyless identity, including stable
      placeholders across two collector instances
- [ ] 4.3 Add `--metrics-block-size` (repeatable, default `24` and `64`) and
      free-block series only for aliases with a completed run; verify a test
      where a non-target alias exports utilization but no free blocks, and a
      `/26` pool exports nothing at size 24
- [ ] 4.4 Write `docs/how-to/monitor-the-server.md` (grant for
      `tag:prometheus`, scrape config against
      `https://tsipam.example.ts.net/metrics`, alert rules for drift
      persisting at least two intervals, few free blocks, and a stale last
      success, plus a cardinality note) and link it from `docs/README.md`;
      verify `mise run lint-md` passes

## 5. Web UI

- [ ] 5.1 Create `internal/webui`, with the server role in the boundary test,
      with embedded templates and CSS, routes `/`, `/ui/{tailnet}/...`, and
      `/static/`, the security headers from design.md, `GET`/`HEAD` only, and
      per-alias `read` checks; verify the boundary tests pass, and tests for
      the CSP containing `script-src 'none'`, frame refusal, 405 on `POST`,
      and 403 for ungranted callers
- [ ] 5.2 Implement the overview and the reservations page (filters `kind`,
      `site`, `service`, `owner`, `ticket`, `q`, list-command ordering, and
      address-in-pool search) with scoped readers seeing only covered
      aliases, sites, and services; verify handler tests for each filter, a
      search for `10.20.5.0`, and a scoped reader
- [ ] 5.3 Implement the pools page on `internal/usage`; verify a test where
      a 0.5 utilization renders a 50% bar and matches the metric's value
- [ ] 5.4 Implement the drift page (with a stale notice after a failed run)
      and the history page (newest first, keyset paging on event ID); verify
      handler tests for the stale notice and for paging across two pages
- [ ] 5.5 Apply server-side sanitization to every page through
      `internal/redact`; verify a test that sanitized pages contain no real
      CIDR, site, or owner while tickets, descriptions, and site IDs remain
- [ ] 5.6 Add a "Browse the registry" section to the monitoring how-to
      describing the pages, scoped visibility, and sanitization; verify
      `mise run lint-md` passes

## 6. Integration verification

- [ ] 6.1 Using the `add-serve-tsnet` server test harness, start a server
      with one drift target and a fake live collector, then fetch
      `tsipam drift`, `/metrics`, and `/ui/<alias>/drift`; verify all three
      show the same drift count
- [ ] 6.2 Run `mise run test lint cross` and `mise run smoke`, and verify
      all pass

## Workflow follow-up

- Archive the change after its PR merges.
- Tier-2 follow-ups (expiring reservations, webhooks, IaC integrations,
  MCP) are separate proposals.
