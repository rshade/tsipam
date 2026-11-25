// Package storetest holds the registry.Store conformance suite and an
// in-memory reference store. Only tests import it.
package storetest

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

// MemStore is a map-backed registry.Store guarded by one mutex.
// Transactions copy on begin and swap on success.
type MemStore struct {
	mu         sync.Mutex
	regs       map[string]*memRegistry
	failCommit bool
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{regs: map[string]*memRegistry{}}
}

// FailNextCommit makes the next successful Update return *registry.CommitError
// and persist nothing.
func (s *MemStore) FailNextCommit() {
	s.mu.Lock()
	s.failCommit = true
	s.mu.Unlock()
}

var (
	_ registry.Store          = (*MemStore)(nil)
	_ registry.HistoryReader  = (*MemStore)(nil)
	_ registry.IdentitySource = (*MemStore)(nil)
)

type memRegistry struct {
	siteIDs          map[string]registry.Entry
	subnetPools      map[string]registry.Entry
	serviceAddresses map[string]registry.ServiceEntry
	udpPorts         map[string]map[string]string
	// events is oldest first.
	events []registry.Event
}

func (r *memRegistry) clone() *memRegistry {
	out := &memRegistry{
		siteIDs:          cloneEntries(r.siteIDs),
		subnetPools:      cloneEntries(r.subnetPools),
		serviceAddresses: cloneServices(r.serviceAddresses),
		udpPorts:         map[string]map[string]string{},
		events:           slices.Clone(r.events),
	}
	for site, ports := range r.udpPorts {
		out.udpPorts[site] = cloneStrings(ports)
	}
	return out
}

func cloneEntries(in map[string]registry.Entry) map[string]registry.Entry {
	if in == nil {
		return map[string]registry.Entry{}
	}
	out := make(map[string]registry.Entry, len(in))
	maps.Copy(out, in)
	return out
}

func cloneServices(in map[string]registry.ServiceEntry) map[string]registry.ServiceEntry {
	if in == nil {
		return map[string]registry.ServiceEntry{}
	}
	out := make(map[string]registry.ServiceEntry, len(in))
	maps.Copy(out, in)
	return out
}

func cloneStrings(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

type memTx struct {
	reg      *memRegistry
	opts     registry.TxOptions
	actor    string
	repaired bool
}

func (t *memTx) record(event registry.Event, fallback registry.Action) {
	event.At = t.opts.EventTime()
	event.Action = t.opts.EventAction(fallback)
	event.Actor = t.actor
	t.reg.events = append(t.reg.events, event)
}

func entryValue(entry registry.Entry, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: entry.Site, ReservationMetadata: entry.ReservationMetadata}
}

func serviceValue(entry registry.ServiceEntry, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: entry.Service, ReservationMetadata: entry.ReservationMetadata}
}

func deviceValue(device string, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: device}
}

func (t *memTx) Repaired() bool { return t.repaired }

func (t *memTx) SiteIDs() (map[string]registry.Entry, error) {
	return t.reg.siteIDs, nil
}

func (t *memTx) SubnetPools() (map[string]registry.Entry, error) {
	return t.reg.subnetPools, nil
}

func (t *memTx) ServiceAddresses() (map[string]registry.ServiceEntry, error) {
	return t.reg.serviceAddresses, nil
}

func (t *memTx) UDPPorts() (map[string]map[string]string, error) {
	return t.reg.udpPorts, nil
}

func (t *memTx) Counts() registry.Counts {
	ports := 0
	for _, site := range t.reg.udpPorts {
		ports += len(site)
	}
	return registry.Counts{
		SiteIDs:          len(t.reg.siteIDs),
		SubnetPools:      len(t.reg.subnetPools),
		ServiceAddresses: len(t.reg.serviceAddresses),
		UDPPorts:         ports,
	}
}

func (t *memTx) PutSiteID(id string, entry registry.Entry) error {
	prior, exists := t.reg.siteIDs[id]
	t.reg.siteIDs[id] = entry
	t.record(registry.Event{
		Kind: registry.MapSiteIDs, Key: id,
		Before: entryValue(prior, exists), After: entryValue(entry, true),
	}, registry.ActionReserve)
	return nil
}

func (t *memTx) DeleteSiteID(id string) (bool, error) {
	prior, exists := t.reg.siteIDs[id]
	if !exists {
		return false, nil
	}
	delete(t.reg.siteIDs, id)
	t.record(registry.Event{
		Kind: registry.MapSiteIDs, Key: id, Before: entryValue(prior, true),
	}, registry.ActionRelease)
	return true, nil
}

func (t *memTx) PutSubnetPool(cidr string, entry registry.Entry) error {
	prior, exists := t.reg.subnetPools[cidr]
	t.reg.subnetPools[cidr] = entry
	t.record(registry.Event{
		Kind: registry.MapSubnetPools, Key: cidr,
		Before: entryValue(prior, exists), After: entryValue(entry, true),
	}, registry.ActionReserve)
	return nil
}

func (t *memTx) DeleteSubnetPool(cidr string) (bool, error) {
	prior, exists := t.reg.subnetPools[cidr]
	if !exists {
		return false, nil
	}
	delete(t.reg.subnetPools, cidr)
	t.record(registry.Event{
		Kind: registry.MapSubnetPools, Key: cidr, Before: entryValue(prior, true),
	}, registry.ActionRelease)
	return true, nil
}

func (t *memTx) PutServiceAddress(address string, entry registry.ServiceEntry) error {
	prior, exists := t.reg.serviceAddresses[address]
	t.reg.serviceAddresses[address] = entry
	t.record(registry.Event{
		Kind: registry.MapServiceAddresses, Key: address,
		Before: serviceValue(prior, exists), After: serviceValue(entry, true),
	}, registry.ActionReserve)
	return nil
}

func (t *memTx) DeleteServiceAddress(address string) (bool, error) {
	prior, exists := t.reg.serviceAddresses[address]
	if !exists {
		return false, nil
	}
	delete(t.reg.serviceAddresses, address)
	t.record(registry.Event{
		Kind: registry.MapServiceAddresses, Key: address, Before: serviceValue(prior, true),
	}, registry.ActionRelease)
	return true, nil
}

func (t *memTx) PutUDPPort(site, port, device string) error {
	if t.reg.udpPorts[site] == nil {
		t.reg.udpPorts[site] = map[string]string{}
	}
	prior, exists := t.reg.udpPorts[site][port]
	t.reg.udpPorts[site][port] = device
	t.record(registry.Event{
		Kind: registry.MapUDPPorts, Key: port, Site: site,
		Before: deviceValue(prior, exists), After: deviceValue(device, true),
	}, registry.ActionReserve)
	return nil
}

func (t *memTx) DeleteUDPPort(site, port string) (bool, error) {
	prior, exists := t.reg.udpPorts[site][port]
	if !exists {
		return false, nil
	}
	delete(t.reg.udpPorts[site], port)
	t.record(registry.Event{
		Kind: registry.MapUDPPorts, Key: port, Site: site, Before: deviceValue(prior, true),
	}, registry.ActionRelease)
	return true, nil
}

// View reads one tailnet. An absent tailnet is ErrNotFound.
func (s *MemStore) View(
	_ context.Context,
	tailnet string,
	opts registry.TxOptions,
	fn func(registry.Snapshot) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.regs[tailnet]
	if !ok {
		return registry.ErrNotFound
	}
	if err := requireMem(opts.Require); err != nil {
		return err
	}
	return fn(&memTx{reg: reg.clone()})
}

// Update serializes read-decide-write for every tailnet on this store.
// CreateIfMissing installs an empty registry before fn, and keeps it when fn
// returns an error. Mutations made by fn are discarded unless fn returns nil.
// DryRun discards them, and the installed registry, either way.
func (s *MemStore) Update(
	ctx context.Context,
	tailnet string,
	opts registry.TxOptions,
	fn func(registry.Tx) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.regs[tailnet]
	if !ok {
		if !opts.CreateIfMissing {
			return registry.ErrNotFound
		}
		reg = &memRegistry{
			siteIDs:          map[string]registry.Entry{},
			subnetPools:      map[string]registry.Entry{},
			serviceAddresses: map[string]registry.ServiceEntry{},
			udpPorts:         map[string]map[string]string{},
		}
		if !opts.DryRun {
			s.regs[tailnet] = reg
		}
	}
	if err := requireMem(opts.Require); err != nil {
		return err
	}
	working := reg.clone()
	tx := &memTx{reg: working, opts: opts, actor: registry.ActorFrom(ctx)}
	if err := fn(tx); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	if s.failCommit {
		s.failCommit = false
		return &registry.CommitError{Err: errors.New("commit failed")}
	}
	s.regs[tailnet] = working
	registry.RunAfterSuccess(ctx)
	return nil
}

// History returns matching events newest first.
func (s *MemStore) History(
	_ context.Context, tailnet string, filter registry.HistoryFilter,
) ([]registry.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.regs[tailnet]
	if !ok {
		return nil, registry.ErrNotFound
	}
	matched := []registry.Event{}
	for _, event := range slices.Backward(reg.events) {
		if len(matched) == filter.Limit {
			break
		}
		keep, err := matches(event, filter)
		if err != nil {
			return nil, err
		}
		if keep {
			matched = append(matched, event)
		}
	}
	return matched, nil
}

func matches(event registry.Event, filter registry.HistoryFilter) (bool, error) {
	if filter.Kind != 0 && event.Kind != filter.Kind {
		return false, nil
	}
	if filter.Key != "" && event.Key != filter.Key {
		return false, nil
	}
	at, err := time.Parse(time.RFC3339, event.At)
	if err != nil {
		return false, err
	}
	return !at.Before(filter.Since), nil
}

// IdentityValues returns the identity values of one tailnet's registry.
func (s *MemStore) IdentityValues(_ context.Context, tailnet string, _ bool) registry.Identities {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out registry.Identities
	reg, ok := s.regs[tailnet]
	if !ok {
		return out
	}
	for _, entry := range reg.siteIDs {
		out.Sites = append(out.Sites, entry.Site)
		out.Owners = append(out.Owners, entry.Owner)
	}
	for cidr, entry := range reg.subnetPools {
		out.CIDRs = append(out.CIDRs, cidr)
		out.Sites = append(out.Sites, entry.Site)
		out.Owners = append(out.Owners, entry.Owner)
	}
	for address, entry := range reg.serviceAddresses {
		out.Addresses = append(out.Addresses, address)
		out.Services = append(out.Services, entry.Service)
		out.Owners = append(out.Owners, entry.Owner)
	}
	for site, ports := range reg.udpPorts {
		out.Sites = append(out.Sites, site)
		for _, device := range ports {
			out.Devices = append(out.Devices, device)
		}
	}
	for _, event := range reg.events {
		out.Actors = append(out.Actors, event.Actor)
	}
	return out
}

func requireMem(kinds []registry.MapKind) error {
	for _, kind := range kinds {
		switch kind {
		case registry.MapSiteIDs, registry.MapSubnetPools, registry.MapServiceAddresses, registry.MapUDPPorts:
		default:
			return errors.New("unknown registry map")
		}
	}
	return nil
}
