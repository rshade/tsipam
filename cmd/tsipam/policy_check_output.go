package main

import (
	"fmt"
	"io"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipam "github.com/rshade/tsipam/gen/ipam"
)

const kindIPSet = "ipset"

type policyAuthoredOutput struct {
	IPSet string   `json:"ipset"`
	CIDR  string   `json:"cidr"`
	State string   `json:"state"`
	Pools []string `json:"pools"`
}

type policyReservedOutput struct {
	CIDR   string   `json:"cidr"`
	Site   string   `json:"site"`
	State  string   `json:"state"`
	IPSets []string `json:"ipsets"`
}

type policyComparisonOutput struct {
	DriftCount int                    `json:"drift_count"`
	Authored   []policyAuthoredOutput `json:"authored"`
	Reserved   []policyReservedOutput `json:"reserved"`
}

func policyComparisonEnvelope(tailnet string, report *ipam.PolicyComparison) *outputEnvelope {
	status := statusOK
	if report.DriftCount > 0 {
		status = statusDrift
	}
	result := newOutput(commandCheckPolicy, status, fmt.Sprintf("found %d policy drift(s)", report.DriftCount), tailnet)
	comparison := &policyComparisonOutput{
		DriftCount: report.DriftCount,
		Authored:   make([]policyAuthoredOutput, len(report.Authored)),
		Reserved:   make([]policyReservedOutput, len(report.Reserved)),
	}
	for index, entry := range report.Authored {
		comparison.Authored[index] = policyAuthoredOutput{
			IPSet: entry.IPSet, CIDR: entry.CIDR, State: entry.State, Pools: append([]string{}, entry.Pools...),
		}
	}
	for index, entry := range report.Reserved {
		comparison.Reserved[index] = policyReservedOutput{
			CIDR: entry.CIDR, Site: entry.Site, State: entry.State, IPSets: append([]string{}, entry.IPSets...),
		}
	}
	result.PolicyComparison = comparison
	return result
}

func renderPolicyComparison(writer io.Writer, tailnet string, report *policyComparisonOutput) error {
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

func sanitizePolicyComparison(input *policyComparisonOutput, output *sanitize.Sanitizer) *policyComparisonOutput {
	if input == nil {
		return nil
	}
	result := &policyComparisonOutput{
		DriftCount: input.DriftCount,
		Authored:   make([]policyAuthoredOutput, len(input.Authored)),
		Reserved:   make([]policyReservedOutput, len(input.Reserved)),
	}
	for index, entry := range input.Authored {
		result.Authored[index] = policyAuthoredOutput{
			IPSet: output.Value(kindIPSet, entry.IPSet), CIDR: output.Value(kindCIDR, entry.CIDR),
			State: entry.State, Pools: sanitizeNetworkValues(entry.Pools, output),
		}
	}
	for index, entry := range input.Reserved {
		sets := make([]string, len(entry.IPSets))
		for setIndex, name := range entry.IPSets {
			sets[setIndex] = output.Value(kindIPSet, name)
		}
		result.Reserved[index] = policyReservedOutput{
			CIDR: output.Value(kindCIDR, entry.CIDR), Site: output.Value(kindSite, entry.Site),
			State: entry.State, IPSets: sets,
		}
	}
	return result
}
