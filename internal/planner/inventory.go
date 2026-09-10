package planner

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/rshade/tailscale-utils/policycore/addrspace"
	"github.com/rshade/tailscale-utils/policycore/ipcalc"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/devices"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

type evidence struct {
	owners []string
	routes []string
}

func routeInventory(list []devices.Device) (map[netip.Prefix][]string, map[string]evidence, error) {
	routes := make(map[netip.Prefix][]string)
	sites := make(map[string]evidence)
	for deviceIndex, device := range list {
		if strings.TrimSpace(device.Name) == "" {
			return nil, nil, fmt.Errorf("live device %d has no name", deviceIndex)
		}
		for routeIndex, route := range device.AdvertisedRoutes {
			prefix, err := normalizeRoute(route)
			if err != nil {
				return nil, nil, fmt.Errorf("live device %d route %d: %w", deviceIndex, routeIndex, err)
			}
			routes[prefix] = append(routes[prefix], device.Name)
			if siteID, ok := addrspace.SiteID(prefix.String()); ok {
				key := strconv.FormatUint(uint64(siteID), 10)
				item := sites[key]
				item.owners = append(item.owners, device.Name)
				item.routes = append(item.routes, prefix.String())
				sites[key] = item
			}
		}
	}
	return routes, sites, nil
}

func normalizeRoute(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, errors.New("invalid advertised CIDR")
	}
	if normalized, ok := ipcalc.Unmap4In6(prefix); ok {
		prefix = normalized
	}
	prefix = prefix.Masked()
	if addrspace.IsViaPrefix(prefix.String()) {
		if _, ok := addrspace.SiteID(prefix.String()); !ok {
			return netip.Prefix{}, errors.New("invalid 4via6 site-ID encoding")
		}
	}
	return prefix, nil
}

func serviceInventory(list []devices.Service) (map[string][]string, error) {
	addresses := make(map[string][]string)
	for index, service := range list {
		name, err := registry.NormalizeServiceName(service.Name)
		if err != nil {
			return nil, fmt.Errorf("live service %d has an invalid name", index)
		}
		for addressIndex, raw := range service.Addresses {
			address, parseErr := netip.ParseAddr(raw)
			if parseErr != nil || address.Zone() != "" || address.Is4In6() {
				return nil, fmt.Errorf("live service %d address %d is invalid", index, addressIndex)
			}
			if address.Is4() {
				addresses[address.String()] = append(addresses[address.String()], name)
			}
		}
	}
	return addresses, nil
}
