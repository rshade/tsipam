package main

import (
	"context"
	"io"
	"os"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/rshade/tsacl/pkg/policycore/cliflags"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// otlpEndpointEnv is the only switch that makes the runtime build a telemetry
// exporter.
const otlpEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// invocation is one run of the command line and everything it may touch.
type invocation struct {
	args           []string
	getenv         func(string) string
	stdin          io.Reader
	stdinIsTTY     bool
	stdout, stderr io.Writer
	stdoutIsTTY    bool
	listDevices    devicesListFunc
	collectLive    liveSnapshotFunc
}

func runCLI(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	listDevices devicesListFunc,
) int {
	return invocation{
		args: args, getenv: getenv, stdin: os.Stdin, stdinIsTTY: isTerminal(os.Stdin),
		stdout: stdout, stderr: stderr, stdoutIsTTY: isTerminal(stdout), listDevices: listDevices,
	}.run(ctx)
}

func (inv invocation) run(ctx context.Context) int {
	state := &commandState{stdinIsTTY: inv.stdinIsTTY}
	root := inv.commandTree(state)
	state.argv = cliflags.Normalize(root, completeArgs(inv.args))
	root.SetArgs(state.argv)
	return ax.Execute(ctx, root,
		ax.WithStdin(inv.stdin),
		ax.WithStdout(state.stdout),
		ax.WithStderr(inv.stderr),
		ax.WithEnv(withoutExporter(state.getenv)),
		ax.WithStdoutIsTTY(inv.stdoutIsTTY),
		ax.WithVersion(resolvedVersion()),
		ax.WithFlushFunc(state.flushLogger),
	)
}

// commandTree builds every command that can run, including the ones Cobra and
// the runtime would otherwise add too late to be covered by the boundary.
func (inv invocation) commandTree(state *commandState) *cobra.Command {
	getenv := inv.getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	listDevices := inv.listDevices
	if listDevices == nil {
		listDevices = func(ctx context.Context, logger diagnostics.Logger) ([]deviceRecord, error) {
			return collectDevices(ctx, getenv, logger)
		}
	}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: getenv, listDevices: listDevices, collectLive: inv.collectLive,
	})
	addRuntimeFlags(root)
	schema := ax.NewSchemaCommand(root, ax.WithSchemaVersion(resolvedVersion()))
	schema.Args = cobra.NoArgs
	root.AddCommand(schema, newMCPServerCommand(root, state))
	excludeTools(root)
	// Cobra's completion command keeps the writer it finds when it is created.
	state.stdout = stdoutWriter{writer: inv.stdout}
	root.SetOut(state.stdout)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	declareContracts(root)
	installBoundary(root, state)
	return root
}

// addRuntimeFlags registers the runtime's persistent flags before the command
// line is normalized, so their single-dash spellings work like every other
// flag's.
func addRuntimeFlags(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.Bool("dry-run", false, "report what a registry write would do and change nothing")
	flags.Bool("yes", false, "approve a release without a prompt")
	flags.Bool("strict", false, "escalate warnings to a failure; no command emits a warning today")
	flags.String("idempotency-key", "", "opaque key echoed in meta.idempotency_key; it is not stored")
}

// installBoundary routes every error a command can return through the state's
// error boundary. The flag hook is set on each command because an MCP server
// replaces the root's.
func installBoundary(root *cobra.Command, state *commandState) {
	walkCommands(root, func(cmd *cobra.Command) {
		cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
			return state.usageFailed(cmd, nil, err)
		})
		if validate := cmd.Args; validate != nil {
			cmd.Args = func(cmd *cobra.Command, args []string) error {
				return state.usageFailed(cmd, args, validate(cmd, args))
			}
		}
		if prepare := cmd.PersistentPreRunE; prepare != nil {
			cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
				return state.commandFailed(cmd, args, prepare(cmd, args))
			}
		}
		if run := cmd.RunE; run != nil {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				return state.commandFailed(cmd, args, run(cmd, args))
			}
		}
	})
}

// mustDeclare panics on a schema declaration that does not match its command,
// which only a programming error can cause.
func mustDeclare(err error) {
	if err != nil {
		panic(err)
	}
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
}

// completeArgs gives a bare completion request the empty word it completes.
// Cobra adds that command while it runs, where the boundary cannot cover its
// argument check.
func completeArgs(args []string) []string {
	if len(args) == 1 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		return []string{args[0], ""}
	}
	return args
}

// withoutExporter hides the OTLP endpoint, so no telemetry leaves the machine
// whatever the environment sets.
func withoutExporter(getenv func(string) string) func(string) string {
	return func(key string) string {
		if key == otlpEndpointEnv {
			return ""
		}
		return getenv(key)
	}
}

// restoreStdout undoes --strict's buffering for a command. The runtime holds
// stdout back until it knows whether a warning must replace the result, but it
// drops the result of a failing command and the error of a failed write. No
// command warns, so there is nothing to hold back.
func (s *commandState) restoreStdout(cmd *cobra.Command) {
	if strict, err := cmd.Flags().GetBool("strict"); err != nil || !strict {
		return
	}
	// A command with no writer of its own uses the root's.
	if cmd.HasParent() {
		cmd.SetOut(nil)
		return
	}
	cmd.SetOut(s.stdout)
}

// stdoutWriter marks a failed write to stdout, whichever command made it.
type stdoutWriter struct {
	writer io.Writer
}

func (w stdoutWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if err != nil {
		return written, &outputError{err: err}
	}
	return written, nil
}

// isTerminal reports whether stream is an interactive terminal. Other character
// devices, such as /dev/null, are not.
func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
