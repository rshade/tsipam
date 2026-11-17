package hujsonstore

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tailscale/hujson"

	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tsipam/internal/registry"
)

func (doc *document) loadUDPPorts(object *hujson.Object) {
	idx, err := memberIndex(object, udpPortsKey)
	if err != nil {
		doc.udpPortsErr = fmt.Errorf(decodingRegistryPrefix, err)
		return
	}
	if idx < 0 {
		return
	}
	member := object.Members[idx].Value
	sites, ok := member.Value.(*hujson.Object)
	if !ok {
		if hujsonNull(member) {
			return
		}
		doc.udpPortsErr = errors.New("decoding registry: \"udpPorts\" must be an object or null")
		return
	}
	doc.udpPortsPresent = true
	doc.udpPorts = make(map[string]map[string]string, len(sites.Members))
	doc.udpPortSiteRawKeys = make(map[string]string, len(sites.Members))
	doc.udpPortRawKeys = make(map[string]map[string]string, len(sites.Members))
	doc.udpPortSitePresent = make(map[string]bool, len(sites.Members))
	doc.udpPortsErr = doc.loadUDPPortSites(sites)
}

func (doc *document) loadUDPPortSites(sites *hujson.Object) error {
	rawSites := make(map[string]struct{}, len(sites.Members))
	for i := range sites.Members {
		if err := doc.loadUDPPortSite(sites.Members[i], rawSites); err != nil {
			return err
		}
	}
	return nil
}

func (doc *document) loadUDPPortSite(member hujson.ObjectMember, rawSites map[string]struct{}) error {
	rawSite, nameOK := literalName(member.Name.Value)
	if !nameOK {
		return nil
	}
	if _, duplicate := rawSites[rawSite]; duplicate {
		return fmt.Errorf("decoding registry: duplicate udpPorts site key %q", rawSite)
	}
	rawSites[rawSite] = struct{}{}

	site, err := registry.NormalizeSiteName(rawSite)
	if err != nil {
		return fmt.Errorf(
			"decoding registry: invalid UDP-port site key %q: %w",
			rawSite, registry.StoredStateError(err),
		)
	}
	if firstRaw, duplicate := doc.udpPortSiteRawKeys[site]; duplicate {
		return fmt.Errorf(
			"decoding registry: UDP-port site is defined twice, as %q and %q",
			firstRaw, rawSite,
		)
	}
	doc.udpPortSiteRawKeys[site] = rawSite

	ports, isObject := member.Value.Value.(*hujson.Object)
	if !isObject {
		if hujsonNull(member.Value) {
			doc.udpPorts[site] = nil
			return nil
		}
		return fmt.Errorf("decoding registry: UDP-port site %q must be an object or null", rawSite)
	}
	doc.udpPortSitePresent[site] = true
	doc.udpPorts[site] = make(map[string]string, len(ports.Members))
	doc.udpPortRawKeys[site] = make(map[string]string, len(ports.Members))
	return doc.loadUDPPortEntries(site, rawSite, ports)
}

func (doc *document) loadUDPPortEntries(site, rawSite string, ports *hujson.Object) error {
	rawPorts := make(map[string]struct{}, len(ports.Members))
	for i := range ports.Members {
		if err := doc.loadUDPPortEntry(site, rawSite, ports.Members[i], rawPorts); err != nil {
			return err
		}
	}
	return nil
}

func (doc *document) loadUDPPortEntry(
	site, rawSite string, member hujson.ObjectMember, rawPorts map[string]struct{},
) error {
	rawPort, nameOK := literalName(member.Name.Value)
	if !nameOK {
		return nil
	}
	if _, duplicate := rawPorts[rawPort]; duplicate {
		return fmt.Errorf(
			"decoding registry: duplicate UDP-port entry key %q in site %q",
			rawPort, rawSite,
		)
	}
	rawPorts[rawPort] = struct{}{}

	port, err := registry.NormalizePort(rawPort)
	if err != nil {
		return fmt.Errorf(
			"decoding registry: invalid UDP port key %q in site %q: %w",
			rawPort, rawSite, registry.StoredStateError(err),
		)
	}
	if firstRaw, duplicate := doc.udpPortRawKeys[site][port]; duplicate {
		return fmt.Errorf(
			"decoding registry: UDP port %s in site %q is defined twice, as %q and %q",
			port, rawSite, firstRaw, rawPort,
		)
	}
	device, err := udpDevice(member, port, rawSite)
	if err != nil {
		return err
	}
	doc.udpPorts[site][port] = device
	doc.udpPortRawKeys[site][port] = rawPort
	return nil
}

func udpDevice(member hujson.ObjectMember, port, rawSite string) (string, error) {
	var device string
	if err := json.Unmarshal(member.Value.Pack(), &device); err != nil {
		return "", fmt.Errorf(
			"decoding registry: device for UDP port %s in site %q must be a string: %w",
			port, rawSite, err,
		)
	}
	device, err := registry.NormalizeDeviceName(device)
	if err != nil {
		return "", fmt.Errorf(
			"decoding registry: UDP port %s in site %q: %w",
			port, rawSite, registry.StoredStateError(err),
		)
	}
	return device, nil
}

func reserveUDPPortPatchOp(doc *document, site, port, device string) hujsonedit.PatchOperation {
	if !doc.udpPortsPresent {
		return hujsonedit.PatchAdd(
			map[string]map[string]string{site: {port: device}},
			udpPortsKey,
		)
	}
	rawSite := doc.udpPortSiteRawKeys[site]
	if rawSite == "" {
		return hujsonedit.PatchAdd(map[string]string{port: device}, udpPortsKey, site)
	}
	if !doc.udpPortSitePresent[site] {
		return hujsonedit.PatchAdd(map[string]string{port: device}, udpPortsKey, rawSite)
	}
	rawPort := port
	if existingRaw := doc.udpPortRawKeys[site][port]; existingRaw != "" {
		rawPort = existingRaw
	}
	return hujsonedit.PatchAdd(device, udpPortsKey, rawSite, rawPort)
}
