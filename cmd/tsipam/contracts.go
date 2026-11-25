package main

import (
	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
)

// stampedResult is a result whose reserved_at may be stamped from the clock
// during the run. It only tells __schema which field varies: the envelope's own
// type also carries reserved_at values read back from the registry, which do
// not.
type stampedResult struct {
	ReservedAt string `json:"reserved_at" ax:"nondeterministic"`
}

// steadyResult is a result no field of which varies between identical runs.
type steadyResult struct{}

// leafContract is what __schema publishes about a leaf command beyond its
// flags.
type leafContract struct {
	// stamps is true when the command can stamp reserved_at.
	stamps bool
}

// leafContracts holds every leaf command's contract, keyed by its path without
// the tool name. A leaf command missing here fails TestEveryLeafHasAContract.
func leafContracts() map[string]leafContract {
	return map[string]leafContract{
		commandDevicesList:    {},
		commandSiteIDList:     {},
		commandSiteIDReserve:  {stamps: true},
		commandSiteIDRelease:  {},
		commandSubnetList:     {},
		commandSubnetReserve:  {stamps: true},
		commandSubnetAllocate: {stamps: true},
		commandSubnetRelease:  {},
		commandServiceList:    {},
		commandServiceReserve: {stamps: true},
		commandServiceRelease: {},
		commandPortList:       {},
		commandPortReserve:    {},
		commandPortRelease:    {},
		commandCalcSplit:      {},
		commandCalcInfo:       {},
		commandCalcAggregate:  {},
		commandCalcNextFree:   {},
		commandPlan:           {},
		commandCheck:          {},
		commandCheckPolicy:    {},
	}
}

// declareContracts publishes each leaf command's contract through __schema.
func declareContracts(root *cobra.Command) {
	contracts := leafContracts()
	walkCommands(root, func(cmd *cobra.Command) {
		contract, ok := contracts[commandName(cmd)]
		if !ok {
			return
		}
		if contract.stamps {
			ax.WithNonDeterministicFields[stampedResult](cmd)
			return
		}
		ax.WithNonDeterministicFields[steadyResult](cmd)
	})
}
