# Tasks: Subnet Allocate

## 1. Service contract

- [x] 1.1 Add the `allocate_subnet_pool` method to `design/design.go` beside
      `reserve_subnet_pool` (payload `tailnet`, `pool`, `size`, `site`,
      `live_taken`, metadata; result mirrors reserve; errors `invalid_input`
      400 and `exhausted` 422; `POST /tailnet/{tailnet}/ipam/subnet-pools/allocate`)
      and verify `go vet ./design/` passes
- [x] 1.2 Regenerate `gen/` with the pinned goa (`mise exec -- goa gen
      github.com/rshade/tailscale-utils/tail-ipam/design` from `tail-ipam/`)
      and verify `go build ./...` compiles with `ipam.Service` now requiring
      `AllocateSubnetPool`; inspect the diff touches only the new method,
      its types, and the `exhausted` error

## 2. Registry allocation

- [x] 2.1 Add `SubnetPoolExhaustedError{Pool, Size}` to
      `internal/registry/subnetpool.go` rendering `no free /N block in <pool>`
      and verify a unit test pins the message text
- [x] 2.2 Implement `AllocateSubnetPoolWithLogger(path, pool, size, site,
      liveTaken, metadata, logger)`: normalize site and metadata, parse and
      mask the pool, validate size as `invalidInput`, then inside
      `withRegistryLock` run `loadOrCreate`, the ticket replay scan, the
      taken-set union, `ipcalc.NextFree`, and `reservePatchOp`; verify unit
      tests cover the happy path, live-route exclusion, and registry-reservation
      exclusion across sites
- [x] 2.3 Add registry tests for the ticket replay (same site+ticket in pool
      returns the existing entry with `changed=false` and identical file bytes;
      other site or outside pool allocates fresh; lowest CIDR wins on multiple
      matches) and verify they pass
- [x] 2.4 Add a registry test running ten goroutines allocating `/24`s from one
      `/16` concurrently and verify all ten succeed with distinct CIDRs, plus a
      test that exhaustion returns `*SubnetPoolExhaustedError` and leaves the
      file unchanged
- [x] 2.5 Add a registry test in the
      `TestSiteIDReserveCommandPreservesHandWrittenComment` shape proving a
      hand-written comment on an existing `subnetPools` entry survives an
      allocation byte-for-byte, and verify it passes

## 3. Service implementation

- [x] 3.1 Implement `(*Service).AllocateSubnetPool` in
      `internal/ipamsvc/service.go`, translating `*SubnetPoolExhaustedError`
      to `ipam.MakeExhausted` and `*InvalidInputError` to
      `ipam.MakeInvalidInput`, and verify `internal/ipamsvc` tests cover the
      success result shape, the 422 exhausted error, and the 400 size error

## 4. CLI command

- [x] 4.1 Add `commandSubnetAllocate` to `output.go`, `newSubnetAllocateCommand`
      to `subnet.go` (positional `<pool-cidr> <site-name>`, required `--size`,
      metadata flags, registry flags), pre-lock validation matching
      `validateNextFreeOptions`, device collection via `listDevices`, and the
      `allocated subnet CIDR X for "site"` / `already allocated` message
      builders; thread `listDevices` through `newSubnetCommand` and `main.go`;
      verify `go build ./...` and `tail-ipam subnet allocate --help` render
- [x] 4.2 Register the pool with the sanitizer before running so the
      exhaustion error is swept, and verify a CLI test under
      `--sanitize identity` shows a CIDR placeholder in the stderr message
- [x] 4.3 Add CLI tests through the `listDevices` seam: registry + live route
      exclusion returns the third child; exhaustion exits 2 with empty stdout
      in human and JSON; `--size` outside the pool exits 2 before the collector
      runs; a nil collector is refused; two concurrent `runCLI` allocations
      against one registry dir both exit 0 with distinct CIDRs; verify with
      `go test ./cmd/tail-ipam/ -run 'SubnetAllocate'`

## 5. Schema and documentation

- [x] 5.1 Regenerate `cmd/tail-ipam/testdata/schema.golden.json` with
      `go test ./cmd/tail-ipam/ -run TestSchema -update`, inspect that the
      only addition is the `subnet allocate` entry with `size`, metadata, and
      registry flags, and verify `go test ./cmd/tail-ipam/` passes without
      `-update`
- [x] 5.2 Add `subnet allocate` to `README.md` (Contents, a `#### subnet
      allocate` section under `subnet`, the exit-code notes, and remove the
      `allocate` line from "Not yet implemented"), to
      `docs/reference/json-envelope.md`'s command table, and to
      `docs/reference/exit-codes.md`'s exit-2 exhaustion bullet; verify
      `make lint-md` passes from the repository root
- [x] 5.3 Add `subnet allocate` to the schema catalog assertion in
      `smoke/calc_test.go` and add a live smoke case in `smoke/live_test.go`
      (allocate, confirm the CIDR is inside the pool and listed, replay with
      the same ticket returns it with `changed=false`, exhaust a `/24` pool
      with `--size 24` and expect exit 2 with empty stdout); verify the smoke
      package compiles with `go vet ./smoke/`

## 6. Verification

- [x] 6.1 Run `go build ./... && go test ./... && golangci-lint run ./...`
      from `tail-ipam/` and verify all pass with no new lint findings
- [x] 6.2 Build the binary and reproduce the issue's evidence in reverse: two
      backgrounded `subnet allocate 10.0.0.0/16 --size 24 <site>` runs against
      one temp registry (with a stub collector via the test seam or a fixture
      tailnet) both exit 0 with distinct blocks; verify `openspec validate
      add-subnet-allocate` still passes
