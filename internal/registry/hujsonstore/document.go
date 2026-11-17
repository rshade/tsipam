package hujsonstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/rshade/tsipam/internal/registry"
)

// document is the in-memory registry. Canonical maps may be partial when the
// matching *Err is set; accessors hide that partial view, deletes do not.
type document struct {
	siteIDs                 map[string]registry.Entry
	subnetPools             map[string]registry.Entry
	serviceAddresses        map[string]registry.ServiceEntry
	udpPorts                map[string]map[string]string
	rawKeys                 map[string]string
	subnetPoolRawKeys       map[string]string
	serviceAddressRawKeys   map[string]string
	udpPortSiteRawKeys      map[string]string
	udpPortRawKeys          map[string]map[string]string
	siteIDsPresent          bool
	subnetPoolsPresent      bool
	serviceAddressesPresent bool
	udpPortsPresent         bool
	udpPortSitePresent      map[string]bool
	siteIDsErr              error
	subnetPoolsErr          error
	serviceAddressesErr     error
	udpPortsErr             error
}

func (doc *document) counts() registry.Counts {
	return registry.Counts{
		SiteIDs:          len(doc.siteIDs),
		SubnetPools:      len(doc.subnetPools),
		ServiceAddresses: len(doc.serviceAddresses),
		UDPPorts:         doc.udpPortCount(),
	}
}

func (doc *document) udpPortCount() int {
	count := 0
	for _, ports := range doc.udpPorts {
		count += len(ports)
	}
	return count
}

func (doc *document) scopedError(kind registry.MapKind) error {
	switch kind {
	case registry.MapSiteIDs:
		return doc.siteIDsErr
	case registry.MapSubnetPools:
		return doc.subnetPoolsErr
	case registry.MapServiceAddresses:
		return doc.serviceAddressesErr
	case registry.MapUDPPorts:
		return doc.udpPortsErr
	default:
		return errors.New("unknown registry map")
	}
}

func (doc *document) topLevelError(topLevel string) error {
	switch topLevel {
	case siteIDsKey:
		return doc.siteIDsErr
	case subnetPoolsKey:
		return doc.subnetPoolsErr
	case serviceAddressesKey:
		return doc.serviceAddressesErr
	case udpPortsKey:
		return doc.udpPortsErr
	default:
		return fmt.Errorf("unknown registry map %q", topLevel)
	}
}

type mapView struct {
	hasKey  func(string) bool
	rawKeys map[string]string
	err     error
	hasView bool
	present bool
}

func (doc *document) view(topLevel string) (mapView, error) {
	switch topLevel {
	case siteIDsKey:
		return mapView{
			hasKey:  func(key string) bool { _, ok := doc.siteIDs[key]; return ok },
			rawKeys: doc.rawKeys,
			err:     doc.siteIDsErr,
			hasView: doc.siteIDs != nil,
			present: doc.siteIDsPresent,
		}, nil
	case subnetPoolsKey:
		return mapView{
			hasKey:  func(key string) bool { _, ok := doc.subnetPools[key]; return ok },
			rawKeys: doc.subnetPoolRawKeys,
			err:     doc.subnetPoolsErr,
			hasView: doc.subnetPools != nil,
			present: doc.subnetPoolsPresent,
		}, nil
	case serviceAddressesKey:
		return mapView{
			hasKey:  func(key string) bool { _, ok := doc.serviceAddresses[key]; return ok },
			rawKeys: doc.serviceAddressRawKeys,
			err:     doc.serviceAddressesErr,
			hasView: doc.serviceAddresses != nil,
			present: doc.serviceAddressesPresent,
		}, nil
	default:
		return mapView{}, fmt.Errorf("unknown registry map %q", topLevel)
	}
}

func loadBytes(raw []byte) (*document, error) {
	// hujson.Parse keeps the caller's buffer, and Standardize rewrites it.
	// Cloning keeps the pending file bytes, including comments, intact.
	raw = bytes.Clone(raw)
	if bytes.HasPrefix(raw, []byte(utf8BOM)) {
		return nil, errors.New("file starts with a UTF-8 byte-order mark; re-save it as UTF-8 without a BOM")
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf(decodingRegistryPrefix, err)
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("decoding registry: top-level document must be a JSON object")
	}
	doc := &document{}
	doc.loadSiteIDs(object)
	doc.loadSubnetPools(object)
	doc.loadServiceAddresses(object)
	doc.loadUDPPorts(object)
	return doc, nil
}

func (doc *document) loadSiteIDs(object *hujson.Object) {
	entries, present, err := decodeMapMember[registry.Entry](object, siteIDsKey)
	if err != nil {
		doc.siteIDsErr = fmt.Errorf(decodingRegistryPrefix, err)
		return
	}
	doc.siteIDsPresent = present
	ids, rawKeys, canonErr := canonicalizeSiteIDs(entries)
	if canonErr != nil {
		doc.siteIDsErr = fmt.Errorf(decodingRegistryPrefix, canonErr)
		return
	}
	doc.siteIDs = ids
	doc.rawKeys = rawKeys
}

func (doc *document) loadSubnetPools(object *hujson.Object) {
	entries, present, err := decodeMapMember[registry.Entry](object, subnetPoolsKey)
	if err != nil {
		doc.subnetPoolsErr = fmt.Errorf(decodingRegistryPrefix, err)
		return
	}
	pools, rawKeys, canonErr := canonicalizeSubnetPools(entries)
	doc.subnetPools = pools
	doc.subnetPoolRawKeys = rawKeys
	doc.subnetPoolsPresent = present
	if canonErr != nil {
		doc.subnetPoolsErr = fmt.Errorf(decodingRegistryPrefix, canonErr)
	}
}

func (doc *document) loadServiceAddresses(object *hujson.Object) {
	entries, present, err := decodeMapMember[registry.ServiceEntry](object, serviceAddressesKey)
	if err != nil {
		doc.serviceAddressesErr = fmt.Errorf(decodingRegistryPrefix, err)
		return
	}
	addresses, rawKeys, canonErr := canonicalizeServiceAddresses(entries)
	doc.serviceAddresses = addresses
	doc.serviceAddressRawKeys = rawKeys
	doc.serviceAddressesPresent = present
	if canonErr != nil {
		doc.serviceAddressesErr = fmt.Errorf(decodingRegistryPrefix, canonErr)
	}
}

func decodeMapMember[V any](object *hujson.Object, key string) (map[string]V, bool, error) {
	idx, err := memberIndex(object, key)
	if err != nil {
		return nil, false, err
	}
	if idx < 0 {
		return nil, false, nil
	}
	member := object.Members[idx].Value
	present := false
	entryObject, isObject := member.Value.(*hujson.Object)
	if isObject {
		present = true
		if dupErr := duplicateKey(entryObject, key); dupErr != nil {
			return nil, false, dupErr
		}
	}
	var entries map[string]V
	if unmarshalErr := json.Unmarshal(member.Pack(), &entries); unmarshalErr != nil {
		return nil, false, fmt.Errorf("%q: %w", key, unmarshalErr)
	}
	return entries, present, nil
}

func memberIndex(object *hujson.Object, key string) (int, error) {
	var spellings []string
	idx := -1
	for i := range object.Members {
		name, isLiteral := literalName(object.Members[i].Name.Value)
		if !isLiteral || !strings.EqualFold(name, key) {
			continue
		}
		spellings = append(spellings, name)
		idx = i
	}
	switch len(spellings) {
	case 0:
		return -1, nil
	case 1:
		if spellings[0] != key {
			return -1, fmt.Errorf("top-level key %q must be spelled %q", spellings[0], key)
		}
		return idx, nil
	default:
		quoted := make([]string, len(spellings))
		for i, spelling := range spellings {
			quoted[i] = strconv.Quote(spelling)
		}
		return -1, fmt.Errorf("multiple top-level keys match %q: %s", key, strings.Join(quoted, ", "))
	}
}

func duplicateKey(object *hujson.Object, key string) error {
	seen := make(map[string]struct{}, len(object.Members))
	for i := range object.Members {
		name, isLiteral := literalName(object.Members[i].Name.Value)
		if !isLiteral {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate %s entry key %q", key, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func literalName(value any) (string, bool) {
	literal, ok := value.(hujson.Literal)
	if !ok {
		return "", false
	}
	return literal.String(), true
}

func hujsonNull(value hujson.Value) bool {
	var decoded any
	return json.Unmarshal(value.Pack(), &decoded) == nil && decoded == nil
}
