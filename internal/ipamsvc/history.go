package ipamsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/registry"
)

// ListHistory implements ipam.Service.
func (s *Service) ListHistory(ctx context.Context, p *ipam.ListHistoryPayload) ([]*ipam.RegistryEvent, error) {
	filter, err := historyFilter(p)
	if err != nil {
		return nil, ipam.MakeInvalidInput(err)
	}
	events, err := s.Ledger.History(ctx, p.Tailnet, filter)
	if err != nil {
		return nil, translateHistoryError(err)
	}
	out := make([]*ipam.RegistryEvent, len(events))
	for i, event := range events {
		out[i] = &ipam.RegistryEvent{
			At: event.At, Action: string(event.Action), Kind: event.Kind.String(), Key: event.Key,
			Site: nonEmptyPtr(event.Site), Actor: nonEmptyPtr(event.Actor),
			Before: eventValue(event.Before), After: eventValue(event.After),
		}
	}
	return out, nil
}

func historyFilter(p *ipam.ListHistoryPayload) (registry.HistoryFilter, error) {
	filter := registry.HistoryFilter{Key: stringOrEmpty(p.Key), Limit: p.Limit}
	if p.Kind != nil {
		kind, err := registry.ParseMapKind(*p.Kind)
		if err != nil {
			return registry.HistoryFilter{}, err
		}
		filter.Kind = kind
	}
	if p.Since != nil {
		since, err := time.Parse(time.RFC3339, *p.Since)
		if err != nil {
			return registry.HistoryFilter{}, fmt.Errorf("since %q is not an RFC 3339 time", *p.Since)
		}
		filter.Since = since
	}
	return filter, nil
}

func eventValue(value *registry.EventValue) *ipam.RegistryEventValue {
	if value == nil {
		return nil
	}
	return &ipam.RegistryEventValue{
		Target: value.Target,
		Owner:  nonEmptyPtr(value.Owner), Description: nonEmptyPtr(value.Description),
		Ticket: nonEmptyPtr(value.Ticket), ReservedAt: nonEmptyPtr(value.ReservedAt),
	}
}

func translateHistoryError(err error) error {
	if errors.Is(err, registry.ErrHistoryUnsupported) {
		return ipam.MakeUnsupported(err)
	}
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}
