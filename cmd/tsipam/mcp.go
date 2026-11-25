package main

import (
	"fmt"

	"github.com/rshade/ax-go/mcp"
	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"
)

const commandMCPServer = "mcp-server"

// newMCPServerCommand serves the command tree to an MCP client over stdio. It is
// not the runtime's own command, which would add an unauthenticated HTTP
// transport that can write the registry.
func newMCPServerCommand(root *cobra.Command, state *commandState) *cobra.Command {
	return &cobra.Command{
		Use:   commandMCPServer,
		Short: "Serve the commands to an MCP client over stdin and stdout.",
		Long: "Serve every leaf command except check as a tool to an MCP client on this machine.\n\n" +
			"The client starts this command as a child, speaks the Model Context Protocol on its\n" +
			"stdin and stdout, and ends it by closing stdin. It opens no network listener, and logs\n" +
			"go to stderr. The client gets the caller's own authority: it can read any registry or\n" +
			"policy file the caller can, and write registries. A release needs yes in that call.",
		Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := refuseCallFlags(cmd); err != nil {
				return err
			}
			state.serving = true
			return mcp.Serve(cmd.Context(), root, mcp.WithVersion(resolvedVersion()))
		},
	}
}

// refuseCallFlags refuses the flags that each tool call resolves for itself.
// Accepting one here would promise a rehearsal, an approval, or a redaction that
// no call gets.
func refuseCallFlags(cmd *cobra.Command) error {
	for flag, instead := range map[string]string{
		"dry-run":         "pass dry-run in each call",
		"yes":             "pass yes in each release call",
		"idempotency-key": "pass idempotency-key in each call",
		"sanitize":        "set " + sanitize.LevelEnv + " to sanitize every call",
	} {
		if cmd.Flags().Changed(flag) {
			return invalidInput(fmt.Errorf("--%s does not reach tool calls; %s", flag, instead))
		}
	}
	return nil
}

// excludeTools keeps the root and check out of the tool list. A failed call
// returns no result, so check would report drift without its rows; plan returns
// the same report as a result.
func excludeTools(root *cobra.Command) {
	mcp.Exclude(root)
	for _, cmd := range root.Commands() {
		if cmd.Name() == commandCheck {
			mcp.Exclude(cmd)
		}
	}
}
