package ipamsvc

import (
	"context"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/policycheck"
)

// CheckPolicy compares local authored address space with one subnet-pool snapshot.
func (s *Service) CheckPolicy(ctx context.Context, payload *ipam.CheckPolicyPayload) (*ipam.PolicyComparison, error) {
	if err := ctx.Err(); err != nil {
		return nil, ipam.MakePolicyCheckFailed(err)
	}
	pools, err := s.Ledger.ListSubnetPools(ctx, payload.Tailnet)
	if err != nil {
		return nil, ipam.MakePolicyCheckFailed(err)
	}
	report, err := policycheck.Compare([]byte(payload.Policy), pools)
	if err != nil {
		return nil, ipam.MakePolicyCheckFailed(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, ipam.MakePolicyCheckFailed(err)
	}
	result := &ipam.PolicyComparison{
		DriftCount: report.DriftCount,
		Authored:   make([]*ipam.PolicyAuthored, len(report.Authored)),
		Reserved:   make([]*ipam.PolicyReserved, len(report.Reserved)),
	}
	for index, entry := range report.Authored {
		result.Authored[index] = &ipam.PolicyAuthored{
			IPSet: entry.IPSet, CIDR: entry.CIDR, State: entry.State, Pools: entry.Pools,
		}
	}
	for index, entry := range report.Reserved {
		result.Reserved[index] = &ipam.PolicyReserved{
			CIDR: entry.CIDR, Site: entry.Site, State: entry.State, IPSets: entry.IPSets,
		}
	}
	return result, nil
}
