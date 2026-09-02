# Tasks: add-service-address-reservations

## 1. Registry data model and normalization

- [x] 1.1 Write registry tests first for `NormalizeServiceAddress` (bare IPv4,
      exact `/32`, wider-prefix rejection without masking, native IPv6,
      IPv4-mapped IPv6, zones, malformed/blank input) and
      `NormalizeServiceName` (bare/prefixed equivalence, prefix-only, doubled
      prefix, whitespace, and control characters); verify the focused
      `go test ./internal/registry -run 'TestNormalizeService'` run fails before
      the new functions exist.
- [x] 1.2 Add `ServiceEntry` string/object JSON tests, including the required
      `service` field, optional metadata spellings, concise metadata-free
      encoding, and rejection of unsupported value shapes; then implement the
      two normalizers, typed validation error, `ServiceEntry`, and a shared
      metadata-resolution helper used by both existing `Entry` and the new
      type. Verify all `internal/registry` entry/normalization tests pass and
      existing site/subnet encodings are byte-for-byte unchanged.
- [x] 1.3 Write `Registry.Load` tests first for absent/empty/valid
      `serviceAddresses`, bare and exact-`/32` raw keys, canonical alias
      duplicates, exact duplicate keys, near-miss/duplicate top-level keys,
      invalid address keys, empty/malformed service values, object metadata,
      and bidirectional validation isolation from `siteIDs`/`subnetPools`;
      verify the focused tests fail before `Registry` loads the third map.
- [x] 1.4 Extend `Registry`, `Load`, diagnostics counts, raw-key tracking,
      `mapAccessorFor`, the type-agnostic release membership path, and the raw
      identity inventory for `serviceAddresses` (including object-form service
      values). Verify `go test ./internal/registry` passes, an exact `/32` raw
      key resolves to the bare logical key, and site-ID/subnet tests remain
      green.

## 2. Registry service-address operations

- [x] 2.1 Write `internal/registry/serviceaddress_test.go` first for new
      reserve, collision, idempotent repeat, multiple addresses per service,
      metadata merge/auto-stamp, arbitrary valid address with no parent pool,
      absent release, missing-file release/list behavior, numeric listing,
      `/32` raw-key update/release, and scoped-map failures; verify the focused
      test file fails before the operations exist.
- [x] 2.2 Implement `ServiceAddressEntry`,
      `ServiceAddressCollisionError`, `ReserveServiceAddressWithLogger`,
      `ReleaseServiceAddressWithLogger`, and `ListServiceAddressesWithLogger`
      using the existing file lock and HuJSON patch mechanics. Verify
      `go test ./internal/registry -run 'Test.*ServiceAddress'` and then
      `go test ./internal/registry` pass.
- [x] 2.3 Add registry-level preservation and concurrency regressions: a
      comment on one service entry survives reserve/update/release of another,
      an existing exact-`/32` key is patched through its raw spelling, and
      concurrent distinct reservations all land under one lock. Verify the
      tests pass under `go test -race ./internal/registry`.

## 3. Goa design, generated contract, and service adapter

- [x] 3.1 Extend `design/design.go` with `ServiceAddressReservation`, the
      three service-address methods, shared reservation metadata, `collision`
      and `invalid_input` errors, and the query-parameter DELETE route; run
      `goa gen github.com/rshade/tailscale-utils/tail-ipam/design`. Verify the
      generator exits 0, the generated service/client/server expose all three
      methods and paths, and `git diff --check -- gen design/design.go` passes.
- [x] 3.2 Write `internal/ipamsvc` tests first for list/reserve/release result
      translation, canonical outputs, metadata, collision → named 409 error,
      validation → named 400 error, and non-validation registry failures;
      verify the focused tests fail while the generated interface is not yet
      fully implemented.
- [x] 3.3 Implement the three `internal/ipamsvc.Service` methods and the
      validation/collision translations without bypassing the registry layer.
      Verify `go test ./internal/ipamsvc ./internal/loopback` passes and the
      compile-time `ipam.Service` assertion succeeds.

## 4. CLI, output contract, and sanitization

- [x] 4.1 Write output-contract tests first for the new `Address`, `Service`,
      and `ServiceAddresses` fields: JSON tags, empty-list distinction,
      tab-separated human ordering, metadata column, identity sanitization,
      and `sanitize.UnhandledFields` coverage. Verify the focused
      `cmd/tail-ipam` tests fail before the fields/renderers are added.
- [x] 4.2 Extend `outputEnvelope`, add `serviceAddressOutput`, register command
      constants, render list entries, and rebuild/sanitize address (`ip`),
      service (`service`), owner, and tailnet identities. Keep reserve/release
      messages envelope-backed after sanitization. Verify the output and
      reflection-contract tests pass without changing the output schema
      version.
- [x] 4.3 Write `cmd/tail-ipam/service_test.go` first for reserve/repeat,
      release/absent release, numeric list in human and JSON formats, metadata,
      bare-versus-`/32` equivalence, invalid/wider/IPv6 inputs, usage arity,
      missing registries, collision exit 1, and parse-clean stdout; include the
      acceptance-level test that a comment on one registry entry survives a
      command-layer edit to another. Verify the tests fail before the Cobra
      group is wired.
- [x] 4.4 Implement `cmd/tail-ipam/service.go`, add the group to the root
      command, and route every leaf through the generated client and
      `loopback.RunWithLogger` with the existing registry/tailnet and metadata
      flags. Verify `go test ./cmd/tail-ipam -run 'TestService'` passes and no
      command imports `internal/registry` for direct execution.
- [x] 4.5 Add the service-address collision type to `exitCodeFor`'s in-process
      insurance, implement an anchored capture/segment sanitizer for address
      plus both service identities, and extend known identity registration.
      Add format-drift and adversarial-name tests through the real CLI; verify
      `go test ./cmd/tail-ipam -run 'Test.*Service.*Sanit|Test.*Collision'`
      passes and sanitized stderr contains no raw address or service name.
- [x] 4.6 Assert `service reserve`, `service release`, and `service list` in
      the schema tests, regenerate `cmd/tail-ipam/testdata/schema.golden.json`
      with `go test ./cmd/tail-ipam -update`, read the golden diff, and verify
      a normal `go test ./cmd/tail-ipam` run passes with no unreviewed fixture
      changes.

## 5. Documentation and end-to-end verification

- [x] 5.1 Update `tail-ipam/README.md` with the `serviceAddresses` HuJSON
      shape, command examples in both formats, exact `/32` validation,
      canonical `svc:` behavior, collision/release/list semantics, metadata,
      sanitization, and the explicit no-parent-pool/no-exhaustion boundary.
      Do not restate a hand-maintained command count. Verify `make lint-md`
      passes from the repository root.
- [x] 5.2 Run formatting and module verification: `gofmt` changed non-generated
      Go files, `go test ./... -race` and `go mod tidy` from `tail-ipam`, and
      confirm `go.mod`/`go.sum` have no unexplained dependency changes.
- [x] 5.3 Run the repository gates from the root with
      `make test build cross lint lint-md`; verify every discovered module is
      green and the built `bin/tail-ipam` is from the current working tree.
- [x] 5.4 Dogfood `bin/tail-ipam service` against a temporary registry in
      human and JSON formats: reserve bare and `/32`, metadata update, sorted
      list, absent release, collision with sanitization, and wider-prefix/IPv6
      rejection. Verify `jq` consumes every JSON success, failures leave
      stdout empty, comments survive an unrelated edit, and no live Tailscale
      state is contacted or changed.
- [x] 5.5 Run `openspec validate add-service-address-reservations --strict`,
      `git diff --check`, fetch `origin`, and review the diff/stat against
      `origin/main` for already-merged or direction-reversed work before
      describing completion. Verify all planning requirements map to passing
      tests or documented evidence and leave the change ready for archive in a
      separate workflow.
