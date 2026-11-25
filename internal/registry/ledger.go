package registry

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rshade/tsacl/pkg/ipcalc"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// Ledger applies allocation rules to a Store. Each method keeps the
// historical normalize, decide, mutate, and debug-event sequence.
type Ledger struct {
	store  Store
	logger diagnostics.Logger
	now    func() time.Time
}

// NewLedger returns a ledger that reads time from [time.Now].
func NewLedger(store Store, logger diagnostics.Logger) *Ledger {
	return &Ledger{store: store, logger: logger, now: time.Now}
}

// SetNow replaces the clock used to stamp ReservedAt.
func (l *Ledger) SetNow(now func() time.Time) {
	if now == nil {
		l.now = time.Now
		return
	}
	l.now = now
}

// Rehearsal returns a ledger whose mutating methods decide and report exactly
// as l's do but commit nothing (Store rule 9). Reads are unchanged.
func (l *Ledger) Rehearsal() *Ledger {
	rehearsing := *l
	rehearsing.store = rehearsalStore{Store: l.store}
	return &rehearsing
}

// backing returns the store that reads go to. A rehearsal wraps it, and the
// wrapper would hide the optional interfaces it implements.
func (l *Ledger) backing() Store {
	if rehearsing, ok := l.store.(rehearsalStore); ok {
		return rehearsing.Store
	}
	return l.store
}

// rehearsalStore sets DryRun on every Update.
type rehearsalStore struct {
	Store
}

func (s rehearsalStore) Update(ctx context.Context, tailnet string, opts TxOptions, fn func(Tx) error) error {
	opts.DryRun = true
	return s.Store.Update(ctx, tailnet, opts, fn)
}

type repairReporter interface {
	Repaired() bool
}

func (l *Ledger) after(ctx context.Context, event *string) context.Context {
	return WithAfterSuccess(ctx, func() {
		if event != nil && *event != "" {
			diagnostics.Debug(l.logger, *event)
		}
	})
}

func wrapCommit(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*CommitError](err); !ok {
		return err
	}
	// Repair text is already final. Prefixing it would change the sentence
	// operators and tests match, while still hiding the CommitError type.
	if strings.HasPrefix(err.Error(), "repairing ") {
		return err
	}
	return fmt.Errorf(format, append(args, err)...)
}

// ReserveSiteID reserves id for site, creating the registry when it is absent.
//
//nolint:dupl // mirrors ReserveServiceAddress's normalize/collide/resolve/patch shape
func (l *Ledger) ReserveSiteID(
	ctx context.Context, tailnet, id, site string, metadata ReservationMetadata,
) (string, string, ReservationMetadata, bool, error) {
	id, err := NormalizeSiteID(id)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	site, err = NormalizeSiteName(site)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	var (
		changed        bool
		resultMetadata ReservationMetadata
		event          string
	)
	err = l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		CreateIfMissing: true,
		Require:         []MapKind{MapSiteIDs},
		Action:          ActionReserve,
		Now:             l.now,
	}, func(tx Tx) error {
		sites, readErr := tx.SiteIDs()
		if readErr != nil {
			return readErr
		}
		existing, exists := sites[id]
		if exists && existing.Site != site {
			return &SiteIDCollisionError{ID: id, Existing: existing.Site, Wanted: site}
		}
		newEntry, unchanged := resolveReservationEntry(existing, exists, site, metadata, l.now)
		if unchanged {
			resultMetadata = existing.ReservationMetadata
			event = "site-ID reservation already present"
			return nil
		}
		if putErr := tx.PutSiteID(id, newEntry); putErr != nil {
			return fmt.Errorf("reserving site ID %s: %w", id, putErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		event = "site-ID reservation written"
		return nil
	})
	if err = wrapCommit(err, "reserving site ID %s: %w", id); err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	return id, site, resultMetadata, changed, nil
}

// ReleaseSiteID removes id. An absent id in an existing registry is a no-op.
func (l *Ledger) ReleaseSiteID(ctx context.Context, tailnet, id string) (string, bool, error) {
	id, err := NormalizeSiteID(id)
	if err != nil {
		return "", false, err
	}
	changed, err := l.release(ctx, tailnet, "releasing site ID "+id, "site-ID", func(tx Tx) (bool, error) {
		return tx.DeleteSiteID(id)
	})
	if err != nil {
		return "", false, err
	}
	return id, changed, nil
}

// ListSiteIDs returns reservations sorted numerically by ID.
func (l *Ledger) ListSiteIDs(ctx context.Context, tailnet string) ([]SiteIDEntry, error) {
	var entries []SiteIDEntry
	err := l.store.View(ctx, tailnet, TxOptions{Require: []MapKind{MapSiteIDs}}, func(snap Snapshot) error {
		sites, readErr := snap.SiteIDs()
		if readErr != nil {
			return readErr
		}
		l.logLoaded(snap, true)
		entries = make([]SiteIDEntry, 0, len(sites))
		for id, entry := range sites {
			entries = append(entries, SiteIDEntry{
				ID: id, Site: entry.Site, ReservationMetadata: entry.ReservationMetadata,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := strconv.Atoi(entries[i].ID)
		right, rightErr := strconv.Atoi(entries[j].ID)
		if leftErr == nil && rightErr == nil && left != right {
			return left < right
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

// ReserveSubnetPool reserves cidr for site, creating the registry when needed.
func (l *Ledger) ReserveSubnetPool(
	ctx context.Context, tailnet, cidr, site string, metadata ReservationMetadata,
) (string, string, ReservationMetadata, bool, error) {
	cidr, err := NormalizeSubnetCIDR(cidr)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	site, err = NormalizeSiteName(site)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	var (
		changed        bool
		resultMetadata ReservationMetadata
		event          string
	)
	err = l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		CreateIfMissing: true,
		Require:         []MapKind{MapSubnetPools},
		Action:          ActionReserve,
		Now:             l.now,
	}, func(tx Tx) error {
		pools, readErr := tx.SubnetPools()
		if readErr != nil {
			return readErr
		}
		existing, exists := pools[cidr]
		if conflictErr := checkSubnetPoolConflict(existing, exists, pools, cidr, site); conflictErr != nil {
			return conflictErr
		}
		newEntry, unchanged := resolveReservationEntry(existing, exists, site, metadata, l.now)
		if unchanged {
			resultMetadata = existing.ReservationMetadata
			event = "subnet-pool reservation already present"
			return nil
		}
		if putErr := tx.PutSubnetPool(cidr, newEntry); putErr != nil {
			return fmt.Errorf("reserving subnet CIDR %s: %w", cidr, putErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		event = "subnet-pool reservation written"
		return nil
	})
	if err = wrapCommit(err, "reserving subnet CIDR %s: %w", cidr); err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	return cidr, site, resultMetadata, changed, nil
}

// AllocateSubnetPool reserves the first free size-bit child of pool for site.
func (l *Ledger) AllocateSubnetPool(
	ctx context.Context,
	tailnet, pool string,
	size int,
	site string,
	liveTaken []string,
	metadata ReservationMetadata,
) (string, string, ReservationMetadata, bool, error) {
	poolPrefix, err := NormalizeAllocationPool(pool, size)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	taken, err := parseLiveTaken(liveTaken)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	site, err = NormalizeSiteName(site)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	var (
		cidr           string
		resultMetadata ReservationMetadata
		changed        bool
		event          string
	)
	err = l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		CreateIfMissing: true,
		Require:         []MapKind{MapSubnetPools},
		Action:          ActionAllocate,
		Now:             l.now,
	}, func(tx Tx) error {
		pools, readErr := tx.SubnetPools()
		if readErr != nil {
			return readErr
		}
		if replay, ok := findTicketReplay(pools, poolPrefix, site, metadata.Ticket); ok {
			cidr = replay.CIDR
			resultMetadata = replay.ReservationMetadata
			event = "subnet-pool allocation replayed by ticket"
			return nil
		}
		for reserved := range pools {
			prefix, parseErr := netip.ParsePrefix(reserved)
			if parseErr != nil {
				return fmt.Errorf("stored subnet CIDR %s: %w", reserved, parseErr)
			}
			taken = append(taken, prefix)
		}
		block, ok := ipcalc.NextFree(poolPrefix, size, taken)
		if !ok {
			return &SubnetPoolExhaustedError{Pool: poolPrefix.String(), Size: size}
		}
		cidr = block.String()
		newEntry, _ := resolveReservationEntry(Entry{}, false, site, metadata, l.now)
		if putErr := tx.PutSubnetPool(cidr, newEntry); putErr != nil {
			return fmt.Errorf("allocating subnet CIDR %s: %w", cidr, putErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		event = "subnet-pool allocation written"
		return nil
	})
	if err = wrapCommit(err, "allocating subnet CIDR %s: %w", cidr); err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	return cidr, site, resultMetadata, changed, nil
}

// ReleaseSubnetPool removes cidr. An absent reservation is a no-op.
func (l *Ledger) ReleaseSubnetPool(ctx context.Context, tailnet, cidr string) (string, bool, error) {
	cidr, err := NormalizeSubnetCIDR(cidr)
	if err != nil {
		return "", false, err
	}
	changed, err := l.release(ctx, tailnet, "releasing subnet CIDR "+cidr, "subnet-pool", func(tx Tx) (bool, error) {
		return tx.DeleteSubnetPool(cidr)
	})
	if err != nil {
		return "", false, err
	}
	return cidr, changed, nil
}

// ListSubnetPools returns reservations sorted by network address.
func (l *Ledger) ListSubnetPools(ctx context.Context, tailnet string) ([]SubnetPoolEntry, error) {
	var pools map[string]Entry
	err := l.store.View(ctx, tailnet, TxOptions{Require: []MapKind{MapSubnetPools}}, func(snap Snapshot) error {
		var readErr error
		pools, readErr = snap.SubnetPools()
		if readErr != nil {
			return readErr
		}
		l.logLoaded(snap, false)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedSubnetPoolEntries(pools), nil
}

// ReserveServiceAddress reserves an IPv4 host for a service.
//
//nolint:dupl // mirrors ReserveSiteID's normalize/collide/resolve/patch shape
func (l *Ledger) ReserveServiceAddress(
	ctx context.Context, tailnet, address, service string, metadata ReservationMetadata,
) (string, string, ReservationMetadata, bool, error) {
	address, err := NormalizeServiceAddress(address)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	service, err = NormalizeServiceName(service)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	var (
		changed        bool
		resultMetadata ReservationMetadata
		event          string
	)
	err = l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		CreateIfMissing: true,
		Require:         []MapKind{MapServiceAddresses},
		Action:          ActionReserve,
		Now:             l.now,
	}, func(tx Tx) error {
		addresses, readErr := tx.ServiceAddresses()
		if readErr != nil {
			return readErr
		}
		existing, exists := addresses[address]
		if exists && existing.Service != service {
			return &ServiceAddressCollisionError{
				Address: address, Existing: existing.Service, Wanted: service,
			}
		}
		newEntry, unchanged := resolveServiceEntry(existing, exists, service, metadata, l.now)
		if unchanged {
			resultMetadata = existing.ReservationMetadata
			event = "service-address reservation already present"
			return nil
		}
		if putErr := tx.PutServiceAddress(address, newEntry); putErr != nil {
			return fmt.Errorf("reserving service address %s: %w", address, putErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		event = "service-address reservation written"
		return nil
	})
	if err = wrapCommit(err, "reserving service address %s: %w", address); err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	return address, service, resultMetadata, changed, nil
}

// ReleaseServiceAddress removes an address reservation.
func (l *Ledger) ReleaseServiceAddress(ctx context.Context, tailnet, address string) (string, bool, error) {
	address, err := NormalizeServiceAddress(address)
	if err != nil {
		return "", false, err
	}
	changed, err := l.release(
		ctx,
		tailnet,
		"releasing service address "+address,
		"service-address",
		func(tx Tx) (bool, error) {
			return tx.DeleteServiceAddress(address)
		},
	)
	if err != nil {
		return "", false, err
	}
	return address, changed, nil
}

// ListServiceAddresses returns reservations sorted by numeric IPv4 address.
func (l *Ledger) ListServiceAddresses(ctx context.Context, tailnet string) ([]ServiceAddressEntry, error) {
	var entries []ServiceAddressEntry
	err := l.store.View(ctx, tailnet, TxOptions{Require: []MapKind{MapServiceAddresses}}, func(snap Snapshot) error {
		addresses, readErr := snap.ServiceAddresses()
		if readErr != nil {
			return readErr
		}
		l.logLoaded(snap, false)
		entries = make([]ServiceAddressEntry, 0, len(addresses))
		for address, entry := range addresses {
			entries = append(entries, ServiceAddressEntry{
				Address: address, Service: entry.Service, ReservationMetadata: entry.ReservationMetadata,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := netip.ParseAddr(entries[i].Address)
		right, rightErr := netip.ParseAddr(entries[j].Address)
		if leftErr == nil && rightErr == nil {
			return left.Compare(right) < 0
		}
		return entries[i].Address < entries[j].Address
	})
	return entries, nil
}

// ReserveUDPPort reserves a UDP listen port for a device within a site.
func (l *Ledger) ReserveUDPPort(
	ctx context.Context, tailnet, site, port, device string,
) (string, string, string, bool, error) {
	site, err := NormalizeSiteName(site)
	if err != nil {
		return "", "", "", false, err
	}
	port, err = NormalizePort(port)
	if err != nil {
		return "", "", "", false, err
	}
	device, err = NormalizeDeviceName(device)
	if err != nil {
		return "", "", "", false, err
	}

	var (
		changed bool
		event   string
	)
	err = l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		CreateIfMissing: true,
		Require:         []MapKind{MapUDPPorts},
		Action:          ActionReserve,
		Now:             l.now,
	}, func(tx Tx) error {
		ports, readErr := tx.UDPPorts()
		if readErr != nil {
			return readErr
		}
		existing, exists := ports[site][port]
		if exists && existing != device {
			return &UDPPortCollisionError{Site: site, Port: port, Existing: existing, Wanted: device}
		}
		if exists {
			event = "UDP-port reservation already present"
			return nil
		}
		if putErr := tx.PutUDPPort(site, port, device); putErr != nil {
			return fmt.Errorf("reserving UDP port %s: %w", port, putErr)
		}
		changed = true
		event = "UDP-port reservation written"
		return nil
	})
	if err = wrapCommit(err, "reserving UDP port %s: %w", port); err != nil {
		return "", "", "", false, err
	}
	return site, port, device, changed, nil
}

// ReleaseUDPPort removes a site/port reservation.
func (l *Ledger) ReleaseUDPPort(ctx context.Context, tailnet, site, port string) (string, string, bool, error) {
	site, err := NormalizeSiteName(site)
	if err != nil {
		return "", "", false, err
	}
	port, err = NormalizePort(port)
	if err != nil {
		return "", "", false, err
	}
	changed, err := l.release(ctx, tailnet, "releasing UDP port "+port, "UDP-port", func(tx Tx) (bool, error) {
		return tx.DeleteUDPPort(site, port)
	})
	if err != nil {
		return "", "", false, err
	}
	return site, port, changed, nil
}

// ListUDPPorts returns reservations, optionally filtered to one site, sorted
// by site and numeric port.
func (l *Ledger) ListUDPPorts(ctx context.Context, tailnet, site string) ([]UDPPortEntry, error) {
	if site != "" {
		var err error
		site, err = NormalizeSiteName(site)
		if err != nil {
			return nil, err
		}
	}
	var entries []UDPPortEntry
	err := l.store.View(ctx, tailnet, TxOptions{Require: []MapKind{MapUDPPorts}}, func(snap Snapshot) error {
		ports, readErr := snap.UDPPorts()
		if readErr != nil {
			return readErr
		}
		l.logLoaded(snap, true)
		entries = make([]UDPPortEntry, 0, snap.Counts().UDPPorts)
		for entrySite, sitePorts := range ports {
			if site != "" && entrySite != site {
				continue
			}
			for entryPort, entryDevice := range sitePorts {
				entries = append(entries, UDPPortEntry{Site: entrySite, Port: entryPort, Device: entryDevice})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Site != entries[j].Site {
			return entries[i].Site < entries[j].Site
		}
		left, leftErr := strconv.Atoi(entries[i].Port)
		right, rightErr := strconv.Atoi(entries[j].Port)
		if leftErr == nil && rightErr == nil && left != right {
			return left < right
		}
		return entries[i].Port < entries[j].Port
	})
	return entries, nil
}

// LoadForPlanning reads the maps plan and check compare. It does not create
// a missing registry. UDP-port validation stays isolated from this read.
func (l *Ledger) LoadForPlanning(ctx context.Context, tailnet string) (Allocations, error) {
	var out Allocations
	err := l.store.View(ctx, tailnet, TxOptions{Require: []MapKind{
		MapSiteIDs, MapSubnetPools, MapServiceAddresses,
	}}, func(snap Snapshot) error {
		var readErr error
		if out.SiteIDs, readErr = snap.SiteIDs(); readErr != nil {
			return readErr
		}
		if out.SubnetPools, readErr = snap.SubnetPools(); readErr != nil {
			return readErr
		}
		out.ServiceAddresses, readErr = snap.ServiceAddresses()
		return readErr
	})
	if err != nil {
		return Allocations{}, err
	}
	return out, nil
}

func (l *Ledger) release(
	ctx context.Context,
	tailnet, prefix, diagnosticName string,
	deleteFn func(Tx) (bool, error),
) (bool, error) {
	var (
		changed bool
		event   string
	)
	err := l.store.Update(l.after(ctx, &event), tailnet, TxOptions{
		Action: ActionRelease,
		Now:    l.now,
	}, func(tx Tx) error {
		var delErr error
		changed, delErr = deleteFn(tx)
		if delErr != nil {
			return prefixMutation(prefix, delErr)
		}
		switch {
		case repaired(tx):
			event = diagnosticName + " reservation removed by raw-key repair"
		case changed:
			event = diagnosticName + " reservation removed"
		default:
			event = diagnosticName + " reservation already absent"
		}
		return nil
	})
	if err != nil {
		return false, wrapCommit(err, "%s: %w", prefix)
	}
	return changed, nil
}

// logLoaded emits the historical registry-loaded event. Subnet-pool and
// service-address lists omit udp_port_count; the other lists include it.
// Planning loads stay quiet and do not call this.
func (l *Ledger) logLoaded(snap Snapshot, includeUDP bool) {
	counts := snap.Counts()
	args := []any{
		"site_id_count", counts.SiteIDs,
		"subnet_pool_count", counts.SubnetPools,
		"service_address_count", counts.ServiceAddresses,
	}
	if includeUDP {
		args = append(args, "udp_port_count", counts.UDPPorts)
	}
	diagnostics.Debug(l.logger, "registry loaded", args...)
}

// prefixMutation adds the operation prefix to a patch failure. Fail-closed
// and repair messages already carry their final text.
func prefixMutation(prefix string, err error) error {
	if err == nil || strings.HasPrefix(err.Error(), "cannot determine whether") ||
		strings.HasPrefix(err.Error(), "repairing ") {
		return err
	}
	if strings.HasPrefix(err.Error(), prefix+":") {
		return err
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func repaired(tx Tx) bool {
	reporter, ok := tx.(repairReporter)
	return ok && reporter.Repaired()
}
