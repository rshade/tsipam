// Package loopback serves an ipam HTTP handler to a generated client over an
// ephemeral, per-invocation Unix domain socket, in-process. It knows nothing
// about the service behind the handler.
//
// The point is dogfooding: every tsipam invocation, including the fully
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

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/ipamconn"
)

// socketFileName is fixed because each Run gets its own freshly created
// temporary directory (see [os.MkdirTemp] below) — the uniqueness guarantee
// comes from the directory, not the file name, so two concurrent tsipam
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

// Run serves handler on a fresh Unix socket, calls fn with a client for it,
// then shuts the server down and removes the socket — in that order, before
// returning fn's error (if any). logger receives identity-free structured
// lifecycle diagnostics; nil is quiet.
func Run(
	ctx context.Context,
	handler http.Handler,
	logger diagnostics.Logger,
	fn func(context.Context, *ipamclient.Client) error,
) error {
	diagnostics.Debug(logger, "starting loopback registry transport")
	socketDir, err := os.MkdirTemp("", "tsipam-*")
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

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout}

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
	client := ipamconn.NewClient("http", "tsipam.local", httpClient)

	diagnostics.Debug(logger, "loopback registry transport ready")
	return fn(ctx, client)
}
