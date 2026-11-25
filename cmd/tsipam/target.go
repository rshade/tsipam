package main

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"go.opentelemetry.io/otel/trace"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/ipamhttp"
	"github.com/rshade/tsipam/internal/ipamsvc"
	"github.com/rshade/tsipam/internal/loopback"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/backend"
)

const (
	registryFlag    = "registry"
	registryDirFlag = "registry-dir"
	tailnetFlagName = "tailnet"
	registryEnv     = "TSIPAM_REGISTRY"
)

type liveSnapshotFunc func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error)

// registryTarget is where a registry-backed command sends its requests. It is
// the one place commands reach server code (see boundary_test.go).
type registryTarget struct {
	selector backend.Selector
	logger   diagnostics.Logger
	// collectLive reads live allocations for plan and check; nil elsewhere.
	collectLive liveSnapshotFunc
}

func addRegistryFlags(cmd *cobra.Command, tailnet *string) {
	cmd.Flags().String(registryFlag, "",
		"registry URL: file://<directory> or sqlite://<path> (default: $"+registryEnv+
			", or \"file://.\"); mutually exclusive with --registry-dir")
	cmd.Flags().String(registryDirFlag, defaultRegistryDir,
		"directory holding <tailnet>.ipam.hujson registry files")
	cmd.Flags().StringVar(tailnet, tailnetFlagName, "",
		"routing/storage key for the registry (default: $TAILSCALE_TAILNET, or \"local\")")
}

// registryTarget resolves cmd's registry selection without touching storage.
func (s *commandState) registryTarget(cmd *cobra.Command) (registryTarget, error) {
	selector, err := resolveRegistry(cmd.Flags(), s.getenv)
	if err != nil {
		return registryTarget{}, err
	}
	return registryTarget{selector: selector, logger: s.logger}, nil
}

// resolveRegistry applies the selection order: an explicit flag, then
// TSIPAM_REGISTRY, then the current directory.
func resolveRegistry(flags *pflag.FlagSet, getenv func(string) string) (backend.Selector, error) {
	urlChanged, dirChanged := flags.Changed(registryFlag), flags.Changed(registryDirFlag)
	switch {
	case urlChanged && dirChanged:
		return backend.Selector{}, invalidInput(
			errors.New("--registry and --registry-dir cannot be used together"))
	case urlChanged:
		return parseRegistryURL(flagValue(flags, registryFlag))
	case dirChanged:
		return backend.File(flagValue(flags, registryDirFlag)), nil
	}
	if env := getenv(registryEnv); env != "" {
		return parseRegistryURL(env)
	}
	return backend.File(defaultRegistryDir), nil
}

func parseRegistryURL(raw string) (backend.Selector, error) {
	selector, err := backend.Parse(raw)
	return selector, invalidInput(err)
}

func flagValue(flags *pflag.FlagSet, name string) string {
	flag := flags.Lookup(name)
	if flag == nil {
		return ""
	}
	return flag.Value.String()
}

// run calls fn with a client for a service that lives only as long as the call.
func (t registryTarget) run(ctx context.Context, fn func(context.Context, *ipamclient.Client) error) error {
	ledger, closeStore, err := t.openLedger()
	if err != nil {
		return err
	}
	defer closeStore()
	handler := ipamhttp.NewHandler(&ipamsvc.Service{
		Ledger:      ledger,
		Logger:      t.logger,
		CollectLive: t.collectLive,
	}, ipamhttp.WithTracerProvider(trace.SpanFromContext(ctx).TracerProvider()))
	return loopback.Run(ctx, handler, t.logger, fn)
}

// locations lists the filesystem names that identify the registry, for the
// sanitizer.
func (t registryTarget) locations(tailnet string) []string {
	return t.selector.Locations(tailnet)
}

// identities lists the identity values the registry stores, for the sanitizer.
func (t registryTarget) identities(ctx context.Context, tailnet string, rehearsing bool) registry.Identities {
	ledger, closeStore, err := t.openLedger()
	if err != nil {
		return registry.Identities{}
	}
	defer closeStore()
	if rehearsing {
		ledger = ledger.Rehearsal()
	}
	return ledger.IdentityValues(ctx, tailnet)
}

func (t registryTarget) openLedger() (*registry.Ledger, func(), error) {
	store, err := backend.Open(t.selector, t.logger)
	if err != nil {
		return nil, nil, err
	}
	return registry.NewLedger(store, t.logger), func() {
		closer, ok := store.(io.Closer)
		if !ok {
			return
		}
		if closeErr := closer.Close(); closeErr != nil {
			diagnostics.Warn(t.logger, "closing registry failed", "error", closeErr)
		}
	}, nil
}

// liveCollector reads live state from the tailnet it is given rather than the
// one in the environment, which holds only the credentials.
func liveCollector(getenv func(string) string) liveSnapshotFunc {
	return func(ctx context.Context, source string, logger diagnostics.Logger) (devices.Snapshot, error) {
		sourceEnv := func(key string) string {
			if key == "TAILSCALE_TAILNET" {
				return source
			}
			return getenv(key)
		}
		return devices.CollectSnapshot(ctx, sourceEnv, logger)
	}
}

// checkLiveCredentials reports, without contacting anything, whether a live
// read can be attempted.
func checkLiveCredentials(getenv func(string) string) error {
	if err := devices.CheckCredentials(getenv); err != nil {
		return &credentialsError{err: err}
	}
	return nil
}
