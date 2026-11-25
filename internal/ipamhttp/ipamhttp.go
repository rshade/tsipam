// Package ipamhttp builds the HTTP handler that serves the goa ipam service.
// Every server, whatever it listens on, uses this one handler.
package ipamhttp

import (
	"net/http"

	goahttp "goa.design/goa/v3/http"

	ipamserver "github.com/rshade/tsipam/gen/http/ipam/server"
	ipam "github.com/rshade/tsipam/gen/ipam"
)

// NewHandler returns a handler that serves every method of svc.
func NewHandler(svc ipam.Service) http.Handler {
	mux := goahttp.NewMuxer()
	server := ipamserver.New(
		ipam.NewEndpoints(svc), mux, goahttp.RequestDecoder, goahttp.ResponseEncoder, nil, nil,
	)
	ipamserver.Mount(mux, server)
	return mux
}
