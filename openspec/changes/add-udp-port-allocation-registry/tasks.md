# Tasks: Per-Device UDP Port Allocation Registry

## 1. Registry model and validation

- [x] 1.1 Add table-driven parallel tests for `NormalizePort` (canonical
      leading-zero spellings, 1024/65535 boundaries, and empty, signed,
      fractional, non-ASCII, non-numeric, and out-of-range rejection) and
      `NormalizeDeviceName` (trim, empty, and control-character behavior);
      verify the focused registry tests fail before the functions exist.
- [x] 1.2 Add HuJSON loader tests for absent/null/empty/populated `udpPorts`,
      raw and canonical duplicate sites and ports, malformed child shapes,
      invalid device leaves, and error isolation in both directions between
      `udpPorts` and the existing maps; verify the new tests fail against the
      current three-map `Registry`.
- [x] 1.3 Extend `Registry`, `loadBytes`, scoped map selection, package
      documentation, and load/create diagnostics with the canonical nested UDP
      view, both levels of raw spellings/presence, and the scoped UDP error;
      implement the two normalizers and verify all loader/normalization tests
      pass with `go test ./internal/registry -run
      'Test(Normalize|Load.*UDP|UDP.*Load)'`.

## 2. Registry reserve, release, list, and identity inventory

- [x] 2.1 Write parallel registry operation tests for missing-file creation,
      canonical return values, exact-repeat byte-level idempotency, same-site
      collision without overwrite, cross-site port reuse, no-op release,
      missing-file release/list errors, site-filtered list, and site-then-numeric
      sorting; verify they fail before UDP operations are implemented.
- [x] 2.2 Add focused HuJSON regression tests covering the full two-level
      patch matrix (top-level and site absent/null/empty/populated), exact raw
      key targeting for normalized aliases, comments on sites/ports/siblings,
      preservation of unknown and existing top-level maps, and retention of an
      empty site node after final release; verify the tests demonstrate failure
      if either level is replaced wholesale.
- [x] 2.3 Implement `UDPPortEntry`, `UDPPortCollisionError`, the dedicated
      nested reserve-patch selector, and locked
      `ReserveUDPPortWithLogger`/`ReleaseUDPPortWithLogger`/`ListUDPPortsWithLogger`
      operations in `internal/registry/udpport.go`; verify all registry tests
      pass with `go test ./internal/registry`.
- [x] 2.4 Add same-site concurrent reservation coverage proving the lock loses
      no distinct ports and permits only one winner for competing devices, and
      implement/test best-effort `RawUDPPorts` extraction for raw site and
      device sanitizer candidates in malformed files; verify with repeated
      `go test ./internal/registry -run 'Test.*UDP' -count=10`.

## 3. Goa contract and service layer

- [x] 3.1 Add `UDPPortReservation` and the list/reserve/release methods to
      `design/design.go` using `/tailnet/{tailnet}/ipam/udp-ports`, optional or
      required site query/body fields per the design, string ports, bad-request
      input errors, and conflict collisions; regenerate all `gen/**` output
      with pinned Goa 3.30.0 and verify generated clients, servers, service
      interfaces, and OpenAPI documents contain all three operations without
      hand edits.
- [x] 3.2 Add parallel `internal/ipamsvc` tests for canonical reserve/release
      results, changed flags, optional list filtering, same-site conflict
      translation, cross-site success, and invalid-input translation; verify
      the focused tests fail until `Service` implements the generated methods.
- [x] 3.3 Implement the three generated service methods and UDP error
      translation against the registry operations, update the service
      interface assertion, and verify `go test ./internal/ipamsvc ./gen/...`
      passes.

## 4. CLI, output contract, and sanitization

- [x] 4.1 Extend `outputEnvelope` with canonical `port`/`device` fields and a
      non-nil `udp_ports` list payload, add three-column human rendering and
      post-sanitization reserve/release messages, and update the output
      contract/reflection tests; verify focused human and JSON render tests
      cover populated and empty lists and pass.
- [x] 4.2 Write parallel CLI tests for exact positional arity, required
      reserve/release `--site`, optional list `--site`, range/label validation,
      reserve idempotency, collision exit 1 with empty stdout, cross-site reuse,
      release behavior, sorted human output, JSON envelopes, missing-file
      errors, loopback service traversal, and `-schema` discovery; verify the
      tests fail before the command group is registered.
- [x] 4.3 Implement `cmd/tail-ipam/udpport.go` with a shared leaf runner,
      generated-client calls, registry/tailnet flags, and sanitizer registration
      for site/device inputs; register the `port` group and refresh root examples
      and the schema golden, then verify `go test ./cmd/tail-ipam` passes.
- [x] 4.4 Add sanitizer regressions for reserve/list output, invalid raw input,
      malformed stored site/device values, and collision strings containing
      quotes, backslashes, and template-word lookalikes; verify the tests fail
      until UDP identities are included in every output/error path.
- [x] 4.5 Extend the raw registry identity inventory, output identity walk,
      `sanitizeOutput`, anchored collision splice pattern, sanitizer field
      guards, and in-process collision exit-code mapping while leaving port
      numbers visible; verify `go test ./cmd/tail-ipam -run
      'Test.*(Sanit|Collision|OutputContract)'` passes and no failure writes a
      success payload to stdout.

## 5. Documentation

- [x] 5.1 Add README command examples and behavior for `port reserve`,
      `release`, and filtered/unfiltered `list`, including 1024-65535
      validation, per-site uniqueness/cross-site reuse, explicit-only
      allocation, JSON fields, and the nested `udpPorts` HuJSON shape; verify
      examples and field names match `tail-ipam -schema` and generated output.
- [x] 5.2 Update README registry/error-isolation and sanitization descriptions,
      and add UDP listen-port tracking as the fourth `tail-ipam` allocation
      driver in `../ROADMAP.md` with issue #240; verify `make lint-md` passes
      from the monorepo root.

## 6. End-to-end verification

- [x] 6.1 Format changed Go with `gofmt`/`goimports`, inspect every generated
      and golden-file diff, and run `make test lint build cross` from the
      monorepo root; verify all modules, lint checks, the CLI build, and all
      target compilations pass.
- [x] 6.2 Dogfood the built CLI against fresh and commented existing registry
      files: reserve the same port in two sites, provoke a same-site collision,
      repeat an idempotent reserve, filter/sort both output formats, release an
      existing and absent port, and parse every JSON result with `jq`; verify
      comments/siblings survive and stdout/stderr/exit codes match the spec.
- [x] 6.3 Run `openspec validate add-udp-port-allocation-registry --strict` and
      review the final diff against the intended issue #240 scope; verify the
      change remains explicit-allocation only, metadata-free, and independent
      of live Tailscale and site-ID state.
