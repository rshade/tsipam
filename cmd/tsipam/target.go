package main

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/ipamhttp"
	"github.com/rshade/tsipam/internal/ipamsvc"
	"github.com/rshade/tsipam/internal/loopback"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

type liveSnapshotFunc func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error)

// registryTarget is where a registry-backed command sends its requests. It is
// the one place commands reach server code (see boundary_test.go).
type registryTarget struct {
	registryDir string
	logger      diagnostics.Logger
	// collectLive reads live allocations for plan and check; nil elsewhere.
	collectLive liveSnapshotFunc
}

func (s *commandState) registryTarget(registryDir string) registryTarget {
	return registryTarget{registryDir: registryDir, logger: s.logger}
}

// run calls fn with a client for a service that lives only as long as the call.
func (t registryTarget) run(ctx context.Context, fn func(context.Context, *ipamclient.Client) error) error {
	handler := ipamhttp.NewHandler(&ipamsvc.Service{
		Ledger:      registry.NewLedger(hujsonstore.New(t.registryDir, t.logger), t.logger),
		Logger:      t.logger,
		CollectLive: t.collectLive,
	}, ipamhttp.WithTracerProvider(trace.SpanFromContext(ctx).TracerProvider()))
	return loopback.Run(ctx, handler, t.logger, fn)
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
