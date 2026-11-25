// Package ipamconn builds the generated ipam client. Every client, whatever
// it dials, uses the encoding chosen here.
package ipamconn

import (
	goahttp "goa.design/goa/v3/http"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
)

// NewClient returns a client that sends requests for scheme://host through doer.
func NewClient(scheme, host string, doer goahttp.Doer) *ipamclient.Client {
	return ipamclient.NewClient(scheme, host, doer, goahttp.RequestEncoder, goahttp.ResponseDecoder, false)
}
