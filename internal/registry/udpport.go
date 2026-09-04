package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/rshade/tailscale-utils/policycore/hujsonedit"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

const (
	minUDPPort = 1024
	maxUDPPort = 65535
)

// UDPPortEntry is one resolved per-device UDP listen-port reservation.
type UDPPortEntry struct {
	Site   string
	Port   string
	Device string
}

// UDPPortCollisionError reports that a UDP port is already reserved for a
// different device within the same site/firewall boundary.
type UDPPortCollisionError struct {
	Site     string
	Port     string
	Existing string
	Wanted   string
}

func (e *UDPPortCollisionError) Error() string {
	return fmt.Sprintf(
		"UDP port %s in site %q is already reserved for %q, not %q",
		e.Port,
		e.Site,
		e.Existing,
		e.Wanted,
	)
}

// NormalizePort validates and canonicalizes a UDP listen port. Only ASCII
// base-10 digits are accepted; leading zeroes are removed in the result.
func NormalizePort(port string) (string, error) {
	port = strings.TrimSpace(port)
	if port == "" {
		return "", invalidInput(errors.New("UDP port must not be empty"))
	}
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return "", invalidInput(fmt.Errorf("UDP port %q is not a decimal integer", port))
		}
	}
	n, err := strconv.ParseUint(port, 10, 32)
	if err != nil || n < minUDPPort || n > maxUDPPort {
		return "", invalidInput(fmt.Errorf(
			"UDP port %q is out of range: valid ports are %d-%d",
			port,
			minUDPPort,
			maxUDPPort,
		))
	}
	return strconv.FormatUint(n, 10), nil
}

// NormalizeDeviceName validates a device label without imposing a hostname or
// live-tailnet grammar: it must be non-empty after trimming, contain no
// control characters, and be valid UTF-8.
func NormalizeDeviceName(name string) (string, error) {
	return normalizeLabel("device name", name)
}

// ReserveUDPPortWithLogger reserves an explicit UDP listen port for a device
// within a site, creating the registry file when needed. The complete
// load/collision/patch transaction is protected by the registry lock.
func ReserveUDPPortWithLogger(
	path, site, port, device string,
	logger diagnostics.Logger,
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

	changed := false
	lockErr := withRegistryLock(path, logger, func() error {
		reg, loadErr := loadOrCreate(path, udpPortsKey, logger)
		if loadErr != nil {
			return loadErr
		}
		existing, exists := reg.UDPPorts[site][port]
		if exists && existing != device {
			return &UDPPortCollisionError{
				Site: site, Port: port, Existing: existing, Wanted: device,
			}
		}
		if exists {
			diagnostics.Debug(logger, "UDP-port reservation already present")
			return nil
		}

		op := reserveUDPPortPatchOp(reg, site, port, device)
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("reserving UDP port %s: %w", port, patchErr)
		}
		changed = true
		diagnostics.Debug(logger, "UDP-port reservation written")
		return nil
	})
	if lockErr != nil {
		return "", "", "", false, lockErr
	}
	return site, port, device, changed, nil
}

// reserveUDPPortPatchOp selects the narrowest safe add for the nested
// udpPorts/site/port shape. Presence and raw spellings come from Load rather
// than being inferred from nil maps.
func reserveUDPPortPatchOp(
	reg *Registry,
	site, port, device string,
) hujsonedit.PatchOperation {
	if !reg.udpPortsPresent {
		return hujsonedit.PatchAdd(
			map[string]map[string]string{site: {port: device}},
			udpPortsKey,
		)
	}
	rawSite := reg.udpPortSiteRawKeys[site]
	if rawSite == "" {
		return hujsonedit.PatchAdd(map[string]string{port: device}, udpPortsKey, site)
	}
	if !reg.udpPortSitePresent[site] {
		return hujsonedit.PatchAdd(map[string]string{port: device}, udpPortsKey, rawSite)
	}
	rawPort := port
	if existingRaw := reg.udpPortRawKeys[site][port]; existingRaw != "" {
		rawPort = existingRaw
	}
	return hujsonedit.PatchAdd(device, udpPortsKey, rawSite, rawPort)
}

// ReleaseUDPPortWithLogger removes a site/port reservation. An absent pair in
// an existing valid registry is a no-op; the containing site object is kept
// when its last port is removed so site-level comments survive.
//
//nolint:gocognit // repair logic required for malformed maps adds unavoidable complexity
func ReleaseUDPPortWithLogger(
	path, site, port string,
	logger diagnostics.Logger,
) (string, string, bool, error) {
	site, err := NormalizeSiteName(site)
	if err != nil {
		return "", "", false, err
	}
	port, err = NormalizePort(port)
	if err != nil {
		return "", "", false, err
	}

	changed := false
	lockErr := withRegistryLock(path, logger, func() error {
		reg, loadErr := Load(path)
		if loadErr != nil {
			return loadErr
		}
		diagnostics.Debug(logger, "registry loaded",
			"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
			"service_address_count", len(reg.ServiceAddresses), "udp_port_count", reg.udpPortCount())

		if _, exists := reg.UDPPorts[site][port]; !exists {
			mapErr := reg.mapError(udpPortsKey)
			if mapErr == nil {
				diagnostics.Debug(logger, "UDP-port reservation already absent")
				return nil
			}
			// Map has an error and no usable view; attempt best-effort repair.
			repaired, repairErr := repairMalformedUDPPort(path, site, port)
			if repairErr != nil {
				return repairErr
			}
			if repaired {
				changed = true
				diagnostics.Debug(logger, "UDP-port reservation removed by raw-key repair")
				return nil
			}
			return fmt.Errorf("cannot determine whether UDP port %s in site %q is reserved: %w", port, site, mapErr)
		}

		rawSite := reg.udpPortSiteRawKeys[site]
		if rawSite == "" {
			rawSite = site
		}
		rawPort := reg.udpPortRawKeys[site][port]
		if rawPort == "" {
			rawPort = port
		}
		if patchErr := hujsonedit.WritePatch(
			path,
			nil,
			hujsonedit.PatchRemove(udpPortsKey, rawSite, rawPort),
		); patchErr != nil {
			return fmt.Errorf("releasing UDP port %s: %w", port, patchErr)
		}
		changed = true
		diagnostics.Debug(logger, "UDP-port reservation removed")
		return nil
	})
	if lockErr != nil {
		return "", "", false, lockErr
	}
	return site, port, changed, nil
}

// ListUDPPortsWithLogger returns all UDP-port reservations, or the selected
// site's reservations when site is non-empty, sorted by site and numeric port.
func ListUDPPortsWithLogger(
	path, site string,
	logger diagnostics.Logger,
) ([]UDPPortEntry, error) {
	if site != "" {
		var err error
		site, err = NormalizeSiteName(site)
		if err != nil {
			return nil, err
		}
	}
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if mapErr := reg.mapError(udpPortsKey); mapErr != nil {
		return nil, mapErr
	}
	diagnostics.Debug(logger, "registry loaded",
		"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
		"service_address_count", len(reg.ServiceAddresses), "udp_port_count", reg.udpPortCount())

	entries := make([]UDPPortEntry, 0, reg.udpPortCount())
	for entrySite, ports := range reg.UDPPorts {
		if site != "" && entrySite != site {
			continue
		}
		for entryPort, entryDevice := range ports {
			entries = append(entries, UDPPortEntry{Site: entrySite, Port: entryPort, Device: entryDevice})
		}
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

// RawUDPPorts best-effort extracts raw site, port, and device spellings for
// error-sanitizer inventory. It deliberately does not validate: values from a
// malformed map are exactly the ones that may surface in a load error.
func RawUDPPorts(path string) []UDPPortEntry {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil
	}

	idx := -1
	for i := range object.Members {
		name, nameOK := literalName(object.Members[i].Name.Value)
		if nameOK && strings.EqualFold(name, udpPortsKey) {
			idx = i
		}
	}
	if idx < 0 {
		return nil
	}
	sites, ok := object.Members[idx].Value.Value.(*hujson.Object)
	if !ok {
		return nil
	}

	var entries []UDPPortEntry
	for i := range sites.Members {
		rawSite, nameOK := literalName(sites.Members[i].Name.Value)
		if !nameOK {
			continue
		}
		ports, portsOK := sites.Members[i].Value.Value.(*hujson.Object)
		if !portsOK || len(ports.Members) == 0 {
			entries = append(entries, UDPPortEntry{Site: rawSite})
			continue
		}
		for j := range ports.Members {
			rawPort, portOK := literalName(ports.Members[j].Name.Value)
			if !portOK {
				continue
			}
			var device string
			_ = json.Unmarshal(ports.Members[j].Value.Pack(), &device)
			entries = append(entries, UDPPortEntry{Site: rawSite, Port: rawPort, Device: device})
		}
	}
	return entries
}

// loadUDPPorts populates the nested udpPorts view or records an error scoped
// to that map. It walks HuJSON object members directly so duplicate raw keys
// are visible and so both levels' exact on-file spellings and object-presence
// states survive canonicalization.
func (reg *Registry) loadUDPPorts(object *hujson.Object) {
	idx, err := registryMemberIndex(object, udpPortsKey)
	if err != nil {
		reg.udpPortsErr = fmt.Errorf("decoding registry: %w", err)
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
		reg.udpPortsErr = errors.New("decoding registry: \"udpPorts\" must be an object or null")
		return
	}

	reg.udpPortsPresent = true
	reg.UDPPorts = make(map[string]map[string]string, len(sites.Members))
	reg.udpPortSiteRawKeys = make(map[string]string, len(sites.Members))
	reg.udpPortRawKeys = make(map[string]map[string]string, len(sites.Members))
	reg.udpPortSitePresent = make(map[string]bool, len(sites.Members))
	reg.udpPortsErr = reg.loadUDPPortSites(sites)
}

func (reg *Registry) loadUDPPortSites(sites *hujson.Object) error {
	rawSites := make(map[string]struct{}, len(sites.Members))
	for i := range sites.Members {
		if err := reg.loadUDPPortSite(sites.Members[i], rawSites); err != nil {
			return err
		}
	}
	return nil
}

func (reg *Registry) loadUDPPortSite(member hujson.ObjectMember, rawSites map[string]struct{}) error {
	rawSite, nameOK := literalName(member.Name.Value)
	if !nameOK {
		return nil
	}
	if _, duplicate := rawSites[rawSite]; duplicate {
		return fmt.Errorf("decoding registry: duplicate udpPorts site key %q", rawSite)
	}
	rawSites[rawSite] = struct{}{}

	site, err := NormalizeSiteName(rawSite)
	if err != nil {
		return fmt.Errorf(
			"decoding registry: invalid UDP-port site key %q: %w",
			rawSite,
			storedStateError(err),
		)
	}
	if firstRaw, duplicate := reg.udpPortSiteRawKeys[site]; duplicate {
		return fmt.Errorf(
			"decoding registry: UDP-port site is defined twice, as %q and %q",
			firstRaw,
			rawSite,
		)
	}
	reg.udpPortSiteRawKeys[site] = rawSite

	ports, isObject := member.Value.Value.(*hujson.Object)
	if !isObject {
		if hujsonNull(member.Value) {
			reg.UDPPorts[site] = nil
			return nil
		}
		return fmt.Errorf("decoding registry: UDP-port site %q must be an object or null", rawSite)
	}
	reg.udpPortSitePresent[site] = true
	reg.UDPPorts[site] = make(map[string]string, len(ports.Members))
	reg.udpPortRawKeys[site] = make(map[string]string, len(ports.Members))
	return reg.loadUDPPortEntries(site, rawSite, ports)
}

func (reg *Registry) loadUDPPortEntries(site, rawSite string, ports *hujson.Object) error {
	rawPorts := make(map[string]struct{}, len(ports.Members))
	for i := range ports.Members {
		rawPort, nameOK := literalName(ports.Members[i].Name.Value)
		if !nameOK {
			continue
		}
		if _, duplicate := rawPorts[rawPort]; duplicate {
			return fmt.Errorf(
				"decoding registry: duplicate UDP-port entry key %q in site %q",
				rawPort,
				rawSite,
			)
		}
		rawPorts[rawPort] = struct{}{}

		port, err := NormalizePort(rawPort)
		if err != nil {
			return fmt.Errorf(
				"decoding registry: invalid UDP port key %q in site %q: %w",
				rawPort,
				rawSite,
				storedStateError(err),
			)
		}
		if firstRaw, duplicate := reg.udpPortRawKeys[site][port]; duplicate {
			return fmt.Errorf(
				"decoding registry: UDP port %s in site %q is defined twice, as %q and %q",
				port,
				rawSite,
				firstRaw,
				rawPort,
			)
		}

		var device string
		if err = json.Unmarshal(ports.Members[i].Value.Pack(), &device); err != nil {
			return fmt.Errorf(
				"decoding registry: device for UDP port %s in site %q must be a string: %w",
				port,
				rawSite,
				err,
			)
		}
		device, err = NormalizeDeviceName(device)
		if err != nil {
			return fmt.Errorf(
				"decoding registry: UDP port %s in site %q: %w",
				port,
				rawSite,
				storedStateError(err),
			)
		}
		reg.UDPPorts[site][port] = device
		reg.udpPortRawKeys[site][port] = rawPort
	}
	return nil
}

func hujsonNull(value hujson.Value) bool {
	var decoded any
	return json.Unmarshal(value.Pack(), &decoded) == nil && decoded == nil
}

func literalName(value any) (string, bool) {
	literal, ok := value.(hujson.Literal)
	if !ok {
		return "", false
	}
	return literal.String(), true
}

func (reg *Registry) udpPortCount() int {
	count := 0
	for _, ports := range reg.UDPPorts {
		count += len(ports)
	}
	return count
}

// repairMalformedUDPPort attempts to repair a malformed udpPorts map by
// removing a raw (site, port) pair that normalizes to the requested (site, port).
//
// The candidate is produced and judged entirely in memory: the file is read
// once, patched with hujsonedit.PatchBytes, and reloaded through loadBytes.
// It is written back only once that reload BOTH shows udpPorts' view now
// validates AND shows (site, port) absent from the rebuilt view -- see
// repairCandidateFixesUDPPort for why one without the other proves nothing.
//
// Returns whether the repair was kept, and an error only if the single write
// of an already-validated candidate fails.
func repairMalformedUDPPort(path, site, port string) (bool, error) {
	original, readErr := os.ReadFile(path)
	if readErr != nil {
		//nolint:nilerr // nothing was read, so no repair is even possible
		return false, nil
	}
	for _, rawSite := range repairRawUDPSites(original, site) {
		for _, rawPort := range repairRawUDPPorts(original, rawSite, port) {
			candidate, patchErr := hujsonedit.PatchBytes(
				original, nil, hujsonedit.PatchRemove(udpPortsKey, rawSite, rawPort),
			)
			if patchErr != nil || !repairCandidateFixesUDPPort(candidate, site, port) {
				continue
			}
			if writeErr := hujsonedit.WriteRaw(path, candidate); writeErr != nil {
				return false, fmt.Errorf(
					"repairing %s: removing site %q port %q would fix it, but writing the repaired registry failed: %w",
					udpPortsKey, rawSite, rawPort, identityFreePathError(writeErr),
				)
			}
			return true, nil
		}
	}
	return false, nil
}

// repairRawUDPSites returns every exact on-file site spelling in udpPorts
// that normalizes to site. Candidates remain in document order.
func repairRawUDPSites(document []byte, site string) []string {
	sites, ok := topLevelObjectMember(document, udpPortsKey)
	if !ok {
		return nil
	}

	rawSites := make([]string, 0, 1)
	for i := range sites.Members {
		literal, isLiteral := literalName(sites.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		canonicalSite, normalizeErr := NormalizeSiteName(literal)
		if normalizeErr == nil && canonicalSite == site {
			rawSites = append(rawSites, literal)
		}
	}
	return rawSites
}

// repairRawUDPPorts returns every exact on-file port spelling under the given
// rawSite in udpPorts that normalizes to port. Candidates remain in document order.
func repairRawUDPPorts(document []byte, rawSite, port string) []string {
	sites, ok := topLevelObjectMember(document, udpPortsKey)
	if !ok {
		return nil
	}

	var siteIdx int
	found := false
	for i := range sites.Members {
		literal, isLiteral := literalName(sites.Members[i].Name.Value)
		if isLiteral && literal == rawSite {
			siteIdx = i
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	ports, ok := sites.Members[siteIdx].Value.Value.(*hujson.Object)
	if !ok {
		return nil
	}

	rawPorts := make([]string, 0, 1)
	for i := range ports.Members {
		literal, isLiteral := literalName(ports.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		canonicalPort, normalizeErr := NormalizePort(literal)
		if normalizeErr == nil && canonicalPort == port {
			rawPorts = append(rawPorts, literal)
		}
	}
	return rawPorts
}

// repairCandidateFixesUDPPort reports whether candidate -- the in-memory result
// of removing (site, port) from udpPorts -- is worth writing: it must rebuild
// udpPorts' view without error AND leave (site, port) absent from that view.
//
// Neither condition alone is sufficient. A literal-key match proves nothing
// about which entry actually caused the failure, so a candidate that still
// fails to validate means the removed entry was a healthy sibling and the
// real culprit is elsewhere. And a clean reload proves nothing about whether
// (site, port) is still reserved: a second on-file spelling can survive the
// removal of its twin.
func repairCandidateFixesUDPPort(candidate []byte, site, port string) bool {
	reloaded, loadErr := loadBytes(candidate)
	if loadErr != nil || reloaded.mapError(udpPortsKey) != nil {
		return false
	}
	if reloaded.UDPPorts == nil {
		return true
	}
	_, exists := reloaded.UDPPorts[site][port]
	return !exists
}
