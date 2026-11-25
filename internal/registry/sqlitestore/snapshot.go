package sqlitestore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strconv"

	"go4.org/netipx"

	"github.com/rshade/tsacl/pkg/ipcalc"

	"github.com/rshade/tsipam/internal/registry"
)

const (
	familyIPv4 = 4
	familyIPv6 = 6
)

// snapshot reads one tailnet inside a transaction. Other programs can write
// the database, so every row is validated as if it had been read from a
// hand-edited file, and an invalid row makes its whole map an error.
type snapshot struct {
	ctx       context.Context
	tx        *sql.Tx
	tailnetID int64
}

func (s *snapshot) require(kinds []registry.MapKind) error {
	for _, kind := range kinds {
		var err error
		switch kind {
		case registry.MapSiteIDs:
			_, err = s.SiteIDs()
		case registry.MapSubnetPools:
			_, err = s.SubnetPools()
		case registry.MapServiceAddresses:
			_, err = s.ServiceAddresses()
		case registry.MapUDPPorts:
			_, err = s.UDPPorts()
		default:
			err = errors.New("unknown registry map")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *snapshot) SiteIDs() (map[string]registry.Entry, error) {
	entries := map[string]registry.Entry{}
	err := s.each(`SELECT site_id, site, owner, description, ticket, reserved_at
		FROM site_ids WHERE tailnet_id = ?`, func(rows *sql.Rows) error {
		var (
			key      int64
			entry    registry.Entry
			metadata nullMetadata
		)
		if err := rows.Scan(append([]any{&key, &entry.Site}, metadata.targets()...)...); err != nil {
			return err
		}
		entry.ReservationMetadata = metadata.value()
		id, err := registry.NormalizeSiteID(strconv.FormatInt(key, 10))
		if err != nil {
			return invalidKey("site ID", strconv.FormatInt(key, 10), err)
		}
		if err = validateEntry(entry); err != nil {
			return fmt.Errorf("site ID %s: %w", id, err)
		}
		entries[id] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *snapshot) SubnetPools() (map[string]registry.Entry, error) {
	entries := map[string]registry.Entry{}
	err := s.each(`SELECT cidr, family, range_start, range_end, site, owner, description, ticket, reserved_at
		FROM subnet_pools WHERE tailnet_id = ?`, func(rows *sql.Rows) error {
		var (
			key      string
			stored   poolRange
			entry    registry.Entry
			metadata nullMetadata
		)
		if err := rows.Scan(append(
			[]any{&key, &stored.family, &stored.start, &stored.end, &entry.Site}, metadata.targets()...,
		)...); err != nil {
			return err
		}
		entry.ReservationMetadata = metadata.value()
		cidr, err := registry.NormalizeSubnetCIDR(key)
		if err == nil && cidr != key {
			err = errors.New("key is not canonical")
		}
		if err != nil {
			return invalidKey("subnet pool", key, err)
		}
		want, err := rangeOf(cidr)
		if err != nil {
			return invalidKey("subnet pool", key, err)
		}
		if !stored.equal(want) {
			return fmt.Errorf("subnet CIDR %s: stored address range does not match the CIDR", cidr)
		}
		if err = validateEntry(entry); err != nil {
			return fmt.Errorf("subnet CIDR %s: %w", cidr, err)
		}
		entries[cidr] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *snapshot) ServiceAddresses() (map[string]registry.ServiceEntry, error) {
	entries := map[string]registry.ServiceEntry{}
	err := s.each(`SELECT address, service, owner, description, ticket, reserved_at
		FROM service_addresses WHERE tailnet_id = ?`, func(rows *sql.Rows) error {
		var (
			key      string
			entry    registry.ServiceEntry
			metadata nullMetadata
		)
		if err := rows.Scan(append([]any{&key, &entry.Service}, metadata.targets()...)...); err != nil {
			return err
		}
		entry.ReservationMetadata = metadata.value()
		address, err := registry.NormalizeServiceAddress(key)
		if err == nil && address != key {
			err = errors.New("key is not canonical")
		}
		if err != nil {
			return invalidKey("service address", key, err)
		}
		if err = validateLabel(entry.Service, registry.NormalizeServiceName); err != nil {
			return fmt.Errorf("service address %s: %w", address, err)
		}
		if err = validateMetadata(entry.ReservationMetadata); err != nil {
			return fmt.Errorf("service address %s: %w", address, err)
		}
		entries[address] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *snapshot) UDPPorts() (map[string]map[string]string, error) {
	entries := map[string]map[string]string{}
	err := s.each(`SELECT site, port, device FROM udp_ports WHERE tailnet_id = ?`, func(rows *sql.Rows) error {
		var (
			site, device string
			key          int64
		)
		if err := rows.Scan(&site, &key, &device); err != nil {
			return err
		}
		if err := validateLabel(site, registry.NormalizeSiteName); err != nil {
			return fmt.Errorf("UDP port site: %w", err)
		}
		port, err := registry.NormalizePort(strconv.FormatInt(key, 10))
		if err != nil {
			return invalidKey("UDP port", strconv.FormatInt(key, 10), err)
		}
		if err = validateLabel(device, registry.NormalizeDeviceName); err != nil {
			return fmt.Errorf("UDP port %s in site %q: %w", port, site, err)
		}
		if entries[site] == nil {
			entries[site] = map[string]string{}
		}
		entries[site][port] = device
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Counts reports stored rows, valid or not. It is diagnostic only, so a map
// that cannot be counted reports zero.
func (s *snapshot) Counts() registry.Counts {
	var counts registry.Counts
	_ = s.tx.QueryRowContext(s.ctx, `SELECT
		(SELECT count(*) FROM site_ids WHERE tailnet_id = ?1),
		(SELECT count(*) FROM subnet_pools WHERE tailnet_id = ?1),
		(SELECT count(*) FROM service_addresses WHERE tailnet_id = ?1),
		(SELECT count(*) FROM udp_ports WHERE tailnet_id = ?1)`, s.tailnetID,
	).Scan(&counts.SiteIDs, &counts.SubnetPools, &counts.ServiceAddresses, &counts.UDPPorts)
	return counts
}

func (s *snapshot) each(query string, scan func(*sql.Rows) error) error {
	rows, err := s.tx.QueryContext(s.ctx, query, s.tailnetID)
	if err != nil {
		return describe(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err = scan(rows); err != nil {
			return err
		}
	}
	return describe(rows.Err())
}

// nullMetadata scans the four optional metadata columns; NULL is unset.
type nullMetadata struct {
	owner, description, ticket, reservedAt sql.NullString
}

func (m *nullMetadata) targets() []any {
	return []any{&m.owner, &m.description, &m.ticket, &m.reservedAt}
}

func (m *nullMetadata) value() registry.ReservationMetadata {
	return registry.ReservationMetadata{
		Owner:       m.owner.String,
		Description: m.description.String,
		Ticket:      m.ticket.String,
		ReservedAt:  m.reservedAt.String,
	}
}

// poolRange is a pool's first and last address in 16-byte form.
type poolRange struct {
	family     int
	start, end []byte
}

func (r poolRange) equal(other poolRange) bool {
	return r.family == other.family && bytes.Equal(r.start, other.start) && bytes.Equal(r.end, other.end)
}

// rangeOf derives the range columns from a CIDR. An IPv4-mapped IPv6 pool is
// the IPv4 pool it maps, because the registry rules treat the two as
// overlapping.
func rangeOf(cidr string) (poolRange, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return poolRange{}, err
	}
	if unmapped, ok := ipcalc.Unmap4In6(prefix); ok {
		prefix = unmapped
	}
	family := familyIPv6
	if prefix.Addr().Is4() {
		family = familyIPv4
	}
	addresses := netipx.RangeOfPrefix(prefix)
	start, end := addresses.From().As16(), addresses.To().As16()
	return poolRange{family: family, start: start[:], end: end[:]}, nil
}

func validateEntry(entry registry.Entry) error {
	if err := validateLabel(entry.Site, registry.NormalizeSiteName); err != nil {
		return err
	}
	return validateMetadata(entry.ReservationMetadata)
}

func validateMetadata(metadata registry.ReservationMetadata) error {
	return registry.StoredStateError(registry.ValidateStoredReservationMetadata(metadata))
}

func validateLabel(stored string, normalize func(string) (string, error)) error {
	canonical, err := normalize(stored)
	if err != nil {
		return registry.StoredStateError(err)
	}
	if canonical != stored {
		return fmt.Errorf("stored name %q is not canonical", stored)
	}
	return nil
}

func invalidKey(kind, key string, err error) error {
	return fmt.Errorf("invalid %s key %q: %w", kind, key, registry.StoredStateError(err))
}
