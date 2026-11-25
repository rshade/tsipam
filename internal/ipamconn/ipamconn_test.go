package ipamconn_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/ipamconn"
	"github.com/rshade/tsipam/internal/ipamhttp"
)

type fakeService struct {
	ipam.Service
}

func (fakeService) ReserveSiteID(
	_ context.Context, payload *ipam.ReserveSiteIDPayload,
) (*ipam.ReserveSiteIDResult, error) {
	if payload.Site == "taken" {
		return nil, ipam.MakeCollision(errors.New("site ID is taken"))
	}
	return &ipam.ReserveSiteIDResult{ID: payload.ID, Site: payload.Site, Changed: true}, nil
}

func reserveSiteID(t *testing.T, site string) (any, error) {
	t.Helper()
	server := httptest.NewServer(ipamhttp.NewHandler(fakeService{}))
	defer server.Close()
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	client := ipamconn.NewClient(address.Scheme, address.Host, server.Client())
	return client.ReserveSiteID()(
		t.Context(), &ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "7", Site: site},
	)
}

func TestNewClientCallsHandler(t *testing.T) {
	t.Parallel()
	response, err := reserveSiteID(t, "hq")
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	result, ok := response.(*ipam.ReserveSiteIDResult)
	if !ok {
		t.Fatalf("result type = %T, want *ipam.ReserveSiteIDResult", response)
	}
	if result.ID != "7" || result.Site != "hq" || !result.Changed {
		t.Errorf("result = %+v, want {ID:7 Site:hq Changed:true}", result)
	}
}

func TestNewClientDecodesNamedErrors(t *testing.T) {
	t.Parallel()
	_, err := reserveSiteID(t, "taken")
	serviceErr, ok := errors.AsType[*goa.ServiceError](err)
	if !ok {
		t.Fatalf("error = %T %v, want *goa.ServiceError", err, err)
	}
	if serviceErr.Name != "collision" || serviceErr.Message != "site ID is taken" {
		t.Errorf("error = %q %q, want collision with the service message", serviceErr.Name, serviceErr.Message)
	}
}
