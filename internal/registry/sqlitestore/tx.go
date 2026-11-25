package sqlitestore

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/rshade/tsipam/internal/registry"
)

// writeTx mutates one tailnet inside an Update. Each mutation writes its audit
// event in the same transaction.
type writeTx struct {
	snapshot

	opts  registry.TxOptions
	actor string
}

func (t *writeTx) PutSiteID(id string, entry registry.Entry) error {
	entries, err := t.SiteIDs()
	if err != nil {
		return err
	}
	prior, exists := entries[id]
	query := `INSERT INTO site_ids (site, owner, description, ticket, reserved_at, tailnet_id, site_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if exists {
		query = `UPDATE site_ids SET site = ?, owner = ?, description = ?, ticket = ?, reserved_at = ?
			WHERE tailnet_id = ? AND site_id = ?`
	}
	args := append([]any{entry.Site}, metadataArgs(entry.ReservationMetadata)...)
	if err = t.exec(query, append(args, t.tailnetID, id)...); err != nil {
		return err
	}
	return t.record(registry.Event{
		Kind: registry.MapSiteIDs, Key: id,
		Before: entryValue(prior, exists), After: entryValue(entry, true),
	}, registry.ActionReserve)
}

func (t *writeTx) PutSubnetPool(cidr string, entry registry.Entry) error {
	entries, err := t.SubnetPools()
	if err != nil {
		return err
	}
	prior, exists := entries[cidr]
	args := append([]any{entry.Site}, metadataArgs(entry.ReservationMetadata)...)
	args = append(args, t.tailnetID, cidr)
	query := `UPDATE subnet_pools SET site = ?, owner = ?, description = ?, ticket = ?, reserved_at = ?
		WHERE tailnet_id = ? AND cidr = ?`
	if !exists {
		pool, rangeErr := rangeOf(cidr)
		if rangeErr != nil {
			return rangeErr
		}
		query = `INSERT INTO subnet_pools
			(site, owner, description, ticket, reserved_at, tailnet_id, cidr, family, range_start, range_end)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		args = append(args, pool.family, pool.start, pool.end)
	}
	if err = t.exec(query, args...); err != nil {
		return err
	}
	return t.record(registry.Event{
		Kind: registry.MapSubnetPools, Key: cidr,
		Before: entryValue(prior, exists), After: entryValue(entry, true),
	}, registry.ActionReserve)
}

func (t *writeTx) PutServiceAddress(address string, entry registry.ServiceEntry) error {
	entries, err := t.ServiceAddresses()
	if err != nil {
		return err
	}
	prior, exists := entries[address]
	query := `INSERT INTO service_addresses (service, owner, description, ticket, reserved_at, tailnet_id, address)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if exists {
		query = `UPDATE service_addresses SET service = ?, owner = ?, description = ?, ticket = ?, reserved_at = ?
			WHERE tailnet_id = ? AND address = ?`
	}
	args := append([]any{entry.Service}, metadataArgs(entry.ReservationMetadata)...)
	if err = t.exec(query, append(args, t.tailnetID, address)...); err != nil {
		return err
	}
	return t.record(registry.Event{
		Kind: registry.MapServiceAddresses, Key: address,
		Before: serviceValue(prior, exists), After: serviceValue(entry, true),
	}, registry.ActionReserve)
}

func (t *writeTx) PutUDPPort(site, port, device string) error {
	entries, err := t.UDPPorts()
	if err != nil {
		return err
	}
	prior, exists := entries[site][port]
	query := `INSERT INTO udp_ports (device, tailnet_id, site, port) VALUES (?, ?, ?, ?)`
	if exists {
		query = `UPDATE udp_ports SET device = ? WHERE tailnet_id = ? AND site = ? AND port = ?`
	}
	if err = t.exec(query, device, t.tailnetID, site, port); err != nil {
		return err
	}
	return t.record(registry.Event{
		Kind: registry.MapUDPPorts, Key: port, Site: site,
		Before: deviceValue(prior, exists), After: deviceValue(device, true),
	}, registry.ActionReserve)
}

func (t *writeTx) DeleteSiteID(id string) (bool, error) {
	entries, err := t.SiteIDs()
	if err != nil {
		return false, undetermined("site ID "+id, err)
	}
	prior, exists := entries[id]
	if !exists {
		return false, nil
	}
	if err = t.exec(`DELETE FROM site_ids WHERE tailnet_id = ? AND site_id = ?`, t.tailnetID, id); err != nil {
		return false, err
	}
	return t.recordRelease(registry.Event{
		Kind: registry.MapSiteIDs, Key: id, Before: entryValue(prior, true),
	})
}

func (t *writeTx) DeleteSubnetPool(cidr string) (bool, error) {
	entries, err := t.SubnetPools()
	if err != nil {
		return false, undetermined("subnet CIDR "+cidr, err)
	}
	prior, exists := entries[cidr]
	if !exists {
		return false, nil
	}
	if err = t.exec(`DELETE FROM subnet_pools WHERE tailnet_id = ? AND cidr = ?`, t.tailnetID, cidr); err != nil {
		return false, err
	}
	return t.recordRelease(registry.Event{
		Kind: registry.MapSubnetPools, Key: cidr, Before: entryValue(prior, true),
	})
}

func (t *writeTx) DeleteServiceAddress(address string) (bool, error) {
	entries, err := t.ServiceAddresses()
	if err != nil {
		return false, undetermined("service address "+address, err)
	}
	prior, exists := entries[address]
	if !exists {
		return false, nil
	}
	if err = t.exec(
		`DELETE FROM service_addresses WHERE tailnet_id = ? AND address = ?`, t.tailnetID, address,
	); err != nil {
		return false, err
	}
	return t.recordRelease(registry.Event{
		Kind: registry.MapServiceAddresses, Key: address, Before: serviceValue(prior, true),
	})
}

func (t *writeTx) DeleteUDPPort(site, port string) (bool, error) {
	entries, err := t.UDPPorts()
	if err != nil {
		return false, undetermined(fmt.Sprintf("UDP port %s in site %q", port, site), err)
	}
	prior, exists := entries[site][port]
	if !exists {
		return false, nil
	}
	if err = t.exec(
		`DELETE FROM udp_ports WHERE tailnet_id = ? AND site = ? AND port = ?`, t.tailnetID, site, port,
	); err != nil {
		return false, err
	}
	return t.recordRelease(registry.Event{
		Kind: registry.MapUDPPorts, Key: port, Site: site, Before: deviceValue(prior, true),
	})
}

// undetermined is the fail-closed delete error. The Ledger recognizes its
// prefix and leaves the text alone.
func undetermined(subject string, cause error) error {
	return fmt.Errorf("cannot determine whether %s is reserved: %w", subject, cause)
}

func (t *writeTx) exec(query string, args ...any) error {
	_, err := t.tx.ExecContext(t.ctx, query, args...)
	return describe(err)
}

func (t *writeTx) recordRelease(event registry.Event) (bool, error) {
	if err := t.record(event, registry.ActionRelease); err != nil {
		return false, err
	}
	return true, nil
}

func (t *writeTx) record(event registry.Event, fallback registry.Action) error {
	before, err := encodeValue(event.Before)
	if err != nil {
		return err
	}
	after, err := encodeValue(event.After)
	if err != nil {
		return err
	}
	return t.exec(`INSERT INTO events
		(tailnet_id, at, action, kind, key, site, before_value, after_value, actor)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.tailnetID, t.opts.EventTime(), string(t.opts.EventAction(fallback)), event.Kind.String(), event.Key,
		nullable(event.Site), before, after, nullable(t.actor),
	)
}

func entryValue(entry registry.Entry, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: entry.Site, ReservationMetadata: entry.ReservationMetadata}
}

func serviceValue(entry registry.ServiceEntry, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: entry.Service, ReservationMetadata: entry.ReservationMetadata}
}

func deviceValue(device string, exists bool) *registry.EventValue {
	if !exists {
		return nil
	}
	return &registry.EventValue{Target: device}
}

// storedValue is the JSON form of an event's before and after values.
type storedValue struct {
	Target      string `json:"target"`
	Owner       string `json:"owner,omitempty"`
	Description string `json:"description,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	ReservedAt  string `json:"reservedAt,omitempty"`
}

func encodeValue(value *registry.EventValue) (sql.NullString, error) {
	if value == nil {
		return sql.NullString{}, nil
	}
	encoded, err := json.Marshal(storedValue{
		Target: value.Target, Owner: value.Owner, Description: value.Description,
		Ticket: value.Ticket, ReservedAt: value.ReservedAt,
	})
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(encoded), Valid: true}, nil
}

func decodeValue(stored sql.NullString) (*registry.EventValue, error) {
	if !stored.Valid {
		return nil, nil //nolint:nilnil // a NULL column is an absent value, not an error
	}
	var value storedValue
	if err := json.Unmarshal([]byte(stored.String), &value); err != nil {
		return nil, err
	}
	return &registry.EventValue{
		Target: value.Target,
		Owner:  value.Owner, Description: value.Description, Ticket: value.Ticket, ReservedAt: value.ReservedAt,
	}, nil
}

func metadataArgs(metadata registry.ReservationMetadata) []any {
	return []any{
		nullable(metadata.Owner), nullable(metadata.Description),
		nullable(metadata.Ticket), nullable(metadata.ReservedAt),
	}
}

func nullable(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
