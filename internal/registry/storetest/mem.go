// Package storetest holds the registry.Store conformance suite and an
// in-memory reference store. Only tests import it.
package storetest

import (
	"context"
	"errors"
	"maps"
	"sync"

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

var _ registry.Store = (*MemStore)(nil)

type memRegistry struct {
	siteIDs          map[string]registry.Entry
	subnetPools      map[string]registry.Entry
	serviceAddresses map[string]registry.ServiceEntry
	udpPorts         map[string]map[string]string
}

func (r *memRegistry) clone() *memRegistry {
	out := &memRegistry{
		siteIDs:          cloneEntries(r.siteIDs),
		subnetPools:      cloneEntries(r.subnetPools),
		serviceAddresses: cloneServices(r.serviceAddresses),
		udpPorts:         map[string]map[string]string{},
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
	repaired bool
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
	t.reg.siteIDs[id] = entry
	return nil
}

func (t *memTx) DeleteSiteID(id string) (bool, error) {
	if _, ok := t.reg.siteIDs[id]; !ok {
		return false, nil
	}
	delete(t.reg.siteIDs, id)
	return true, nil
}

func (t *memTx) PutSubnetPool(cidr string, entry registry.Entry) error {
	t.reg.subnetPools[cidr] = entry
	return nil
}

func (t *memTx) DeleteSubnetPool(cidr string) (bool, error) {
	if _, ok := t.reg.subnetPools[cidr]; !ok {
		return false, nil
	}
	delete(t.reg.subnetPools, cidr)
	return true, nil
}

func (t *memTx) PutServiceAddress(address string, entry registry.ServiceEntry) error {
	t.reg.serviceAddresses[address] = entry
	return nil
}

func (t *memTx) DeleteServiceAddress(address string) (bool, error) {
	if _, ok := t.reg.serviceAddresses[address]; !ok {
		return false, nil
	}
	delete(t.reg.serviceAddresses, address)
	return true, nil
}

func (t *memTx) PutUDPPort(site, port, device string) error {
	if t.reg.udpPorts[site] == nil {
		t.reg.udpPorts[site] = map[string]string{}
	}
	t.reg.udpPorts[site][port] = device
	return nil
}

func (t *memTx) DeleteUDPPort(site, port string) (bool, error) {
	if _, ok := t.reg.udpPorts[site][port]; !ok {
		return false, nil
	}
	delete(t.reg.udpPorts[site], port)
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
	tx := &memTx{reg: working}
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
