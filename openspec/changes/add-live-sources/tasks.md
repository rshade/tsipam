# Tasks: Live sources beyond the tailnet

## 1. Declarations

- [ ] 1.1 Add source declarations to `registry.SpaceEntry`, the `Store` contract, and
      the in-memory store, with `storetest` cases (round trip, rollback, rehearsal,
      release of a space in use); verify the suite passes with `-race`
- [ ] 1.2 Add the settings table and implement the `sources` map in `hujsonstore` with
      that validation and comment preservation, and register names and setting values with the
      sanitizer; verify file tests and `error_sanitize_test.go` cases pass
- [ ] 1.3 Add `Ledger` methods to add, remove, and list sources, and extend the
      space-release refusal; verify table tests for idempotency, collision, and
      rehearsal

## 2. Source seam

- [ ] 2.1 Add `internal/sources` (interface, type table, `CollectionError`, bounded
      concurrent collection, traced reads) with the server role in
      `boundary_test.go`; verify tests with a fake source for two failures in
      either order, timeout, cancellation by the caller, and an empty result, and that
      the settings table and the implementations name the same types
- [ ] 2.2 Add D7 to `design/` and run `mise run gen`; verify `mise run gen-check`
      passes, and existing methods gain only optional attributes and the two D7 errors
- [ ] 2.3 Implement the methods in `ipamsvc` with `CollectSources` injected from
      `target.go`; verify loopback tests for each method, and that `add_source` with a
      bad setting contacts nothing and echoes no value

## 3. Comparison

- [ ] 3.1 Compare per origin in `planner` per D4; verify golden reports for two
      regions, an unplanned network, an external conflict, and a space that is both
      mapped and sourced
- [ ] 3.2 Add `only` selection and `excluded` disclosure; verify no request reaches an
      unselected origin, and `--only prod` runs without Tailscale variables
- [ ] 3.3 Render and sanitize `origin` and source evidence; verify golden output at
      every sanitize level, and existing goldens unchanged

## 4. Allocation

- [ ] 4.1 Read sources in `allocate_subnet_pool` per D5; verify tests that the lock is
      not held during collection, that a changed declaration is refused, and that a
      failed source writes nothing
- [ ] 4.2 Use `list_space_observations` in `calc next-free`; verify it names the block
      that `subnet allocate` then reserves, for v4 and v6

## 5. AWS VPC source

- [ ] 5.1 Add `aws-sdk-go-v2` and a Renovate group; verify `mise run cross
      license-check govulncheck tidy-check` pass, `scripts/notice.sh` succeeds, and
      the release binary size before and after is in the PR body
- [ ] 5.2 Add `internal/sources/awsvpc` with the two-method client interface; verify
      table tests for every association state, IPv4 and IPv6, `resource=subnet`, the
      `vpc` filter, pagination, and a failing later page
- [ ] 5.3 Map failures to `CollectionError`; verify tests over real SDK errors from an
      `httptest` endpoint (401, 403, 503, throttling, timeout, no credentials) that
      the reason is right and no ARN, account ID, or body text appears
- [ ] 5.4 Add a read-only live case for the source, skipped unless
      `TSIPAM_LIVE_AWS=1`; verify it is compiled in CI and never run there

## 6. Commands and documentation

- [ ] 6.1 Add the `source` commands and `--only`: a `leafContracts` row, `bindInputs`
      flags, and a `pinnedTools` decision each; verify the spec scenarios in `cmd/tsipam` tests and an
      updated `schema.golden.json` whose diff only adds entries
- [ ] 6.2 Add `docs/how-to/check-aws-vpcs.md` with the two-permission IAM policy, and
      update the registry-format, JSON envelope, exit-code, and redaction pages;
      verify `mise run lint-md` passes and the guide's offline commands run as written
- [ ] 6.3 Update `CONTEXT.md` (sources in the data table and outbound calls) and
      `ROADMAP.md` (remaining sources in order, plugins and writes parked); verify
      `mise run lint-md` passes
- [ ] 6.4 Add README examples and smoke cases for `source list` and `--only`; verify
      `mise run smoke` passes

## 7. Integration

- [ ] 7.1 Run `mise run test lint cross smoke`; verify all pass with unchanged smoke
      cases for registries without sources
- [ ] 7.2 Revise the `add-serve-tsnet` plan for source collection and the `admin` gate
      with `openspec-update-change`, or file an issue if it is already applied; verify
      `openspec validate --changes` passes
