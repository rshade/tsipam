package ipamhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ipam "github.com/rshade/tsipam/gen/ipam"
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

func reserveSiteID(t *testing.T, site string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/tailnet/acme/ipam/site-ids",
		strings.NewReader(`{"id":"7","site":"`+site+`"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	ipamhttp.NewHandler(fakeService{}).ServeHTTP(recorder, request)
	return recorder
}

func TestNewHandlerServesServiceMethods(t *testing.T) {
	t.Parallel()
	recorder := reserveSiteID(t, "hq")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	var result struct {
		ID      string `json:"id"`
		Site    string `json:"site"`
		Changed bool   `json:"changed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if result.ID != "7" || result.Site != "hq" || !result.Changed {
		t.Errorf("result = %+v, want {ID:7 Site:hq Changed:true}", result)
	}
}

func TestNewHandlerMapsCollisionToConflict(t *testing.T) {
	t.Parallel()
	recorder := reserveSiteID(t, "taken")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "site ID is taken") {
		t.Errorf("body = %s, want the service error message", recorder.Body)
	}
}
