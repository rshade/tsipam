// Package ipamconn builds the generated ipam client. Every client, whatever
// it dials, uses the encoding chosen here.
package ipamconn

import (
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	goahttp "goa.design/goa/v3/http"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
)

// NewClient returns a client that sends requests for scheme://host through
// doer, carrying the caller's trace.
func NewClient(scheme, host string, doer goahttp.Doer) *ipamclient.Client {
	return ipamclient.NewClient(
		scheme, host, tracingDoer{next: doer}, goahttp.RequestEncoder, goahttp.ResponseDecoder, false,
	)
}

// tracingDoer adds the traceparent of the request's context to each request.
type tracingDoer struct {
	next goahttp.Doer
}

func (d tracingDoer) Do(request *http.Request) (*http.Response, error) {
	propagation.TraceContext{}.Inject(request.Context(), propagation.HeaderCarrier(request.Header))
	return d.next.Do(request)
}
