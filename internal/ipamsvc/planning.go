package ipamsvc

import (
	"context"
	"errors"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/planner"
)

// Plan compares one registry snapshot with complete live allocation inventory.
func (s *Service) Plan(ctx context.Context, payload *ipam.PlanPayload) (*ipam.ComparisonReport, error) {
	if s.CollectLive == nil {
		return nil, ipam.MakeComparisonFailed(errors.New("live allocation collector is not configured"))
	}
	reg, err := s.Ledger.LoadForPlanning(ctx, payload.Tailnet)
	if err != nil {
		return nil, ipam.MakeComparisonFailed(err)
	}
	live, err := s.CollectLive(ctx, payload.LiveTailnet, s.Logger)
	if err != nil {
		return nil, collectionFailed(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, ipam.MakeComparisonFailed(err)
	}
	report, err := planner.Compare(reg, live)
	if err != nil {
		return nil, ipam.MakeComparisonFailed(err)
	}
	result := &ipam.ComparisonReport{
		LiveTailnet: payload.LiveTailnet, DriftCount: report.DriftCount, Excluded: report.Excluded,
		Entries: make([]*ipam.ComparisonEntry, len(report.Entries)),
	}
	for index, entry := range report.Entries {
		result.Entries[index] = &ipam.ComparisonEntry{
			Kind: entry.Kind, State: entry.State, Key: entry.Key, Expected: nonEmptyPtr(entry.Expected),
			Observed: entry.Observed, Routes: entry.Routes,
		}
	}
	return result, nil
}

// collectionFailed is a comparison failure that keeps what a caller needs to
// decide on a retry. The Go type does not cross the transport; these fields do.
func collectionFailed(err error) error {
	failure := ipam.MakeComparisonFailed(err)
	if collection, ok := errors.AsType[*devices.CollectionError](err); ok {
		failure.Timeout = collection.Timeout
		failure.Temporary = collection.Temporary
	}
	return failure
}
