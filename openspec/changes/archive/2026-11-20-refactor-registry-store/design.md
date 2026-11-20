# Design: Registry store abstraction

## Context

See `proposal.md` for motivation. The current shape:

- Each operation in `internal/registry` is one exported function taking a
  file path, for example `ReserveSiteIDWithLogger(path, id, site, metadata,
  logger)`. Inside it: normalize, `withRegistryLock(path)`, `Load` or
  `loadOrCreate`, a rule decision, then one `hujsonedit.WritePatch`.
- `Registry` is both the decoded allocation data (`SiteIDs`, `SubnetPools`,
  `ServiceAddresses`, `UDPPorts`) and HuJSON bookkeeping (raw key spellings,
  `*Present` flags, scoped `*Err` fields). `internal/planner` consumes it
  directly through `LoadForPlanning`.
- `internal/ipamsvc.Service` holds `RegistryDir`, builds
  `<dir>/<tailnet>.ipam.hujson`, and rewrites `os.ErrNotExist` into the
  "missing directory" or "no registry for tailnet" message.
- `cmd/tsipam/main.go` reads the raw file through `RawEntries` and
  `RawUDPPorts` to register sanitizer sweep values before the command runs.
- The existing specs already pin file-specific behavior: comment-preserving
  persistence, locked next-free allocation, scoped map validation, and
  best-effort release repair. `cmd/tsipam/contract_test.go` asserts verbose
  diagnostic events such as `registry lock acquired`.

## Goals / Non-Goals

**Goals:**

- Every allocation rule exists exactly once and runs against any store.
- The HuJSON backend reproduces today's bytes, error text, exit codes, and
  verbose diagnostic events exactly.
- The `Store` contract is specific enough that a SQL backend can implement
  it without reading `hujsonstore`, and a shared test suite proves that.

**Non-Goals:**

- A SQLite or Postgres backend, `export`/`import`, `tsipam serve`, or a
  `--registry` URL flag. These are follow-up changes.
- Changing `ipamsvc` result shapes or turning the multi-value returns into
  result structs.
- Honoring context cancellation while waiting for the lockfile. The
  5-second lock timeout stays as it is.
- Moving the sanitizer's identity collection behind the `Store` interface.
  It stays a HuJSON-specific reader until a non-file backend needs it.

## Decisions

### Package layout

```text
internal/registry               domain types, normalization, typed errors,
                                Store/Snapshot/Tx interfaces, Ledger (rules)
internal/registry/hujsonstore   HuJSON file backend (imports registry)
internal/registry/storetest     conformance suite + in-memory reference store
```

`registry` imports neither backend. A backend imports `registry` for domain
types and normalizers. `ipamsvc` and `loopback` depend on `registry.Ledger`
and receive a `Store`.

*Alternative considered:* keep the HuJSON code in `registry` and add
`registry/sqlstore` later. Rejected because the rules would stay
interleaved with the file mechanics, which is the problem this change
removes.

### The Store contract

```go
type MapKind int // SiteIDs, SubnetPools, ServiceAddresses, UDPPorts

type TxOptions struct {
    CreateIfMissing bool      // Update only: create an empty registry first
    Require         []MapKind // maps that must validate before fn runs
}

type Store interface {
    View(ctx context.Context, tailnet string, opts TxOptions, fn func(Snapshot) error) error
    Update(ctx context.Context, tailnet string, opts TxOptions, fn func(Tx) error) error
}

type Snapshot interface {
    SiteIDs() (map[string]Entry, error)
    SubnetPools() (map[string]Entry, error)
    ServiceAddresses() (map[string]ServiceEntry, error)
    UDPPorts() (map[string]map[string]string, error)
    Counts() Counts // diagnostics only
}

type Tx interface {
    Snapshot
    PutSiteID(id string, e Entry) error
    DeleteSiteID(id string) (bool, error)
    PutSubnetPool(cidr string, e Entry) error
    DeleteSubnetPool(cidr string) (bool, error)
    PutServiceAddress(address string, e ServiceEntry) error
    DeleteServiceAddress(address string) (bool, error)
    PutUDPPort(site, port, device string) error
    DeleteUDPPort(site, port string) (bool, error)
}
```

The contract every backend must meet:

1. **Not found.** `View` and `Update` without `CreateIfMissing` on an absent
   registry return an error matching `errors.Is(err, registry.ErrNotFound)`.
   The HuJSON store's error also still matches `os.ErrNotExist` and carries
   today's message text.
2. **Serialization.** `Update` calls for the same tailnet do not interleave.
   Their read-decide-write spans are mutually exclusive.
3. **Atomicity.** If `fn` returns an error, no mutation from that `Update`
   is persisted.
4. **Read-your-writes.** Snapshot reads inside an `Update` reflect earlier
   mutations from the same transaction.
5. **Scoped validation.** `Require` lists the maps that must validate before
   `fn` runs. A map the operation does not need never blocks it. Accessors
   for an invalid map return that map's error, never a partial view. A
   backend whose maps cannot be invalid (SQL) returns nil errors.
6. **Canonical keys in, canonical keys out.** Callers pass keys already
   normalized by the `registry` normalizers. Backends never see raw
   spellings, and never apply allocation rules. They may enforce integrity
   constraints as a backstop.
7. **Deletes fail closed.** A `Delete*` of an absent key in a valid map
   returns `(false, nil)`. When the backend cannot tell whether the key is
   present, it may attempt backend-specific recovery. If that fails, it
   returns an error rather than `(false, nil)`.
8. **Commit failures.** A failure while making mutations durable is returned
   as `*registry.CommitError`, so the ledger can add today's operation
   prefix (`reserving site ID 7: ...`).

`Require` reproduces the current `loadOrCreate` and `mapError` sequence
exactly, including where the `registry loaded` event is emitted. It also
makes scoped validation a named part of the contract instead of a file
quirk. That matters because the specs already require that "a malformed
`subnetPools` map SHALL NOT block operations on the other registry maps."

*Typed methods versus a generic `Put(kind, key, value any)`:* typed. There
are four maps with three different value types, and the compiler should
catch a site-ID entry written into the service-address map.

### Rules live in `registry.Ledger`

```go
func NewLedger(store Store, logger diagnostics.Logger) *Ledger
func (l *Ledger) ReserveSiteID(ctx context.Context, tailnet, id, site string,
    metadata ReservationMetadata) (string, string, ReservationMetadata, bool, error)
// ... one method per current ...WithLogger function, same return values
func (l *Ledger) LoadForPlanning(ctx context.Context, tailnet string) (Allocations, error)
```

Each method keeps today's sequence: normalize the inputs, open the
transaction with the right options, apply the rule, perform at most one
mutation, and emit the same "written", "already present", or "removed"
debug events. Collision checks, `validateSubnetPoolOverlap`,
`resolveReservationEntry`, `findTicketReplay`, the `ipcalc.NextFree` pick,
and list sorting move here unchanged.

Return values stay the same so that the `ipamsvc` diff is mechanical. Result
structs can be a later cleanup.

### Planner input becomes `registry.Allocations`

`Allocations` is a plain struct with `SiteIDs`, `SubnetPools`, and
`ServiceAddresses`, and no file bookkeeping. `planner.Compare` takes it
instead of `*registry.Registry`. The exported `Registry` type is removed.
Its bookkeeping becomes `hujsonstore`'s private document type.

### HuJSON transactions are buffered and validated in memory

`hujsonstore.Update` takes the lockfile, reads the file once (creating
`{}\n` when `CreateIfMissing` is set, with today's `O_EXCL` race handling),
and holds the original bytes as the pending document.

Each `Put*` or `Delete*` builds its patch with today's `reservePatchOp` or
`reserveUDPPortPatchOp` logic. It applies the patch with
`hujsonedit.PatchBytes` to the pending bytes, then re-decodes the result
through `loadBytes`. That keeps presence flags and raw spellings correct for
the next mutation, which gives read-your-writes. If `fn` succeeds and the
pending bytes changed, they are written once with `hujsonedit.WriteRaw`,
which already writes through a temporary file and a rename.

Patch errors are returned from the mutation call, formatted as
`WritePatch` formats them today (`<path>: <err>`). Write errors come back
as `*registry.CommitError`.

Malformed-map repair (`repairMalformedMap`, `repairMalformedUDPPort`)
becomes part of `Delete*`. The candidate is still judged by
`repairCandidateFixesMap`. A repair whose write fails keeps today's
`repairing ...: removing %q would fix it, but writing the repaired registry
failed` text. The store produces that message itself, and the ledger does
not wrap it again.

*Alternative considered:* write-through, with one `WritePatch` per mutation
call. It is closer to today's code, but it breaks contract rule 3 as soon as
a transaction makes two mutations, which `import` will need. Buffering costs
one extra in-memory parse per mutation.

### Error and diagnostics placement

- `ipamsvc.registryError` moves into `hujsonstore` and wraps not-found
  errors with the same directory-versus-file text, plus
  `registry.ErrNotFound`. `ipamsvc` stops calling `os.Stat`.
- Lockfile events (`acquiring registry lock`, `registry lock acquired`,
  `registry lock released`) and `registry loaded`/`registry created`, with
  their count fields, stay in `hujsonstore`. `Snapshot.Counts()` exists
  only so those counts match today's, including partly decoded maps.
- Rule events stay in `Ledger`.

### Sanitizer stays file-specific for now

`RawEntries` and `RawUDPPorts` move to `hujsonstore`, together with
`hujsonstore.FileName(tailnet)` and `hujsonstore.Path(dir, tailnet)`.
`cmd/tsipam/main.go` calls these instead of building the filename itself. A
backend-neutral source of identity values is deferred to `add-serve-tsnet`,
where the server can supply it.

### Conformance suite and in-memory reference store

`storetest.Run(t, newStore func(t *testing.T) registry.Store)` drives a
`Ledger` through table-driven cases. It covers each rule (collision,
cross-site overlap, same-site overlap, idempotent repeat, metadata merge and
`reservedAt` stamping with an injected clock, ticket replay, next-free
including exhaustion, UDP per-site scoping, list ordering) and each
contract rule (not-found, create-if-missing, atomic rollback when `fn`
fails, read-your-writes, `(false, nil)` on an absent delete, and serialized
concurrent `Update`).

`storetest.NewMemStore()` is a map-backed store guarded by one mutex. It
exists to prove that the contract does not depend on anything HuJSON-shaped
before SQLite arrives. Only tests import it.

HuJSON-only behavior stays in `hujsonstore` tests: comments, raw spellings,
BOM, near-miss and duplicate keys, presence-sensitive patches, repair, and
the lockfile.

The ledger reads time through an injectable `now func() time.Time`, which
defaults to `time.Now`. `resolveReservationEntry` already takes one.

## Risks / Trade-offs

- [Risk] Error text or the order of verbose events drifts during the move.
  → Mitigation: `cmd/` contract and error tests, `smoke/`, and every golden
  fixture must pass without edits. A task greps the old and new trees for
  each error format string to confirm each one survives exactly once.
- [Risk] A large test move hides a dropped case. → Mitigation: two phases.
  Phase one rewrites tests mechanically to go through
  `Ledger`+`hujsonstore` in place, with the same case count. Phase two moves
  rule cases into `storetest`, and every case is accounted for in the PR
  description.
- [Risk] The in-memory store shares its author's assumptions, so passing
  the suite proves less than it seems. → Accepted. The SQLite backend is the
  real independent check. The in-memory store only keeps HuJSON assumptions
  out of the interface.
- [Trade-off] `Require` and `Counts` exist mostly to keep HuJSON behavior
  exact, and SQL backends will mostly ignore them. They are cheap, and
  `Require` reflects a real requirement in the specs.
- [Trade-off] Re-decoding the pending document after each mutation costs
  one extra parse per write. Registry files are small, and each command
  makes at most one mutation.

## Migration Plan

There is no data or user migration. The on-disk format and CLI are
unchanged. It ships as one PR with commits per phase. Rollback is a revert.

## Open Questions

- Should `Ledger` methods return result structs instead of five values?
  This is deferrable, purely internal, and can happen in the SQLite change.
