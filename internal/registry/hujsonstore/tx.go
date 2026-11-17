package hujsonstore

import (
	"fmt"

	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tsipam/internal/registry"
)

type snapshot struct {
	doc *document
}

func (s snapshot) SiteIDs() (map[string]registry.Entry, error) {
	if s.doc.siteIDsErr != nil {
		return nil, s.doc.siteIDsErr
	}
	return s.doc.siteIDs, nil
}

func (s snapshot) SubnetPools() (map[string]registry.Entry, error) {
	if s.doc.subnetPoolsErr != nil {
		return nil, s.doc.subnetPoolsErr
	}
	return s.doc.subnetPools, nil
}

func (s snapshot) ServiceAddresses() (map[string]registry.ServiceEntry, error) {
	if s.doc.serviceAddressesErr != nil {
		return nil, s.doc.serviceAddressesErr
	}
	return s.doc.serviceAddresses, nil
}

func (s snapshot) UDPPorts() (map[string]map[string]string, error) {
	if s.doc.udpPortsErr != nil {
		return nil, s.doc.udpPortsErr
	}
	return s.doc.udpPorts, nil
}

func (s snapshot) Counts() registry.Counts {
	return s.doc.counts()
}

type bufferTx struct {
	doc      *document
	path     string
	original []byte
	pending  []byte
	repaired bool
	repair   *stagedRepair
}

func (t *bufferTx) SiteIDs() (map[string]registry.Entry, error) {
	return (snapshot{doc: t.doc}).SiteIDs()
}

func (t *bufferTx) SubnetPools() (map[string]registry.Entry, error) {
	return (snapshot{doc: t.doc}).SubnetPools()
}

func (t *bufferTx) ServiceAddresses() (map[string]registry.ServiceEntry, error) {
	return (snapshot{doc: t.doc}).ServiceAddresses()
}

func (t *bufferTx) UDPPorts() (map[string]map[string]string, error) {
	return (snapshot{doc: t.doc}).UDPPorts()
}

func (t *bufferTx) Counts() registry.Counts {
	return (snapshot{doc: t.doc}).Counts()
}

// Repaired reports whether this transaction staged a malformed-map repair.
func (t *bufferTx) Repaired() bool { return t.repaired }

func (t *bufferTx) PutSiteID(id string, entry registry.Entry) error {
	return t.putFlat(siteIDsKey, id, entry)
}

func (t *bufferTx) PutSubnetPool(cidr string, entry registry.Entry) error {
	return t.putFlat(subnetPoolsKey, cidr, entry)
}

func (t *bufferTx) PutServiceAddress(address string, entry registry.ServiceEntry) error {
	return t.putFlat(serviceAddressesKey, address, entry)
}

func (t *bufferTx) PutUDPPort(site, port, device string) error {
	if t.doc.udpPortsErr != nil {
		return t.doc.udpPortsErr
	}
	return t.apply(reserveUDPPortPatchOp(t.doc, site, port, device))
}

func (t *bufferTx) DeleteSiteID(id string) (bool, error) {
	return t.deleteFlat(siteIDsKey, id, "site ID")
}

func (t *bufferTx) DeleteSubnetPool(cidr string) (bool, error) {
	return t.deleteFlat(subnetPoolsKey, cidr, "subnet CIDR")
}

func (t *bufferTx) DeleteServiceAddress(address string) (bool, error) {
	return t.deleteFlat(serviceAddressesKey, address, "service address")
}

func (t *bufferTx) DeleteUDPPort(site, port string) (bool, error) {
	if _, exists := t.doc.udpPorts[site][port]; exists {
		return t.removeUDP(site, port)
	}
	if t.doc.udpPortsErr == nil {
		return false, nil
	}
	return t.repairUDP(site, port)
}

func (t *bufferTx) putFlat(topLevel, key string, value any) error {
	view, err := t.doc.view(topLevel)
	if err != nil {
		return err
	}
	if view.err != nil {
		return view.err
	}
	op := flatAdd(view, topLevel, key, value)
	return t.apply(op)
}

func flatAdd(view mapView, topLevel, key string, value any) hujsonedit.PatchOperation {
	if !view.present {
		return hujsonedit.PatchAdd(map[string]any{key: value}, topLevel)
	}
	rawKey := key
	if view.hasKey(key) && view.rawKeys[key] != "" {
		rawKey = view.rawKeys[key]
	}
	return hujsonedit.PatchAdd(value, topLevel, rawKey)
}

func (t *bufferTx) deleteFlat(topLevel, key, displayName string) (bool, error) {
	view, err := t.doc.view(topLevel)
	if err != nil {
		return false, err
	}
	if view.hasKey(key) {
		rawKey := view.rawKeys[key]
		if rawKey == "" {
			rawKey = key
		}
		if patchErr := t.apply(hujsonedit.PatchRemove(topLevel, rawKey)); patchErr != nil {
			return false, patchErr
		}
		return true, nil
	}
	if view.err == nil || view.hasView {
		return false, nil
	}
	return t.repairFlat(topLevel, key, displayName, view.err)
}

func (t *bufferTx) removeUDP(site, port string) (bool, error) {
	rawSite := t.doc.udpPortSiteRawKeys[site]
	if rawSite == "" {
		rawSite = site
	}
	rawPort := t.doc.udpPortRawKeys[site][port]
	if rawPort == "" {
		rawPort = port
	}
	if err := t.apply(hujsonedit.PatchRemove(udpPortsKey, rawSite, rawPort)); err != nil {
		return false, err
	}
	return true, nil
}

func (t *bufferTx) apply(op hujsonedit.PatchOperation) error {
	patched, err := hujsonedit.PatchBytes(t.pending, nil, op)
	if err != nil {
		return fmt.Errorf("%s: %w", t.path, err)
	}
	doc, loadErr := loadBytes(patched)
	if loadErr != nil {
		return fmt.Errorf("%s: %w", t.path, loadErr)
	}
	t.pending = patched
	t.doc = doc
	t.repair = nil
	return nil
}

func (t *bufferTx) repairFlat(topLevel, key, displayName string, cause error) (bool, error) {
	candidate, doc, repair, ok := stageFlatRepair(t.pending, topLevel, key)
	if !ok {
		return false, fmt.Errorf("cannot determine whether %s %s is reserved: %w", displayName, key, cause)
	}
	t.acceptRepair(candidate, doc, repair)
	return true, nil
}

func (t *bufferTx) repairUDP(site, port string) (bool, error) {
	candidate, doc, repair, ok := stageUDPRepair(t.pending, site, port)
	if !ok {
		return false, fmt.Errorf(
			"cannot determine whether UDP port %s in site %q is reserved: %w",
			port, site, t.doc.udpPortsErr,
		)
	}
	t.acceptRepair(candidate, doc, repair)
	return true, nil
}

func (t *bufferTx) acceptRepair(candidate []byte, doc *document, repair stagedRepair) {
	t.pending = candidate
	t.doc = doc
	t.repaired = true
	t.repair = &repair
}
