// Package loopback runs the goa-generated ipam HTTP server and client over an
// ephemeral, per-invocation Unix domain socket, in-process.
//
// The point is dogfooding: every tail-ipam invocation, including the fully
// local default, goes through the same transport/encoding/validation path a
// remote deployment's client would use. Pointing the client at a real host
// later is an endpoint change, not a code change. This is deliberately not a
// daemon — no PID file, no supervision, nothing outlives the command. Each
// call to Run starts a listener, serves exactly the one request the caller's
// fn makes, shuts the server down, and removes the socket, all before
// returning.
package loopback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	goahttp "goa.design/goa/v3/http"

	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	ipamserver "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/server"
	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
)

// socketFileName is fixed because each Run gets its own freshly created
// temporary directory (see [os.MkdirTemp] below) — the uniqueness guarantee
// comes from the directory, not the file name, so two concurrent tail-ipam
// processes never share a socket path.
const socketFileName = "ipam.sock"

// readHeaderTimeout and shutdownTimeout bound the loopback server's request
// header read and graceful-shutdown wait respectively. Both are generous for
// a same-host Unix-socket round trip; they exist to fail a hung invocation
// rather than to accommodate real latency.
const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// RunWithLogger starts an ephemeral server backed by a registry directory of
// "<tailnet>.ipam.hujson" files, binds it to a fresh Unix socket, builds the
// matching generated HTTP client pointed at that socket, calls fn with it,
// then shuts the server down and removes the socket — in that order, before
// returning fn's error (if any). logger receives identity-free structured
// lifecycle diagnostics; nil is quiet.
func RunWithLogger(
	ctx context.Context,
	registryDir string,
	logger diagnostics.Logger,
	fn func(context.Context, *ipamclient.Client) error,
) error {
	return RunWithService(ctx, &ipamsvc.Service{RegistryDir: registryDir, Logger: logger}, fn)
}

// RunWithService uses a configured service, including its optional live
// collector, over the same ephemeral generated HTTP transport as registry calls.
func RunWithService(
	ctx context.Context,
	svc *ipamsvc.Service,
	fn func(context.Context, *ipamclient.Client) error,
) error {
	logger := svc.Logger
	diagnostics.Debug(logger, "starting loopback registry transport")
	socketDir, err := os.MkdirTemp("", "tail-ipam-*")
	if err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(socketDir); removeErr != nil {
			diagnostics.Warn(logger, "removing loopback socket directory failed", "error", removeErr)
		}
	}()
	socketPath := filepath.Join(socketDir, socketFileName)

	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", socketPath, err)
	}

	endpoints := ipam.NewEndpoints(svc)
	mux := goahttp.NewMuxer()
	httpSrv := ipamserver.New(endpoints, mux, goahttp.RequestDecoder, goahttp.ResponseEncoder, nil, nil)
	ipamserver.Mount(mux, httpSrv)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			diagnostics.Warn(logger, "shutting down loopback transport failed", "error", shutdownErr)
		}
		if serveErr := <-serveErrCh; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			diagnostics.Warn(logger, "loopback transport stopped unexpectedly", "error", serveErr)
		}
		diagnostics.Debug(logger, "loopback registry transport stopped")
	}()

	dialer := net.Dialer{}
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(dialCtx, "unix", socketPath)
			},
		},
	}
	client := ipamclient.NewClient("http", "tail-ipam.local", httpClient,
		goahttp.RequestEncoder, goahttp.ResponseDecoder, false)

	diagnostics.Debug(logger, "loopback registry transport ready")
	return fn(ctx, client)
}
