package registry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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
