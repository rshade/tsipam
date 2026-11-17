package hujsonstore

import (
	"errors"
	"fmt"

	"github.com/tailscale/hujson"

	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tsipam/internal/registry"
)

type stagedRepair struct {
	udp      bool
	topLevel string
	rawKey   string
	rawSite  string
	rawPort  string
}

func (repair stagedRepair) writeError(err error) error {
	stripped := identityFreePathError(err)
	if repair.udp {
		return fmt.Errorf(
			"repairing %s: removing site %q port %q would fix it, but writing the repaired registry failed: %w",
			udpPortsKey, repair.rawSite, repair.rawPort, stripped,
		)
	}
	return fmt.Errorf(
		"repairing %s: removing %q would fix it, but writing the repaired registry failed: %w",
		repair.topLevel, repair.rawKey, stripped,
	)
}

func normalizeKey(topLevel, key string) (string, error) {
	switch topLevel {
	case siteIDsKey:
		return registry.NormalizeSiteID(key)
	case subnetPoolsKey:
		return registry.NormalizeSubnetCIDR(key)
	case serviceAddressesKey:
		return registry.NormalizeServiceAddress(key)
	case udpPortsKey:
		return "", errors.New("nested map has no single canonical key")
	default:
		return "", fmt.Errorf("unknown registry map %q", topLevel)
	}
}

func topLevelObject(document []byte, topLevel string) (*hujson.Object, bool) {
	value, err := hujson.Parse(document)
	if err != nil {
		return nil, false
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, false
	}
	idx, indexErr := memberIndex(object, topLevel)
	if indexErr != nil || idx < 0 {
		return nil, false
	}
	entries, isObject := object.Members[idx].Value.Value.(*hujson.Object)
	if !isObject {
		return nil, false
	}
	return entries, true
}

func repairRawKeys(document []byte, topLevel, key string) []string {
	entries, ok := topLevelObject(document, topLevel)
	if !ok {
		return nil
	}
	rawKeys := make([]string, 0, 1)
	for i := range entries.Members {
		rawKey, isLiteral := literalName(entries.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		canonicalKey, normalizeErr := normalizeKey(topLevel, rawKey)
		if normalizeErr == nil && canonicalKey == key {
			rawKeys = append(rawKeys, rawKey)
		}
	}
	return rawKeys
}

func repairCandidate(document []byte, topLevel, key string) (*document, bool) {
	reloaded, err := loadBytes(document)
	if err != nil || reloaded.topLevelError(topLevel) != nil {
		return nil, false
	}
	view, viewErr := reloaded.view(topLevel)
	if viewErr != nil || view.hasKey(key) {
		return nil, false
	}
	return reloaded, true
}

func repairRawUDPSites(document []byte, site string) []string {
	sites, ok := topLevelObject(document, udpPortsKey)
	if !ok {
		return nil
	}
	rawSites := make([]string, 0, 1)
	for i := range sites.Members {
		literal, isLiteral := literalName(sites.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		canonicalSite, normalizeErr := registry.NormalizeSiteName(literal)
		if normalizeErr == nil && canonicalSite == site {
			rawSites = append(rawSites, literal)
		}
	}
	return rawSites
}

func repairRawUDPPorts(document []byte, rawSite, port string) []string {
	sites, ok := topLevelObject(document, udpPortsKey)
	if !ok {
		return nil
	}
	ports, found := udpSiteObject(sites, rawSite)
	if !found {
		return nil
	}
	rawPorts := make([]string, 0, 1)
	for i := range ports.Members {
		literal, isLiteral := literalName(ports.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		canonicalPort, normalizeErr := registry.NormalizePort(literal)
		if normalizeErr == nil && canonicalPort == port {
			rawPorts = append(rawPorts, literal)
		}
	}
	return rawPorts
}

func udpSiteObject(sites *hujson.Object, rawSite string) (*hujson.Object, bool) {
	for i := range sites.Members {
		literal, isLiteral := literalName(sites.Members[i].Name.Value)
		if !isLiteral || literal != rawSite {
			continue
		}
		ports, ok := sites.Members[i].Value.Value.(*hujson.Object)
		if !ok {
			return nil, false
		}
		return ports, true
	}
	return nil, false
}

func repairUDPCandidate(document []byte, site, port string) (*document, bool) {
	reloaded, err := loadBytes(document)
	if err != nil || reloaded.udpPortsErr != nil {
		return nil, false
	}
	if _, exists := reloaded.udpPorts[site][port]; exists {
		return nil, false
	}
	return reloaded, true
}

func stageFlatRepair(pending []byte, topLevel, key string) ([]byte, *document, stagedRepair, bool) {
	for _, rawKey := range repairRawKeys(pending, topLevel, key) {
		candidate, patchErr := hujsonedit.PatchBytes(pending, nil, hujsonedit.PatchRemove(topLevel, rawKey))
		if patchErr != nil {
			continue
		}
		doc, fixed := repairCandidate(candidate, topLevel, key)
		if !fixed {
			continue
		}
		return candidate, doc, stagedRepair{topLevel: topLevel, rawKey: rawKey}, true
	}
	return nil, nil, stagedRepair{}, false
}

func stageUDPRepair(pending []byte, site, port string) ([]byte, *document, stagedRepair, bool) {
	for _, rawSite := range repairRawUDPSites(pending, site) {
		candidate, doc, repair, ok := stageUDPSiteRepair(pending, rawSite, site, port)
		if ok {
			return candidate, doc, repair, true
		}
	}
	return nil, nil, stagedRepair{}, false
}

func stageUDPSiteRepair(pending []byte, rawSite, site, port string) ([]byte, *document, stagedRepair, bool) {
	for _, rawPort := range repairRawUDPPorts(pending, rawSite, port) {
		candidate, patchErr := hujsonedit.PatchBytes(
			pending, nil, hujsonedit.PatchRemove(udpPortsKey, rawSite, rawPort),
		)
		if patchErr != nil {
			continue
		}
		doc, fixed := repairUDPCandidate(candidate, site, port)
		if !fixed {
			continue
		}
		repair := stagedRepair{udp: true, rawSite: rawSite, rawPort: rawPort, topLevel: udpPortsKey}
		return candidate, doc, repair, true
	}
	return nil, nil, stagedRepair{}, false
}
