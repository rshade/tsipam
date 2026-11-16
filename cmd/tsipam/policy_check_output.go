package main

import (
	"fmt"
	"io"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/policycheck"
)

const kindIPSet = "ipset"

func policyComparisonEnvelope(tailnet string, report *ipam.PolicyComparison) *outputEnvelope {
	status := statusOK
	if report.DriftCount > 0 {
		status = statusDrift
	}
	result := newOutput(commandCheckPolicy, status, fmt.Sprintf("found %d policy drift(s)", report.DriftCount), tailnet)
	comparison := &policycheck.Report{
		DriftCount: report.DriftCount,
		Authored:   make([]policycheck.Authored, len(report.Authored)),
		Reserved:   make([]policycheck.Reserved, len(report.Reserved)),
	}
	for index, entry := range report.Authored {
		comparison.Authored[index] = policycheck.Authored{
			IPSet: entry.IPSet, CIDR: entry.CIDR, State: entry.State, Pools: append([]string{}, entry.Pools...),
		}
	}
	for index, entry := range report.Reserved {
		comparison.Reserved[index] = policycheck.Reserved{
			CIDR: entry.CIDR, Site: entry.Site, State: entry.State, IPSets: append([]string{}, entry.IPSets...),
		}
	}
	result.PolicyComparison = comparison
	return result
}

func renderPolicyComparison(writer io.Writer, tailnet string, report *policycheck.Report) error {
	if _, err := fmt.Fprintf(writer, "registry=%+q comparison=authored_ipsets\n", tailnet); err != nil {
		return err
	}
	for _, entry := range report.Authored {
		if _, err := fmt.Fprintf(writer, "authored\t%s\t%+q\tipset=%+q\tpools=%+q\n",
			entry.State, entry.CIDR, entry.IPSet, entry.Pools); err != nil {
			return err
		}
	}
	for _, entry := range report.Reserved {
		if _, err := fmt.Fprintf(writer, "reserved\t%s\t%+q\tsite=%+q\tipsets=%+q\n",
			entry.State, entry.CIDR, entry.Site, entry.IPSets); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "drift_count=%d\n", report.DriftCount)
	return err
}

func sanitizePolicyComparison(input *policycheck.Report, output *sanitize.Sanitizer) *policycheck.Report {
	if input == nil {
		return nil
	}
	result := &policycheck.Report{
		DriftCount: input.DriftCount,
		Authored:   make([]policycheck.Authored, len(input.Authored)),
		Reserved:   make([]policycheck.Reserved, len(input.Reserved)),
	}
	for index, entry := range input.Authored {
		result.Authored[index] = policycheck.Authored{
			IPSet: output.Value(kindIPSet, entry.IPSet), CIDR: output.Value(kindCIDR, entry.CIDR),
			State: entry.State, Pools: sanitizeNetworkValues(entry.Pools, output),
		}
	}
	for index, entry := range input.Reserved {
		sets := make([]string, len(entry.IPSets))
		for setIndex, name := range entry.IPSets {
			sets[setIndex] = output.Value(kindIPSet, name)
		}
		result.Reserved[index] = policycheck.Reserved{
			CIDR: output.Value(kindCIDR, entry.CIDR), Site: output.Value(kindSite, entry.Site),
			State: entry.State, IPSets: sets,
		}
	}
	return result
}
