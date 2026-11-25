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
	// class is the command's side effect. It also decides whether the command
	// runs as a rehearsable write and whether it needs approval.
	class ax.Capability
	// stamps is true when the command can stamp reserved_at.
	stamps bool
}

func (c leafContract) writes() bool { return c.class != "" && c.class != ax.CapabilityReadOnly }

func (c leafContract) removes() bool { return c.class == ax.CapabilityDelete }

func contractFor(cmd *cobra.Command) leafContract { return leafContracts()[commandName(cmd)] }

// leafContracts holds every leaf command's contract, keyed by its path without
// the tool name. A leaf command missing here fails TestEveryLeafHasAContract.
func leafContracts() map[string]leafContract {
	return map[string]leafContract{
		commandDevicesList:    {class: ax.CapabilityReadOnly},
		commandSiteIDList:     {class: ax.CapabilityReadOnly},
		commandSiteIDReserve:  {class: ax.CapabilityMutate, stamps: true},
		commandSiteIDRelease:  {class: ax.CapabilityDelete},
		commandSubnetList:     {class: ax.CapabilityReadOnly},
		commandSubnetReserve:  {class: ax.CapabilityMutate, stamps: true},
		commandSubnetAllocate: {class: ax.CapabilityCreate, stamps: true},
		commandSubnetRelease:  {class: ax.CapabilityDelete},
		commandServiceList:    {class: ax.CapabilityReadOnly},
		commandServiceReserve: {class: ax.CapabilityMutate, stamps: true},
		commandServiceRelease: {class: ax.CapabilityDelete},
		commandPortList:       {class: ax.CapabilityReadOnly},
		commandPortReserve:    {class: ax.CapabilityMutate},
		commandPortRelease:    {class: ax.CapabilityDelete},
		commandCalcSplit:      {class: ax.CapabilityReadOnly},
		commandCalcInfo:       {class: ax.CapabilityReadOnly},
		commandCalcAggregate:  {class: ax.CapabilityReadOnly},
		commandCalcNextFree:   {class: ax.CapabilityReadOnly},
		commandPlan:           {class: ax.CapabilityReadOnly},
		commandCheck:          {class: ax.CapabilityReadOnly},
		commandCheckPolicy:    {class: ax.CapabilityReadOnly},
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
		mustDeclare(ax.DeclareCapability(cmd, contract.class, ""))
		if contract.stamps {
			ax.WithNonDeterministicFields[stampedResult](cmd)
			return
		}
		ax.WithNonDeterministicFields[steadyResult](cmd)
	})
}
