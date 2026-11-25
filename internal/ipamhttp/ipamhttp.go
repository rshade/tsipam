// Package ipamhttp builds the HTTP handler that serves the goa ipam service.
// Every server, whatever it listens on, uses this one handler.
package ipamhttp

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	goahttp "goa.design/goa/v3/http"

	ipamserver "github.com/rshade/tsipam/gen/http/ipam/server"
	ipam "github.com/rshade/tsipam/gen/ipam"
)

// Option configures NewHandler.
type Option func(*options)

type options struct {
	tracerProvider trace.TracerProvider
}

// WithTracerProvider sets where service-call spans are recorded. The default is
// the global provider.
func WithTracerProvider(provider trace.TracerProvider) Option {
	return func(opts *options) { opts.tracerProvider = provider }
}

// NewHandler returns a handler that serves every method of svc.
func NewHandler(svc ipam.Service, opts ...Option) http.Handler {
	resolved := options{tracerProvider: otel.GetTracerProvider()}
	for _, opt := range opts {
		opt(&resolved)
	}
	endpoints := ipam.NewEndpoints(svc)
	endpoints.Use(traceEndpoint(resolved.tracerProvider))
	mux := goahttp.NewMuxer()
	server := ipamserver.New(
		endpoints, mux, goahttp.RequestDecoder, goahttp.ResponseEncoder, nil, nil,
	)
	ipamserver.Mount(mux, server)
	return extractTrace(mux)
}
