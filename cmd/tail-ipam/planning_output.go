package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/planner"
)

type comparisonEntryOutput struct {
	Kind     string   `json:"kind"`
	State    string   `json:"state"`
	Key      string   `json:"key"`
	Expected string   `json:"expected,omitempty"`
	Observed []string `json:"observed"`
	Routes   []string `json:"routes"`
}

type comparisonOutput struct {
	LiveTailnet string                  `json:"live_tailnet"`
	DriftCount  int                     `json:"drift_count"`
	Entries     []comparisonEntryOutput `json:"entries"`
	Excluded    []string                `json:"excluded"`
}

func comparisonEnvelope(command, tailnet string, report *ipam.ComparisonReport) *outputEnvelope {
	status := statusOK
	if report.DriftCount > 0 {
		status = statusDrift
	}
	result := newOutput(command, status, fmt.Sprintf("found %d allocation drift(s)", report.DriftCount), tailnet)
	comparison := &comparisonOutput{
		LiveTailnet: report.LiveTailnet, DriftCount: report.DriftCount, Excluded: report.Excluded,
		Entries: make([]comparisonEntryOutput, len(report.Entries)),
	}
	for index, entry := range report.Entries {
		comparison.Entries[index] = comparisonEntryOutput{
			Kind: entry.Kind, State: entry.State, Key: entry.Key,
			Observed: append([]string{}, entry.Observed...), Routes: append([]string{}, entry.Routes...),
		}
		if entry.Expected != nil {
			comparison.Entries[index].Expected = *entry.Expected
		}
	}
	result.Comparison = comparison
	return result
}

func renderComparison(writer io.Writer, tailnet string, report *comparisonOutput) error {
	if _, err := fmt.Fprintf(writer, "registry=%+q live_tailnet=%+q\n", tailnet, report.LiveTailnet); err != nil {
		return err
	}
	for _, entry := range report.Entries {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\texpected=%+q\tobserved=%+q\troutes=%+q\n",
			entry.Kind, entry.State, entry.Key, entry.Expected, entry.Observed, entry.Routes); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "drift_count=%d excluded=%s (no live counterpart)\n",
		report.DriftCount, strings.Join(report.Excluded, ","))
	return err
}

func sanitizeComparison(input *comparisonOutput, output *sanitize.Sanitizer) *comparisonOutput {
	if input == nil {
		return nil
	}
	result := &comparisonOutput{
		LiveTailnet: output.Value(kindTailnet, input.LiveTailnet), DriftCount: input.DriftCount,
		Excluded: append([]string{}, input.Excluded...), Entries: make([]comparisonEntryOutput, len(input.Entries)),
	}
	for index, entry := range input.Entries {
		keyKind, expectedKind, observedKind := comparisonIdentityKinds(entry.Kind)
		key := entry.Key
		if entry.Kind != planner.KindSiteID {
			key = output.Value(keyKind, key)
		}
		observed := make([]string, len(entry.Observed))
		for ownerIndex, owner := range entry.Observed {
			observed[ownerIndex] = output.Value(observedKind, owner)
		}
		result.Entries[index] = comparisonEntryOutput{
			Kind: entry.Kind, State: entry.State, Key: key, Expected: output.Value(expectedKind, entry.Expected),
			Observed: observed, Routes: sanitizeNetworkValues(entry.Routes, output),
		}
	}
	return result
}

func comparisonIdentityKinds(kind string) (string, string, string) {
	if kind == planner.KindServiceAddress {
		return kindIP, kindService, kindService
	}
	return kindCIDR, kindSite, kindHostname
}
