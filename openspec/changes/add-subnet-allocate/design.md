# Design: Subnet Allocate

## Context

See `proposal.md` for motivation. The pieces this change composes already
exist:

- `internal/registry.withRegistryLock` gives `ReserveSubnetPoolWithLogger`
  a cross-process read-decide-write span over one registry file. Ten
  parallel `reserve` calls are already proven to serialize (ROADMAP.md,
  `tail-ipam` section).
- `cmd/tail-ipam/calc.go` collects live device routes through the
  `devicesListFunc` seam, unions them with every subnet-pool reservation
  (`nextFreeTaken`), and walks `policycore/ipcalc.NextFree`. It refuses to
  run without a collector (`validateNextFreeOptions`).
- Every registry command traverses the generated goa HTTP client and the
  `internal/loopback` server; `internal/ipamsvc` is the one place registry
  errors become goa errors, and `exitCodeFor` maps a `*goa.ServiceError`
  named `collision` to exit 1 and every other error to exit 2.
- `resolveReservationEntry` and `reservePatchOp` write a new entry while
  preserving HuJSON comments and untouched entries.

`design/design.go` is the goa DSL source; `gen/` is committed and CI never
regenerates it. `mise.toml` pins goa 3.30.0.

## Goals / Non-Goals

**Goals:**

- One command, one lock span: the pick and the write are indivisible
  relative to every other `tail-ipam` invocation on the same registry.
- Zero drift from `calc next-free` in what counts as taken, how the pool
  is normalized, and how exhaustion is reported.
- Zero drift from `subnet reserve` in metadata, output shape, sanitization,
  and comment preservation.

**Non-Goals:**

- Allocating more than one block per invocation (`--count`, `--all`).
- Any change to `calc next-free`, `subnet reserve`, or `policycore/ipcalc`.
- Adopting live state into the registry; a separate `decide(...)` issue.
- Site-ID, service-address, or UDP-port allocation.

## Decisions

### The registry picks; the CLI collects

`AllocateSubnetPoolWithLogger(path, pool, size, site, liveTaken, metadata,
logger)` takes the live prefixes as an input and does the pick inside
`withRegistryLock`, after `loadOrCreate`. The CLI collects devices before
calling the service, exactly as `calc next-free` does, and sends the
advertised routes in the payload.

*Why not collect inside the lock?* The lock guards registry state; live
routes are not registry state, and holding a lockfile across a Tailscale
API round-trip would stretch every concurrent writer's wait by a network
call. The window this leaves — a route advertised between the device read
and the write — is the same window `plan`/`check` exist to detect, and no
CLI-side ordering can close it.

*Why not have the CLI lock and call `next-free` then `reserve`?* The lock
is a registry-package primitive, deliberately unexported; exposing it to the
CLI would let every future command hold it for arbitrary work.

### Taken set is every reservation plus every live route

`nextFreeTaken`'s rule is reused unchanged: all subnet-pool reservations
for the selected tailnet, regardless of site, plus every advertised route.
`ipcalc.NextFree` already ignores other-family and outside-pool prefixes,
so IPv6 comes for free (#257) with no family-specific code. `subnet
reserve`'s same-site-overlap allowance is irrelevant here: `allocate`
never proposes a block that overlaps anything, so it can never need the
allowance and never trips the collision check.

### `--ticket` is the idempotency key, and the replay is read-only

Under the lock, before picking, the registry scans the pool's existing
reservations for `site == site && ticket == ticket` (ticket non-empty). A
match is returned with `changed=false` and no write. This is a
deliberately narrower contract than `reserve`'s repeat, which merges
metadata: a replayed `allocate` must not rewrite what the first run
recorded, or a CI retry with slightly different `--description` would
silently churn the file. The lowest matching CIDR wins when several match,
using the same order `subnet list` uses, so the answer is stable.

The requested `--size` is not part of the key. The ticket names the
allocation request; a caller who changes the size but keeps the ticket is
asking "what did this ticket get", and the answer is visible in the output.

*Alternative rejected:* keying on `site` alone. Two legitimate allocations
for one site (two subnets in one VPC) are the common case, not the
exception.

### Exhaustion is a goa error, not a result status

`SubnetPoolExhaustedError{Pool, Size}` renders `no free /N block in <pool>`,
the same text `calc next-free` uses. `ipamsvc` translates it to a new goa
error `exhausted` carried as HTTP 422; the loopback client decodes it into
a `*goa.ServiceError` named `exhausted`, which `exitCodeFor` already maps
to exit 2 because it is not `collision`. Nothing reaches stdout because
the error returns before `emitOutput`.

*Why 422 and not 409?* 409 is `collision` — the request names something
already owned by someone else. Exhaustion is a well-formed request the
current state cannot satisfy, which is what 422 describes. The CLI keys
on the error name, not the status, so the choice only has to be
consistent within `design.go`.

*Why not a `status: exhausted` result like `next-free` once had?* #347
settled that: exit 0 with an empty stdout broke shell composition.

### Pool normalization follows `next-free`, not `reserve`

The pool argument is a search space, not a stored key. It is parsed,
4-in-6 unmapped, and masked, as `nextFreePool` does; it is not held to
`NormalizeSubnetCIDR`'s canonical-spelling rule, because an operator moving
from `next-free <pool>` to `allocate <pool>` must not hit a new rejection.
The allocated child is always canonical by construction.

### Size validation happens twice, on purpose

The CLI validates `--size` against the pool and the collector before any
network or registry I/O (cheap fail, matching `validateNextFreeOptions`).
The registry function validates again and returns `invalidInput`, because
the service method is a contract of its own and the loopback path is not
the only conceivable caller.

### The goa method mirrors `reserve_subnet_pool`

`allocate_subnet_pool`: payload `tailnet`, `pool`, `size` (Int), `site`,
`live_taken` (ArrayOf String), metadata; result `cidr`, `site`, `changed`,
metadata — the same result shape as reserve so the CLI's output path is
shared. Errors `invalid_input` (400) and `exhausted` (422). Route
`POST /tailnet/{tailnet}/ipam/subnet-pools/allocate`, following the
`check-policy` precedent for a verb endpoint.

### Output reuses reserve's envelope and message shape

Command name `subnet allocate`; envelope fields `changed`, `cidr`, `site`,
and metadata; messages `allocated subnet CIDR X for "site"` and
`subnet CIDR X already allocated for "site"`, rendered by a
`messageBuilder` so they read the sanitized values. Human output is a
message line like `reserve`, not a bare CIDR like `next-free`: `allocate`
is a reserve variant, and machine callers have `--format json`.

## Risks / Trade-offs

- [The device read happens outside the lock] → documented above; `check`
  detects the residual drift, and the alternative lengthens every writer's
  lock wait by a network call.
- [`gen/` must be regenerated with the pinned goa] → `mise exec -- goa gen`
  from `tail-ipam/`; the diff is reviewed like any other and CI compiles it.
- [`newSubnetCommand` gains a `listDevices` parameter] → every test caller
  of the constructor is updated in the same change; `runCLI` already
  defaults the collector, so the production wiring is one line.
- [A replay with a different `--size` returns the original block] →
  accepted and specified; the output names the CIDR.
- [Lock timeout under heavy contention] → unchanged from `reserve`;
  `withRegistryLock`'s existing deadline applies and surfaces as exit 2.

## Migration Plan

Additive: a new command, a new service method, a new error. No existing
output, exit code, registry format, or route changes. Roll back by
reverting the PR; registries written by `allocate` are ordinary
`subnetPools` entries that every other command already reads.
