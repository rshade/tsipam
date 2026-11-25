package registry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultHistoryLimit is the number of events History returns when a caller
// has no limit of its own.
const DefaultHistoryLimit = 100

// ErrHistoryUnsupported reports a backend that keeps no audit history.
var ErrHistoryUnsupported = errors.New(
	"registry history requires the sqlite backend; a file registry's history lives in version control",
)

// Action names the operation behind a registry mutation.
type Action string

const (
	// ActionReserve is a reserve command's mutation.
	ActionReserve Action = "reserve"
	// ActionAllocate is an allocate command's mutation.
	ActionAllocate Action = "allocate"
	// ActionRelease is a release command's mutation.
	ActionRelease Action = "release"
	// ActionImport is a bulk import's mutation.
	ActionImport Action = "import"
)

// String returns the audit name of the map.
func (k MapKind) String() string {
	switch k {
	case MapSiteIDs:
		return "site_id"
	case MapSubnetPools:
		return "subnet_pool"
	case MapServiceAddresses:
		return "service_address"
	case MapUDPPorts:
		return "udp_port"
	default:
		return "unknown"
	}
}

// ParseMapKind is the inverse of [MapKind.String]. An unknown name is invalid
// input.
func ParseMapKind(name string) (MapKind, error) {
	for _, kind := range []MapKind{MapSiteIDs, MapSubnetPools, MapServiceAddresses, MapUDPPorts} {
		if kind.String() == name {
			return kind, nil
		}
	}
	return 0, invalidInput(fmt.Errorf(
		"reservation kind %q must be site_id, subnet_pool, service_address, or udp_port", name,
	))
}

// EventValue is one reservation's stored value at a point in time. Target is
// the site of a site ID or subnet pool, the service of a service address, or
// the device of a UDP port.
type EventValue struct {
	ReservationMetadata

	Target string
}

// Event is one append-only audit record of a registry mutation.
type Event struct {
	// At is an RFC 3339 UTC timestamp.
	At     string
	Action Action
	Kind   MapKind
	// Key is the canonical map key.
	Key string
	// Site is set for UDP ports only, whose keys are scoped to a site.
	Site string
	// Before is nil when the key was not reserved.
	Before *EventValue
	// After is nil when the key was released.
	After *EventValue
	// Actor is empty for a change made by a local command.
	Actor string
}

// HistoryFilter selects events. Zero fields do not filter.
type HistoryFilter struct {
	Kind MapKind
	// Key matches the canonical map key exactly.
	Key string
	// Since keeps events at or after this time.
	Since time.Time
	// Limit is the most events returned; it must be positive.
	Limit int
}

// HistoryReader is implemented by a Store that records audit events. History
// returns matching events newest first, and an error matching ErrNotFound for
// an absent registry.
type HistoryReader interface {
	History(ctx context.Context, tailnet string, filter HistoryFilter) ([]Event, error)
}

// Identities are the identity values a registry stores, as stored and
// unvalidated. A sanitizer registers them before any error text is rendered.
type Identities struct {
	Sites     []string
	CIDRs     []string
	Addresses []string
	Services  []string
	Devices   []string
	Owners    []string
	Actors    []string
}

// IdentitySource is implemented by a Store that can list its identity values.
// It is best effort: an unreadable registry or an invalid value is skipped
// rather than reported, because the caller is usually about to render the
// error that invalid value caused.
type IdentitySource interface {
	// IdentityValues touches no file when rehearsing, as rule 9 of Store asks.
	IdentityValues(ctx context.Context, tailnet string, rehearsing bool) Identities
}

type actorKey struct{}

// WithActor records who a mutation made under ctx is attributed to. Only an
// authenticated source may set it; local commands never do.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor set by WithActor, or "" when there is none.
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}

// EventAction returns the options' action, or fallback when a caller that
// bypasses the Ledger set none.
func (o TxOptions) EventAction(fallback Action) Action {
	if o.Action == "" {
		return fallback
	}
	return o.Action
}

// EventTime returns the current time from the options' clock as an event
// timestamp.
func (o TxOptions) EventTime() string {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return now().UTC().Format(time.RFC3339)
}

// History returns the registry's audit events, newest first. It returns
// ErrHistoryUnsupported when the store records none.
func (l *Ledger) History(ctx context.Context, tailnet string, filter HistoryFilter) ([]Event, error) {
	if filter.Limit < 1 {
		return nil, invalidInput(fmt.Errorf("history limit must be at least 1, got %d", filter.Limit))
	}
	reader, ok := l.backing().(HistoryReader)
	if !ok {
		return nil, ErrHistoryUnsupported
	}
	return reader.History(ctx, tailnet, filter)
}

// IdentityValues returns the store's identity values, or none when the store
// cannot list them.
func (l *Ledger) IdentityValues(ctx context.Context, tailnet string) Identities {
	source, ok := l.backing().(IdentitySource)
	if !ok {
		return Identities{}
	}
	_, rehearsing := l.store.(rehearsalStore)
	return source.IdentityValues(ctx, tailnet, rehearsing)
}
