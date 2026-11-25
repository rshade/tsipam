package hujsonstore

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/rshade/tsipam/internal/registry"
)

// RawEntries returns best-effort, unvalidated subjects and owners from the
// site-ID, subnet-pool, and service-address maps. Any read or parse failure
// yields nil results so a sanitizer can still run when the document is invalid.
func RawEntries(path string) (map[string]string, map[string]string, map[string]string, []string) {
	object, ok := rawObject(path)
	if !ok {
		return nil, nil, nil, nil
	}
	siteIDs, owners := rawReservationMember(object, siteIDsKey, "site")
	subnetPools, subnetOwners := rawReservationMember(object, subnetPoolsKey, "site")
	serviceAddresses, serviceOwners := rawReservationMember(object, serviceAddressesKey, "service")
	owners = append(owners, subnetOwners...)
	owners = append(owners, serviceOwners...)
	return siteIDs, subnetPools, serviceAddresses, owners
}

// IdentityValues returns the raw identity spellings in tailnet's file. The
// file may be invalid: the loader error about to be rendered echoes them.
func (s *Store) IdentityValues(_ context.Context, tailnet string, _ bool) registry.Identities {
	path := Path(s.dir, tailnet)
	siteIDs, subnetPools, serviceAddresses, owners := RawEntries(path)
	out := registry.Identities{Owners: owners}
	for _, site := range siteIDs {
		out.Sites = append(out.Sites, site)
	}
	for cidr, site := range subnetPools {
		out.CIDRs = append(out.CIDRs, cidr)
		out.Sites = append(out.Sites, site)
	}
	for address, service := range serviceAddresses {
		out.Addresses = append(out.Addresses, address)
		out.Services = append(out.Services, service)
	}
	for _, entry := range RawUDPPorts(path) {
		out.Sites = append(out.Sites, entry.Site)
		out.Devices = append(out.Devices, entry.Device)
	}
	return out
}

// RawUDPPorts returns best-effort site, port, and device spellings from path.
// Malformed values are kept because they are the ones a load error may echo.
func RawUDPPorts(path string) []registry.UDPPortEntry {
	object, ok := rawObject(path)
	if !ok {
		return nil
	}
	idx := lastFoldedMember(object, udpPortsKey)
	if idx < 0 {
		return nil
	}
	sites, isObject := object.Members[idx].Value.Value.(*hujson.Object)
	if !isObject {
		return nil
	}
	return rawUDPSites(sites)
}

func rawObject(path string) (*hujson.Object, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, false
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	return object, ok
}

func rawReservationMember(object *hujson.Object, key, subjectField string) (map[string]string, []string) {
	idx := lastFoldedMember(object, key)
	if idx < 0 {
		return nil, nil
	}
	entryObject, ok := object.Members[idx].Value.Value.(*hujson.Object)
	if !ok {
		return nil, nil
	}
	entries := make(map[string]string, len(entryObject.Members))
	var owners []string
	for i := range entryObject.Members {
		entryKey, isLiteral := literalName(entryObject.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		subject, owner := rawSubject(entryObject.Members[i].Value.Pack(), subjectField)
		if owner != "" {
			owners = append(owners, owner)
		}
		entries[entryKey] = subject
	}
	return entries, owners
}

func rawSubject(packed []byte, subjectField string) (string, string) {
	var subject string
	if err := json.Unmarshal(packed, &subject); err == nil {
		return subject, ""
	}
	// Match Entry and ServiceEntry: encoding/json prefers an exact field name
	// and otherwise accepts one case-insensitive match. A map lookup misses
	// "Site" or "Service", so a sanitizer would print the private name.
	subject = foldedStringField(packed, subjectField)
	var metadata struct {
		Owner string `json:"owner"`
	}
	_ = json.Unmarshal(packed, &metadata)
	return subject, metadata.Owner
}

func foldedStringField(packed []byte, field string) string {
	switch field {
	case "site":
		var decoded struct {
			Value string `json:"site"`
		}
		if err := json.Unmarshal(packed, &decoded); err != nil {
			return ""
		}
		return decoded.Value
	case "service":
		var decoded struct {
			Value string `json:"service"`
		}
		if err := json.Unmarshal(packed, &decoded); err != nil {
			return ""
		}
		return decoded.Value
	default:
		return ""
	}
}

func lastFoldedMember(object *hujson.Object, key string) int {
	idx := -1
	for i := range object.Members {
		name, isLiteral := literalName(object.Members[i].Name.Value)
		if isLiteral && strings.EqualFold(name, key) {
			idx = i
		}
	}
	return idx
}

func rawUDPSites(sites *hujson.Object) []registry.UDPPortEntry {
	var entries []registry.UDPPortEntry
	for i := range sites.Members {
		rawSite, nameOK := literalName(sites.Members[i].Name.Value)
		if !nameOK {
			continue
		}
		ports, portsOK := sites.Members[i].Value.Value.(*hujson.Object)
		if !portsOK || len(ports.Members) == 0 {
			entries = append(entries, registry.UDPPortEntry{Site: rawSite})
			continue
		}
		entries = append(entries, rawUDPPorts(rawSite, ports)...)
	}
	return entries
}

func rawUDPPorts(rawSite string, ports *hujson.Object) []registry.UDPPortEntry {
	entries := make([]registry.UDPPortEntry, 0, len(ports.Members))
	for i := range ports.Members {
		rawPort, portOK := literalName(ports.Members[i].Name.Value)
		if !portOK {
			continue
		}
		var device string
		_ = json.Unmarshal(ports.Members[i].Value.Pack(), &device)
		entries = append(entries, registry.UDPPortEntry{Site: rawSite, Port: rawPort, Device: device})
	}
	return entries
}
