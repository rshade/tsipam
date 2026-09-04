package main

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const cobraCompletionCommand = "completion"

type schemaFlag struct {
	Name     string `json:"name"`
	Usage    string `json:"usage"`
	Default  string `json:"default"`
	Required bool   `json:"required,omitempty"`
}

type schemaCommand struct {
	Name    string       `json:"name"`
	Summary string       `json:"summary"`
	Usage   string       `json:"usage"`
	Flags   []schemaFlag `json:"flags,omitempty"`
}

type exitCodeSchema struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
}

func flagSchemaFor(flags *pflag.FlagSet) []schemaFlag {
	catalog := make([]schemaFlag, 0)
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" {
			return
		}
		_, required := flag.Annotations[cobra.BashCompOneRequiredFlag]
		catalog = append(catalog, schemaFlag{
			Name: flag.Name, Usage: flag.Usage, Default: flag.DefValue, Required: required,
		})
	})
	return catalog
}

// commandSchema walks the live Cobra tree so adding a command or local flag
// automatically reaches the agent-facing contract. Commands are flattened to
// full paths ("site-id reserve") like tail-acl's schema catalog.
func commandSchema(root *cobra.Command) []schemaCommand {
	commands := make([]schemaCommand, 0)
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if !child.IsAvailableCommand() || child.Name() == cobraCompletionCommand {
				continue
			}
			name := strings.TrimPrefix(child.CommandPath(), root.Name()+" ")
			commands = append(commands, schemaCommand{
				Name: name, Summary: child.Short, Usage: name + strings.TrimPrefix(child.Use, child.Name()),
				Flags: flagSchemaFor(child.LocalNonPersistentFlags()),
			})
			walk(child)
		}
	}
	walk(root)
	return commands
}

func printSchema(writer io.Writer, root *cobra.Command) error {
	document := struct {
		Name          string           `json:"name"`
		Version       string           `json:"version"`
		SchemaVersion string           `json:"schema_version"`
		Description   string           `json:"description"`
		Sanitization  string           `json:"sanitization"`
		Flags         []schemaFlag     `json:"flags"`
		Commands      []schemaCommand  `json:"commands"`
		ExitCodes     []exitCodeSchema `json:"exit_codes"`
	}{
		Name:          toolName,
		Version:       root.Version,
		SchemaVersion: toolSchemaVersion,
		Description: "Allocate and plan Tailscale address space through a local, " +
			"comment-preserving registry and read-only control-plane inventory.",
		Sanitization: "TAILSCALE_SANITIZE=off|identity|strict sanitizes rendered output after collection; " +
			"a non-off environment value overrides --sanitize. Device names, addresses, and routes; " +
			"tailnet routing keys; site names; service identities; reservation owners; reserved subnet CIDRs; " +
			"reserved service addresses; and UDP-port site and device labels are replaced. " +
			"UDP port numbers remain visible. strict is accepted for cross-tool " +
			"flag compatibility and is currently equivalent to identity for tail-ipam. " +
			"calc output (stateless arithmetic over CLI-supplied CIDRs) is not sanitized because it contains no tailnet-derived data. " +
			"Set TAILSCALE_SANITIZE_KEY (64 hex characters) for stable tailnet-scoped placeholders; " +
			"without it placeholders are artifact-scoped.",
		Flags:    flagSchemaFor(root.Flags()),
		Commands: commandSchema(root),
		ExitCodes: []exitCodeSchema{
			{Code: exitOK, Description: "success"},
			{Code: exitFailure, Description: "a valid operation was refused, such as a registry collision " +
				"or a calc split producing too many subnets to materialize"},
			{Code: exitUsage, Description: "invalid invocation, collection failure, or unreadable registry"},
		},
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
