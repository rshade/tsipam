package ipamsvc

import (
	"context"
	"errors"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/planner"
	"github.com/rshade/tsipam/internal/registry"
)

// Plan compares one registry snapshot with complete live allocation inventory.
func (s *Service) Plan(ctx context.Context, payload *ipam.PlanPayload) (*ipam.ComparisonReport, error) {
	if s.CollectLive == nil {
		return nil, ipam.MakeComparisonFailed(errors.New("live allocation collector is not configured"))
	}
	reg, err := registry.LoadForPlanning(s.registryPath(payload.Tailnet))
	if err != nil {
		return nil, ipam.MakeComparisonFailed(s.registryError(payload.Tailnet, err))
	}
	live, err := s.CollectLive(ctx, payload.LiveTailnet, s.Logger)
	if err != nil {
		return nil, ipam.MakeComparisonFailed(err)
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
