package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const flagCIDR = "cidr"

// inputSpec is one required value of a command, which a caller may give by
// position or by flag.
type inputSpec struct {
	// flag is the flag name, without dashes.
	flag string
	// placeholder is the value's name in the command's usage line.
	placeholder string
	usage       string
}

func idInput() inputSpec {
	return inputSpec{flag: "id", placeholder: "id", usage: "4via6 site ID"}
}

func siteInput() inputSpec {
	return inputSpec{flag: "site", placeholder: "site-name", usage: "site name"}
}

func cidrInput() inputSpec {
	return inputSpec{flag: flagCIDR, placeholder: "cidr", usage: "CIDR prefix"}
}

func poolInput() inputSpec {
	return inputSpec{flag: "pool", placeholder: "pool-cidr", usage: "pool CIDR prefix"}
}

func addressInput() inputSpec {
	return inputSpec{flag: "address", placeholder: "address", usage: "IPv4 host address or exact /32"}
}

func serviceInput() inputSpec {
	return inputSpec{flag: "service", placeholder: "service-name", usage: "Service name, with or without svc:"}
}

func portInput() inputSpec {
	return inputSpec{flag: "port", placeholder: "port", usage: "UDP listen port, 1024-65535"}
}

func deviceInput() inputSpec {
	return inputSpec{flag: "device", placeholder: "device", usage: "device label"}
}

func policyFileInput() inputSpec {
	return inputSpec{flag: "policy-file", placeholder: "acl-file", usage: "local HuJSON policy file"}
}

// inputs resolves a command's required values from one source.
type inputs struct {
	specs []inputSpec
}

// bindInputs gives cmd a flag for each required value and an argument check
// that accepts all of them by position or none.
//
// The flags are not marked required, because a caller may use positions
// instead. Presence is checked in resolve rather than in the argument check,
// because an MCP server drops a command whose check refuses zero arguments.
func bindInputs(cmd *cobra.Command, specs ...inputSpec) inputs {
	bound := inputs{specs: specs}
	if len(specs) == 0 {
		cmd.Args = cobra.NoArgs
		return bound
	}
	for _, spec := range specs {
		cmd.Flags().String(spec.flag, "", spec.usage+" (instead of <"+spec.placeholder+">)")
	}
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return bound.checkPositions(cmd, args)
	}
	return bound
}

func (in inputs) checkPositions(cmd *cobra.Command, args []string) error {
	for _, spec := range in.specs {
		if cmd.Flags().Changed(spec.flag) {
			return mixedInputs(spec.flag)
		}
	}
	if len(args) > len(in.specs) {
		return invalidInput(fmt.Errorf("accepts %d arg(s), received %d", len(in.specs), len(args)))
	}
	return missingInputs(in.specs[len(args):])
}

// resolve returns the values in position order, whichever way they were given.
func (in inputs) resolve(cmd *cobra.Command, args []string) ([]string, error) {
	if len(args) > 0 || len(in.specs) == 0 {
		return args, nil
	}
	values := make([]string, len(in.specs))
	var missing []inputSpec
	for index, spec := range in.specs {
		if !cmd.Flags().Changed(spec.flag) {
			missing = append(missing, spec)
			continue
		}
		value, err := cmd.Flags().GetString(spec.flag)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if err := missingInputs(missing); err != nil {
		return nil, err
	}
	return values, nil
}

func missingInputs(missing []inputSpec) error {
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, len(missing))
	for index, spec := range missing {
		names[index] = spec.placeholder + " (--" + spec.flag + ")"
	}
	return invalidInput(errors.New("missing required input: " + strings.Join(names, ", ")))
}

func mixedInputs(flag string) error {
	return invalidInput(fmt.Errorf(
		"give every input by position or every input by flag, not both: --%s was given with positional arguments",
		flag,
	))
}
