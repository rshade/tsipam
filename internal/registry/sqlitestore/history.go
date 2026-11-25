package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

// History returns matching audit events newest first. An event another program
// wrote with an unreadable time or value fails the whole read.
func (s *Store) History(
	ctx context.Context, tailnet string, filter registry.HistoryFilter,
) ([]registry.Event, error) {
	events := []registry.Event{}
	err := s.read(ctx, tailnet, func(tx *sql.Tx, tailnetID int64) error {
		query, args := historyQuery(tailnetID, filter)
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return describe(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			event, scanErr := scanEvent(rows)
			if scanErr != nil {
				return scanErr
			}
			events = append(events, event)
		}
		return describe(rows.Err())
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func historyQuery(tailnetID int64, filter registry.HistoryFilter) (string, []any) {
	conditions := []string{"tailnet_id = ?"}
	args := []any{tailnetID}
	if filter.Kind != 0 {
		conditions = append(conditions, "kind = ?")
		args = append(args, filter.Kind.String())
	}
	if filter.Key != "" {
		conditions = append(conditions, "key = ?")
		args = append(args, filter.Key)
	}
	if !filter.Since.IsZero() {
		// An unreadable time stays in the result so that reading it fails.
		conditions = append(conditions, "coalesce(unixepoch(at) >= ?, 1)")
		args = append(args, ceilSecond(filter.Since))
	}
	query := `SELECT id, at, action, kind, key, site, before_value, after_value, actor
		FROM events WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY id DESC LIMIT ?`
	return query, append(args, filter.Limit)
}

// ceilSecond rounds up because events carry whole seconds: one stamped in the
// same second as since happened at or before it.
func ceilSecond(since time.Time) int64 {
	if since.Nanosecond() > 0 {
		return since.Unix() + 1
	}
	return since.Unix()
}

func scanEvent(rows *sql.Rows) (registry.Event, error) {
	var (
		id                         int64
		event                      registry.Event
		action, kind               string
		site, before, after, actor sql.NullString
	)
	if err := rows.Scan(&id, &event.At, &action, &kind, &event.Key, &site, &before, &after, &actor); err != nil {
		return registry.Event{}, err
	}
	event.Action = registry.Action(action)
	event.Site = site.String
	event.Actor = actor.String
	if err := fillEvent(&event, kind, before, after); err != nil {
		return registry.Event{}, fmt.Errorf("registry event %d is invalid: %w", id, registry.StoredStateError(err))
	}
	return event, nil
}

func fillEvent(event *registry.Event, kind string, before, after sql.NullString) error {
	var err error
	if event.Kind, err = registry.ParseMapKind(kind); err != nil {
		return err
	}
	if event.Before, err = decodeValue(before); err != nil {
		return err
	}
	if event.After, err = decodeValue(after); err != nil {
		return err
	}
	if _, err = time.Parse(time.RFC3339, event.At); err != nil {
		return err
	}
	return validateEventText(event)
}

// validateEventText keeps a value another program stored from reaching human
// output with control characters in it.
func validateEventText(event *registry.Event) error {
	values := []string{event.Key, event.Site, event.Actor}
	for _, value := range []*registry.EventValue{event.Before, event.After} {
		if value == nil {
			continue
		}
		if err := registry.ValidateStoredReservationMetadata(value.ReservationMetadata); err != nil {
			return err
		}
		values = append(values, value.Target)
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if err := validateLabel(value, registry.NormalizeSiteName); err != nil {
			return err
		}
	}
	return nil
}

// IdentityValues returns the raw identity values stored for one tailnet. Each
// query stands alone, so one unreadable table does not hide the others. A
// rehearsal reads a private copy, because it touches no file, and so does a
// database that is refused.
func (s *Store) IdentityValues(ctx context.Context, tailnet string, rehearsing bool) registry.Identities {
	if _, err := os.Stat(s.path); err != nil {
		return registry.Identities{}
	}
	if !rehearsing && s.refuse(ctx) == nil {
		return identityValues(ctx, s.reader, tailnet)
	}
	db, err := s.openImage(ctx)
	if err != nil {
		return registry.Identities{}
	}
	defer func() { _ = db.Close() }()
	return identityValues(ctx, db, tailnet)
}

func identityValues(ctx context.Context, db querier, tailnet string) registry.Identities {
	var out registry.Identities
	column := func(query string) []string {
		return column(ctx, db, query+" WHERE tailnet_id = (SELECT id FROM tailnets WHERE key = ?)", tailnet)
	}
	for _, table := range []string{"site_ids", "subnet_pools", "udp_ports", "events"} {
		out.Sites = append(out.Sites, column("SELECT DISTINCT site FROM "+table)...)
	}
	for _, table := range []string{"site_ids", "subnet_pools", "service_addresses"} {
		out.Owners = append(out.Owners, column("SELECT DISTINCT owner FROM "+table)...)
	}
	out.CIDRs = column("SELECT cidr FROM subnet_pools")
	out.Addresses = column("SELECT address FROM service_addresses")
	out.Services = column("SELECT DISTINCT service FROM service_addresses")
	out.Devices = column("SELECT DISTINCT device FROM udp_ports")
	out.Actors = column("SELECT DISTINCT actor FROM events")
	addEventIdentities(ctx, db, tailnet, &out)
	return out
}

func column(ctx context.Context, q querier, query string, args ...any) []string {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value sql.NullString
		if rows.Scan(&value) == nil && value.String != "" {
			values = append(values, value.String)
		}
	}
	if rows.Err() != nil {
		return values
	}
	return values
}

// addEventIdentities adds the keys and values of past events, which outlive
// the reservations they describe.
func addEventIdentities(ctx context.Context, q querier, tailnet string, out *registry.Identities) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT kind, key, before_value, after_value
		FROM events WHERE tailnet_id = (SELECT id FROM tailnets WHERE key = ?)`, tailnet)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			kind, key     string
			before, after sql.NullString
		)
		if rows.Scan(&kind, &key, &before, &after) != nil {
			continue
		}
		addEventIdentity(out, kind, key, before)
		addEventIdentity(out, kind, key, after)
	}
	_ = rows.Err()
}

func addEventIdentity(out *registry.Identities, kindName, key string, stored sql.NullString) {
	kind, err := registry.ParseMapKind(kindName)
	if err != nil {
		return
	}
	value, err := decodeValue(stored)
	if err != nil || value == nil {
		value = &registry.EventValue{}
	}
	out.Owners = append(out.Owners, value.Owner)
	switch kind {
	case registry.MapSiteIDs:
		out.Sites = append(out.Sites, value.Target)
	case registry.MapSubnetPools:
		out.CIDRs = append(out.CIDRs, key)
		out.Sites = append(out.Sites, value.Target)
	case registry.MapServiceAddresses:
		out.Addresses = append(out.Addresses, key)
		out.Services = append(out.Services, value.Target)
	case registry.MapUDPPorts:
		out.Devices = append(out.Devices, value.Target)
	}
}
