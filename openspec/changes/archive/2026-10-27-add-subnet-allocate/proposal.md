# Proposal: Subnet Allocate

## Why

Allocating address space is a two-command sequence today: `calc next-free`
picks a block outside the registry lock, and `subnet reserve` locks without
picking. Two operators or CI jobs that run `next-free` before either writes
receive the same suggestion, and the second `reserve` is refused as a
collision. A single `subnet allocate` verb that picks the next free child
CIDR *inside* the locked read-decide-write transaction closes that
time-of-check/time-of-use window, and delivers the `allocate` command the
README has listed as "Not yet implemented" since the module landed (#368).

## What Changes

- New CLI command `tail-ipam subnet allocate <pool-cidr> --size N <site-name>`
  that reserves the first free child CIDR of `<pool-cidr>` at prefix length
  `N` for `<site-name>`. It accepts the same metadata flags as
  `subnet reserve` (`--owner`, `--description`, `--ticket`,
  `--reserved-at`) and the same `--registry-dir` / `--tailnet` routing.
- The pick happens under the registry lock. Registry subnet-pool
  reservations for every site and every route advertised by a live device
  are both counted as taken, exactly as `calc next-free` counts them today;
  the live routes are collected before the lock and passed in, since they
  are not registry state.
- A live device read is required, as it is for `calc next-free`; there is
  no `--offline` mode.
- `--ticket` is the idempotency key: when a reservation already exists in
  the pool for the same site and ticket, the command reports it with
  `changed=false`, exits 0, and writes nothing. Without `--ticket`, every
  run allocates a fresh block.
- Exhaustion follows the `calc next-free` precedent settled in #347: exit 2,
  message on stderr, nothing on stdout in either output format.
- New goa service method `allocate_subnet_pool` beside `reserve_subnet_pool`,
  with `gen/` regenerated, so the CLI keeps traversing the generated HTTP
  client and loopback server like every other registry command.
- `-schema` lists the new command; the README documents it and drops the
  `allocate` entry from "Not yet implemented"; the smoke suite gains an
  `allocate` case beside the existing `next-free` exhaustion pin.

No existing command, flag, output shape, or exit code changes.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `subnet`: adds the atomic next-free allocation requirement (pick under the
  lock, live routes counted as taken, ticket-keyed idempotent replay,
  exhaustion exit code, metadata and comment preservation).
- `schema`: the command catalog gains `subnet allocate` with its `size`,
  metadata, and registry-selector flags.

## Impact

- `cmd/tail-ipam/subnet.go`, `output.go`, `main.go`: new `subnet allocate`
  command wired with the device collector; `newSubnetCommand` gains the
  `listDevices` dependency.
- `internal/registry/subnetpool.go`: new
  `AllocateSubnetPoolWithLogger(path, pool, size, site, liveTaken, metadata,
  logger)` plus a `SubnetPoolExhaustedError`, reusing `withRegistryLock`,
  `loadOrCreate`, `resolveReservationEntry`, and `reservePatchOp`.
- `internal/ipamsvc/service.go`: `AllocateSubnetPool` implementing the new
  goa method and translating `exhausted` / `invalid_input` errors.
- `design/design.go` and the committed `gen/` tree: new
  `allocate_subnet_pool` method and its `exhausted` error.
- `cmd/tail-ipam/testdata/schema.golden.json`, `README.md`,
  `smoke/live_test.go`, `smoke/calc_test.go`: catalog, docs, and live pins.
- `policycore/ipcalc.NextFree` is reused unchanged; no policycore change.
- release-please owns `CHANGELOG.md`; the conventional commit subject is the
  changelog entry.
