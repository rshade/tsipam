package registry_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

// bareStore implements only the Store contract.
type bareStore struct {
	registry.Store
}

func TestIdentityValuesWithoutSource(t *testing.T) {
	t.Parallel()
	got := registry.NewLedger(bareStore{}, nil).IdentityValues(context.Background(), "example.ts.net")
	if !reflect.DeepEqual(got, registry.Identities{}) {
		t.Errorf("identities = %+v, want none", got)
	}
}

func TestMapKindNamesRoundTrip(t *testing.T) {
	t.Parallel()
	for _, kind := range []registry.MapKind{
		registry.MapSiteIDs, registry.MapSubnetPools, registry.MapServiceAddresses, registry.MapUDPPorts,
	} {
		got, err := registry.ParseMapKind(kind.String())
		if err != nil || got != kind {
			t.Errorf("ParseMapKind(%q) = %v, %v; want %v", kind.String(), got, err, kind)
		}
	}
	if _, err := registry.ParseMapKind("vlan"); err == nil {
		t.Error("an unknown kind was accepted")
	}
}
