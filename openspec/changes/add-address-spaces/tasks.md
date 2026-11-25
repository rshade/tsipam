# Tasks: Address spaces beyond the tailnet

## 1. Scope and prerequisites

- [ ] 1.1 Update `CONTEXT.md`, `ROADMAP.md`, the README introduction, and
      `docs/explanation/registry-and-live-state.md` per D10, including the tool
      table and the wider read-only rule; verify `mise run lint-md` passes and no
      document still calls on-prem planning out of scope
- [ ] 1.2 Add `MapVia` and `UnmapVia` to `tsacl` `addrspace` in that repository
      (rshade/tsacl#87), release it, and bump `go.mod`; verify tests there agree with
      `addrspace.SiteID` on every case in its test table, including /64, /95, /96,
      and an ID above 65535, and `mise run tidy-check license-check` pass here

## 2. Registry model and stores

- [ ] 2.1 Add space-name normalization, `PoolEntry`, `SpaceEntry`, `MapAddressSpaces`,
      and the D4 `Store` signatures, with all callers on the default space; verify
      `go test ./...` passes with no golden or fixture change
- [ ] 2.2 Add `storetest` cases for both space kinds (round trip, same CIDR in two
      spaces, scoped validation, fail-closed delete, rollback, rehearsal) and
      implement them in the in-memory store; verify the suite passes with `-race`
- [ ] 2.3 Implement `addressSpaces` in `hujsonstore`, including near-miss and
      duplicate keys, repeated site IDs, and comment preservation, patching single
      fields so that unknown fields in a space survive; verify the suite passes, and
      file tests show a malformed map leaves default-space writes working and its own
      bytes unchanged, and an unknown field survives a metadata change, a map, an
      unmap, and a pool write
- [ ] 2.4 Add `usage` to pool entries; verify bare-string entries still round-trip
      byte for byte and an unknown usage is refused on read and on write
- [ ] 2.5 Extend raw-entry discovery for the sanitizer to space names and the
      sites, CIDRs, and owners inside spaces, valid or not; verify
      `error_sanitize_test.go` cases over a malformed `addressSpaces` leak nothing
- [ ] 2.6 Update `docs/reference/registry-format.md` with the map, `usage`, and the
      warning about older writers; verify `mise run lint-md` passes

## 3. Rules

- [ ] 3.1 Add `internal/addrplan` (attribution and labels) with a role in
      `boundary_test.go`; verify table tests cover mapped, unmapped, IPv6,
      embedded `/0`, and an ID whose hex spelling is another reserved ID
- [ ] 3.2 Add `Ledger` methods to reserve, release, list, map, and unmap spaces, and
      the site-ID release refusal; verify table tests for every row of D9 and for a
      malformed `addressSpaces` not blocking `ReleaseSiteID`
- [ ] 3.3 Add the space and usage parameters to reserve, allocate, release, and list,
      with D8; verify tests for per-space overlap and ticket replay, external space
      counted as taken, decoded via routes, and refusal of a mapped space without
      `live_read`
- [ ] 3.4 Verify `Ledger.Rehearsal()` matches real outcomes for the four new mutating
      methods, with nothing written

## 4. API and commands

- [ ] 4.1 Add D9 to `design/` and run `mise run gen`; verify `mise run gen-check`
      passes and existing methods gain only optional attributes
- [ ] 4.2 Implement the methods in `ipamsvc`; verify tests over the loopback for every
      method and error, with the endpoint span tests extended to the new methods
- [ ] 4.3 Add the `space` commands: a `leafContracts` row, `bindInputs` flags for every
      positional argument, and a `pinnedTools` decision each; verify the spec
      scenarios in `cmd/tsipam` tests, including a via prefix argument
- [ ] 4.4 Add `--space` and `--usage` to the subnet commands and `--space` to
      `calc next-free` and registry aggregation; verify a test that no live read
      occurs for an unmapped space and one does for a mapped space
- [ ] 4.5 Render `space` and `mappings` in human and JSON output, sanitized per D6;
      verify golden files for `subnet list` and `space list` at every sanitize level
- [ ] 4.6 Update `schema.golden.json`, the JSON envelope and exit-code pages, the
      README command reference, and its smoke cases together; verify
      `mise run smoke` passes and the golden diff only adds entries

## 5. Comparisons

- [ ] 5.1 Implement D7 in `planner`, ordering by kind, space, then address; verify golden
      reports for two sites reusing one range, a via route outside the plan, external conflicts, an exit node, a
      dangling mapping, and an unmapped site ID unchanged from today
- [ ] 5.2 Apply the external and via rules in `policycheck`; verify a fixture policy
      with 24 sites and 3 reused ranges reports no drift against its registry, and
      one deliberately mapped to the hex-as-decimal ID reports drift
- [ ] 5.3 Render and sanitize the new row fields; verify golden output for `plan`,
      `check`, and `check-policy`, and that existing goldens are unchanged
- [ ] 5.4 Add `docs/how-to/plan-overlapping-sites.md` and update the adoption and
      explanation pages, including that a policy stores IDs in hexadecimal; verify
      every command in the guide runs as written against a scratch registry

## 6. Integration

- [ ] 6.1 Run `mise run test lint cross smoke`; verify all pass and the unchanged
      smoke cases confirm a registry without spaces behaves as before
- [ ] 6.2 Revise the plans of `add-registry-export-import`, `add-sqlite-store`,
      `add-serve-tsnet`, and `add-server-observability` for spaces with
      `openspec-update-change`, skipping any already applied and filing an issue for
      it instead; verify `openspec validate --changes` passes
