package registry

import "context"

// MapKind names one registry map. Backends use it for TxOptions.Require.
// The zero value is not a map.
type MapKind int

const (
	// MapSiteIDs is the 4via6 site-ID map.
	MapSiteIDs MapKind = iota + 1
	// MapSubnetPools is the subnet-CIDR pool map.
	MapSubnetPools
	// MapServiceAddresses is the per-service IPv4 address map.
	MapServiceAddresses
	// MapUDPPorts is the per-site UDP listen-port map.
	MapUDPPorts
)

// TxOptions controls one View or Update.
type TxOptions struct {
	// CreateIfMissing, for Update only, creates an empty registry before fn
	// runs. View ignores it.
	CreateIfMissing bool
	// Require lists maps that must validate before fn runs. A map the
	// operation does not need never blocks it.
	Require []MapKind
	// DryRun, for Update only, rehearses without committing (rule 9). View
	// ignores it.
	DryRun bool
}

// Counts is the diagnostic size of each map, including maps that only partly
// decoded. It is not a validity signal.
type Counts struct {
	SiteIDs          int
	SubnetPools      int
	ServiceAddresses int
	UDPPorts         int
}

// Allocations is a validated snapshot of the maps plan and check compare.
// It carries no file bookkeeping.
type Allocations struct {
	SiteIDs          map[string]Entry
	SubnetPools      map[string]Entry
	ServiceAddresses map[string]ServiceEntry
}

// Store is the backend-agnostic registry storage seam.
//
// Every backend meets these rules:
//
//  1. Not found. View and Update without CreateIfMissing on an absent
//     registry return an error matching [errors.Is](err, ErrNotFound).
//  2. Serialization. Update calls for the same tailnet do not interleave.
//     Their read-decide-write spans are mutually exclusive.
//  3. Atomicity. If fn returns an error, no mutation made by that fn is
//     persisted. CreateIfMissing may still leave an empty registry, because
//     that creation happens before fn.
//  4. Read-your-writes. Snapshot reads inside an Update reflect earlier
//     mutations from the same transaction.
//  5. Scoped validation. Require lists the maps that must validate before
//     fn runs. Accessors for an invalid map return that map's error, never
//     a partial view. A backend whose maps cannot be invalid returns nil
//     errors.
//  6. Canonical keys in, canonical keys out. Callers pass keys already
//     normalized by this package. Backends do not see raw spellings and do
//     not apply allocation rules.
//  7. Deletes fail closed. Delete of an absent key in a valid map returns
//     (false, nil). When the backend cannot tell whether the key is present,
//     it may attempt backend-specific recovery. If that fails, it returns an
//     error rather than (false, nil).
//  8. Commit failures. A failure while making mutations durable is returned
//     as *CommitError.
//  9. Rehearsal. Update with DryRun validates and runs fn as a real Update
//     does, then discards every mutation. It persists nothing, creates
//     nothing (CreateIfMissing supplies an empty in-memory registry), does
//     not run the WithAfterSuccess hook, and need not serialize.
//
// After fn succeeds and the commit is durable, and before the backend
// releases serialization, Update calls the hook registered with
// WithAfterSuccess. That keeps ledger diagnostic events after the write and
// inside the lock.
type Store interface {
	View(ctx context.Context, tailnet string, opts TxOptions, fn func(Snapshot) error) error
	Update(ctx context.Context, tailnet string, opts TxOptions, fn func(Tx) error) error
}

// Snapshot is a read-only view of one tailnet registry.
type Snapshot interface {
	SiteIDs() (map[string]Entry, error)
	SubnetPools() (map[string]Entry, error)
	ServiceAddresses() (map[string]ServiceEntry, error)
	UDPPorts() (map[string]map[string]string, error)
	Counts() Counts
}

// Tx is a Snapshot that can mutate the registry inside Update.
type Tx interface {
	Snapshot
	PutSiteID(id string, entry Entry) error
	DeleteSiteID(id string) (bool, error)
	PutSubnetPool(cidr string, entry Entry) error
	DeleteSubnetPool(cidr string) (bool, error)
	PutServiceAddress(address string, entry ServiceEntry) error
	DeleteServiceAddress(address string) (bool, error)
	PutUDPPort(site, port, device string) error
	DeleteUDPPort(site, port string) (bool, error)
}

type afterSuccessKey struct{}

// WithAfterSuccess registers fn to run after a successful Update commit and
// before the backend releases serialization. A nil fn clears nothing; the
// latest registration wins.
func WithAfterSuccess(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, afterSuccessKey{}, fn)
}

// RunAfterSuccess runs the hook registered with WithAfterSuccess.
// Store implementations call it after a durable successful Update.
func RunAfterSuccess(ctx context.Context) {
	fn, _ := ctx.Value(afterSuccessKey{}).(func())
	if fn != nil {
		fn()
	}
}
