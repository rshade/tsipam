// Command tail-ipam manages a declarative registry of Tailscale address
// space — 4via6 site IDs so far, with subnet CIDR pools and per-service /32
// allocations as separately scoped future work — and will plan/check that
// registry against live control-plane state.
//
// "site-id reserve/release/list" edit and read the local registry file
// (comment-preserving HuJSON, see internal/registry); "devices list" is a
// read-only proof that the tscli-backed client construction works. Neither
// cross-checks the registry against live state yet — that's the "plan"/
// "check" surface, still future work. See ../../README.md and
// ../../../ROADMAP.md ("Future Vision" -> tail-ipam) for the design this will
// grow into.
//
// Output contract (AX conventions, see ../../../AGENTS.md): the command
// result will be written to stdout — human text by default, or a versioned
// JSON envelope with -format json — while logs and errors go to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	goa "goa.design/goa/v3/pkg"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// version is overridden at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

const toolName = "tail-ipam"

// Exit codes, matching the AX scheme documented in ../../../AGENTS.md:
// 0 success, 1 a check/operation was refused (e.g. a site-ID collision),
// 2 usage error or the tool/registry could not be read at all.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() { os.Exit(realMain()) }

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := newRootCommand()
	root.SetArgs(os.Args[1:])
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		// SilenceErrors on every command hands printing to the caller so
		// cobra never writes to stdout on failure; this is that print.
		fmt.Fprintf(os.Stderr, "%s: %v\n", toolName, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// collisionErrorName must match design.Error("collision", ...) in
// ../../design/design.go: it is how a *goa.ServiceError decoded off the wire
// is told apart from every other error the generated HTTP client can return.
const collisionErrorName = "collision"

// exitCodeFor maps a command error to this tool's exit-code scheme (see
// newRootCommand's Long text): a site-ID collision is a decidable refusal,
// not a usage problem, so it gets its own code rather than being
// indistinguishable from a bad flag or an unreadable registry file.
//
// site-id commands now run through the loopback goa HTTP client (see
// internal/loopback), so a collision normally arrives as a *goa.ServiceError
// decoded from the 409 response, not the original *registry.SiteIDCollisionError
// — that concrete Go type does not cross the wire. Both checks stay: the
// goa.ServiceError case is what actually fires today, and the registry error
// case is cheap insurance against a future caller that invokes the registry
// package directly, in-process, without going through the service layer.
func exitCodeFor(err error) int {
	var svcErr *goa.ServiceError
	if errors.As(err, &svcErr) && svcErr.Name == collisionErrorName {
		return exitFailure
	}
	var collision *registry.SiteIDCollisionError
	if errors.As(err, &collision) {
		return exitFailure
	}
	return exitUsage
}

// newRootCommand builds the root command. plan/check against live state
// still needs its own design pass — see ROADMAP.md.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   toolName,
		Short: "Allocate and plan Tailscale address space.",
		Long: "Allocate and plan Tailscale address space.\n\n" +
			"Exit codes: 0 success, 1 a check or operation was refused " +
			"(e.g. a site-ID collision), 2 usage error or the tool/registry " +
			"could not be read at all.",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newDevicesCommand())
	root.AddCommand(newSiteIDCommand())
	return root
}
