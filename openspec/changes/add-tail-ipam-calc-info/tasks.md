# Tasks: add-tail-ipam-calc-info

## 1. policycore/ipcalc: Info

- [x] 1.1 Write `policycore/ipcalc/info_test.go` first (TDD): table-driven
      cases for `/8`, `/16`, `/24`, `/30`, `/31`, `/32` asserting network,
      broadcast (absent for `/31`/`/32` via `IsValid()`), first/last
      usable, and `UsableHosts` per design Decisions 2–3; plus host-bits
      masking (`192.168.1.37/24`), native-IPv6 → `ErrIPv6NotSupported`,
      and invalid-prefix error. Verify: `cd policycore && go test
      ./ipcalc/` fails (Info not yet written).
- [x] 1.2 Implement `BlockInfo`, `ErrIPv6NotSupported`, and
      `Info(netip.Prefix) (BlockInfo, error)` in
      `policycore/ipcalc/info.go`, reusing the package's existing
      big-int/addr helpers where they fit. Verify: `cd policycore && go
      test ./...` passes.

## 2. tail-ipam CLI: envelope and subcommand

- [x] 2.1 Add `blockInfoOutput` and the `BlockInfo *blockInfoOutput`
      envelope field in `cmd/tail-ipam/output.go` (JSON tags per design
      Decision 5, `usable_hosts` as decimal string, `broadcast,omitempty`),
      the unredacted copy-through in `sanitizeOutput` with the
      `Subnets`-style rationale comment, the `renderOutput` human branch
      (tab-separated `fact\tvalue` lines, broadcast line omitted when
      absent), and the `commandCalcInfo` constant. Update
      `contract_test.go`'s `UnhandledFields` handled lists (envelope +
      new struct guard). Verify: `cd tail-ipam && go test ./...` passes,
      including the contract test.
- [x] 2.2 Write CLI tests in `cmd/tail-ipam/calc_test.go` first (TDD),
      following the `runCalcSplit`/`runCalcSplitCLI` helper pattern: human
      and JSON output for a classic block, `/31` and `/32` RFC 3021
      output, 4-in-6 normalization, native-IPv6 rejection (usage exit
      code, stderr names IPv6 + issue #251, stdout empty), malformed-CIDR
      rejection, and `-schema` listing `calc info`. Verify: tests fail
      (subcommand not yet wired).
- [x] 2.3 Implement `newCalcInfoCommand` in `cmd/tail-ipam/calc.go`
      (ExactArgs(1), no flags, `Unmap4In6` normalization before `Info`,
      `errors.Is(err, ipcalc.ErrIPv6NotSupported)` translated to the
      #251-pointing message) and register it in `newCalcCommand`. Verify:
      `cd tail-ipam && go test ./...` passes.

## 3. Schema and docs

- [x] 3.1 Generalize the hand-written `Sanitization` prose in
      `cmd/tail-ipam/schema.go` from "calc split output …" to cover all
      `calc` output (design Decision 7). Verify: `bin/tail-ipam -schema`
      shows the new wording and lists `calc info`.
- [x] 3.2 Add a `#### calc info` section to `tail-ipam/README.md` beside
      `#### calc split`: examples for a classic block and `/31` (RFC 3021
      wording), the omitted-broadcast and string-count JSON encodings, the
      IPv6 → #251 rejection, and 4-in-6 normalization. No hardcoded
      catalog counts. Verify: `make lint-md` passes.

## 4. Verification

- [x] 4.1 Run `make test build lint` from the repo root (extended timeout
      for lint) and confirm all green.
- [x] 4.2 Dogfood the built binary: run `bin/tail-ipam calc info` against
      `10.0.0.0/24`, `10.0.0.0/31`, `10.0.0.5/32`, `2001:db8::/64`, and
      `::ffff:10.0.0.0/120` in both formats; confirm outputs match the
      spec scenarios and stdout stays parse-clean (`jq` consumes the JSON).
- [x] 4.3 Diff against `origin/main` (fetch first) to confirm nothing in
      the branch duplicates already-merged calc work, per the AGENTS.md
      diffstat-direction rule.
