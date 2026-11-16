package registry_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

func TestReserveServiceAddressCreatesListsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	address, service, metadata, changed, err := registry.ReserveServiceAddressWithLogger(
		path,
		"100.100.100.7/32",
		"payments",
		registry.ReservationMetadata{},
		nil,
	)
	if err != nil {
		t.Fatalf("ReserveServiceAddress: %v", err)
	}
	if address != "100.100.100.7" || service != "svc:payments" || !changed {
		t.Errorf("ReserveServiceAddress = (%q, %q, %+v, %t), want canonical address/service and changed",
			address, service, metadata, changed)
	}

	_, _, _, changed, err = registry.ReserveServiceAddressWithLogger(
		path, address, "svc:payments", registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("repeat ReserveServiceAddress: %v", err)
	}
	if changed {
		t.Error("changed = true on equivalent bare-address/service repeat, want false")
	}

	if _, _, _, _, reserveErr := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.8", "payments", registry.ReservationMetadata{}, nil,
	); reserveErr != nil {
		t.Fatalf("reserving second address for same service: %v", reserveErr)
	}
	entries, err := registry.ListServiceAddressesWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListServiceAddresses: %v", err)
	}
	if len(entries) != 2 || entries[0].Address != "100.100.100.7" || entries[1].Address != "100.100.100.8" {
		t.Errorf("ListServiceAddresses = %+v, want two addresses for svc:payments", entries)
	}
	for _, entry := range entries {
		if entry.Service != "svc:payments" {
			t.Errorf("entry.Service = %q, want svc:payments", entry.Service)
		}
	}
}

func TestReserveServiceAddressRefusesCollisionWithoutMutation(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if _, _, _, _, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7", "payments", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading seeded registry: %v", err)
	}

	_, _, _, changed, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7/32", "search", registry.ReservationMetadata{Owner: "platform"}, nil,
	)
	var collision *registry.ServiceAddressCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("ReserveServiceAddress error = %v, want *ServiceAddressCollisionError", err)
	}
	if collision.Address != "100.100.100.7" || collision.Existing != "svc:payments" ||
		collision.Wanted != "svc:search" {
		t.Errorf("collision = %+v, want canonical address/existing/wanted", collision)
	}
	wantMessage := `service address 100.100.100.7 is already reserved for "svc:payments", not "svc:search"`
	if err.Error() != wantMessage {
		t.Errorf("collision error = %q, want %q", err, wantMessage)
	}
	if changed {
		t.Error("changed = true on refused collision, want false")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading registry after collision: %v", readErr)
	}
	if string(after) != string(before) {
		t.Errorf("registry changed across collision:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestReserveServiceAddressMetadataMergesAndAutoStamps(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, first, changed, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7", "payments",
		registry.ReservationMetadata{Owner: "platform", Ticket: "NET-237"}, nil,
	)
	if err != nil {
		t.Fatalf("first ReserveServiceAddress: %v", err)
	}
	if !changed || first.ReservedAt == "" {
		t.Fatalf("first metadata = %+v, changed=%t; want timestamped change", first, changed)
	}
	if _, parseErr := time.Parse(time.RFC3339, first.ReservedAt); parseErr != nil {
		t.Errorf("ReservedAt = %q, want RFC3339: %v", first.ReservedAt, parseErr)
	}

	_, _, second, changed, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7/32", "svc:payments",
		registry.ReservationMetadata{Description: "payments VIP"}, nil,
	)
	if err != nil {
		t.Fatalf("second ReserveServiceAddress: %v", err)
	}
	if !changed || second.Owner != "platform" || second.Ticket != "NET-237" ||
		second.Description != "payments VIP" || second.ReservedAt != first.ReservedAt {
		t.Errorf("merged metadata = %+v, changed=%t; want omitted fields and timestamp preserved", second, changed)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	for _, want := range []string{`"service"`, `"svc:payments"`, `"owner"`, `"platform"`, `"ticket"`, `"NET-237"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("registry = %s, want object-form token %s", raw, want)
		}
	}
	if strings.Contains(string(raw), `"site"`) {
		t.Errorf("registry service entry = %s, must not use a site field", raw)
	}
}

func TestReserveServiceAddressNeedsNoParentPool(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	writeServiceRegistryFixture(t, path, `{"subnetPools":{"10.0.0.0/24":"hq"}}`+"\n")

	if _, _, _, changed, err := registry.ReserveServiceAddressWithLogger(
		path, "203.0.113.7", "payments", registry.ReservationMetadata{}, nil,
	); err != nil || !changed {
		t.Fatalf("ReserveServiceAddress outside subnet pools = changed %t, error %v; want success", changed, err)
	}
}

func TestReleaseServiceAddressRemovesEntryAndAbsentIsNoop(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if _, _, _, _, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7", "payments", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	address, changed, err := registry.ReleaseServiceAddressWithLogger(path, "100.100.100.7/32", nil)
	if err != nil {
		t.Fatalf("ReleaseServiceAddress: %v", err)
	}
	if address != "100.100.100.7" || !changed {
		t.Errorf("ReleaseServiceAddress = (%q, %t), want canonical address and changed", address, changed)
	}
	_, changed, err = registry.ReleaseServiceAddressWithLogger(path, "100.100.100.7", nil)
	if err != nil {
		t.Fatalf("absent ReleaseServiceAddress: %v", err)
	}
	if changed {
		t.Error("changed = true releasing absent address, want false")
	}
	entries, err := registry.ListServiceAddressesWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListServiceAddresses: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ListServiceAddresses = %+v, want empty after release", entries)
	}
}

func TestReleaseAndListServiceAddressMissingFileAreErrors(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, releaseErr := registry.ReleaseServiceAddressWithLogger(path, "100.100.100.7", nil)
	if !errors.Is(releaseErr, os.ErrNotExist) {
		t.Errorf("ReleaseServiceAddress missing-file error = %v, want os.ErrNotExist", releaseErr)
	}
	if _, err := registry.ListServiceAddressesWithLogger(path, nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ListServiceAddresses missing-file error = %v, want os.ErrNotExist", err)
	}
}

func TestListServiceAddressesSortsNumerically(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	writeServiceRegistryFixture(t, path, `{
		"serviceAddresses": {
			"100.100.100.10": "svc:ten",
			"100.100.100.2": {"service":"svc:two","owner":"platform"},
			"100.100.100.1": "svc:one"
		}
	}`+"\n")

	entries, err := registry.ListServiceAddressesWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListServiceAddresses: %v", err)
	}
	want := []string{"100.100.100.1", "100.100.100.2", "100.100.100.10"}
	if len(entries) != len(want) {
		t.Fatalf("ListServiceAddresses = %+v, want %d entries", entries, len(want))
	}
	for i := range want {
		if entries[i].Address != want[i] {
			t.Errorf("entries[%d].Address = %q, want %q", i, entries[i].Address, want[i])
		}
	}
	if entries[1].Owner != "platform" {
		t.Errorf("entries[1] = %+v, want metadata preserved", entries[1])
	}
}

func TestServiceAddressOperationsUseExactSlash32RawKey(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	writeServiceRegistryFixture(t, path, `{
		"serviceAddresses": {
			"100.100.100.7/32": "svc:payments"
		}
	}`+"\n")

	if _, _, _, changed, updateErr := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.7", "payments", registry.ReservationMetadata{Owner: "platform"}, nil,
	); updateErr != nil || !changed {
		t.Fatalf("metadata update through canonical key = changed %t, error %v", changed, updateErr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if !strings.Contains(string(raw), `"100.100.100.7/32"`) || strings.Contains(string(raw), `"100.100.100.7":`) {
		t.Errorf("registry = %s, want only the original /32 raw key", raw)
	}

	if _, changed, releaseErr := registry.ReleaseServiceAddressWithLogger(
		path, "100.100.100.7", nil,
	); releaseErr != nil || !changed {
		t.Fatalf("release through canonical key = changed %t, error %v", changed, releaseErr)
	}
}

func TestReserveServiceAddressRejectsInvalidInputWithoutCreatingFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		service string
		meta    registry.ReservationMetadata
	}{
		{name: "wider prefix", address: "100.100.100.7/24", service: "payments"},
		{name: "IPv6", address: "2001:db8::7", service: "payments"},
		{name: "empty service", address: "100.100.100.7", service: "svc:"},
		{name: "invalid metadata", address: "100.100.100.7", service: "payments",
			meta: registry.ReservationMetadata{Owner: "platform\nops"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			_, _, _, _, err := registry.ReserveServiceAddressWithLogger(path, tc.address, tc.service, tc.meta, nil)
			if _, ok := errors.AsType[*registry.InvalidInputError](err); !ok {
				t.Fatalf("ReserveServiceAddress error = %v, want *InvalidInputError", err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("registry file stat error = %v, want file to remain absent", statErr)
			}
		})
	}
}

func TestServiceAddressEditsPreserveCommentedSiblingBytes(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	protected := "\t\t// payments owns this VIP; keep this note with the entry\n" +
		"\t\t\"100.100.100.7\": \"svc:payments\",\n"
	writeServiceRegistryFixture(t, path, "{\n\t\"serviceAddresses\": {\n"+protected+"\t},\n}\n")

	if _, _, _, _, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.8", "search", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("reserve sibling: %v", err)
	}
	assertRegistryContainsBytes(t, path, protected)

	if _, _, _, _, err := registry.ReserveServiceAddressWithLogger(
		path, "100.100.100.8/32", "svc:search", registry.ReservationMetadata{Owner: "platform"}, nil,
	); err != nil {
		t.Fatalf("update sibling metadata: %v", err)
	}
	assertRegistryContainsBytes(t, path, protected)

	if _, changed, err := registry.ReleaseServiceAddressWithLogger(path, "100.100.100.8", nil); err != nil || !changed {
		t.Fatalf("release sibling = changed %t, error %v", changed, err)
	}
	assertRegistryContainsBytes(t, path, protected)
}

func TestConcurrentServiceAddressReservationsAllLand(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	const reservations = 12

	errCh := make(chan error, reservations)
	var wg sync.WaitGroup
	for i := range reservations {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			address := fmt.Sprintf("100.100.100.%d", index+1)
			service := fmt.Sprintf("service-%d", index+1)
			_, _, _, _, err := registry.ReserveServiceAddressWithLogger(
				path, address, service, registry.ReservationMetadata{}, nil,
			)
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("concurrent ReserveServiceAddress: %v", err)
		}
	}

	entries, err := registry.ListServiceAddressesWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListServiceAddresses: %v", err)
	}
	if len(entries) != reservations {
		t.Fatalf("ListServiceAddresses count = %d, want %d; entries: %+v", len(entries), reservations, entries)
	}
}

func assertRegistryContainsBytes(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if !strings.Contains(string(raw), want) {
		t.Errorf("registry after sibling edit = %q, want protected bytes %q", raw, want)
	}
}

func writeServiceRegistryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing service registry fixture: %v", err)
	}
}

func TestNormalizeServiceAddress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "bare address", address: "100.100.100.7", want: "100.100.100.7"},
		{name: "surrounding whitespace", address: " 100.100.100.7 ", want: "100.100.100.7"},
		{name: "exact slash 32", address: "100.100.100.7/32", want: "100.100.100.7"},
		{name: "minimum address", address: "0.0.0.0", want: "0.0.0.0"},
		{name: "maximum address", address: "255.255.255.255/32", want: "255.255.255.255"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeServiceAddress(tc.address)
			if err != nil {
				t.Fatalf("NormalizeServiceAddress(%q): %v", tc.address, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeServiceAddress(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}

func TestNormalizeServiceAddressRejectsNonIPv4HostInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
	}{
		{name: "blank", address: "  "},
		{name: "wider IPv4 prefix", address: "100.100.100.7/24"},
		{name: "IPv4 prefix too long", address: "100.100.100.7/33"},
		{name: "native IPv6 address", address: "2001:db8::7"},
		{name: "native IPv6 prefix", address: "2001:db8::7/128"},
		{name: "IPv4 mapped IPv6 address", address: "::ffff:100.100.100.7"},
		{name: "IPv4 mapped IPv6 prefix", address: "::ffff:100.100.100.7/128"},
		{name: "zoned address", address: "fe80::1%eth0"},
		{name: "malformed", address: "not-an-address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizeServiceAddress(tc.address)
			if err == nil {
				t.Fatalf("NormalizeServiceAddress(%q) error = nil, want rejection", tc.address)
			}
			if !strings.Contains(err.Error(), "IPv4 host or exact /32") {
				t.Errorf("NormalizeServiceAddress(%q) error = %q, want IPv4 /32 guidance", tc.address, err)
			}
		})
	}
}

func TestNormalizeServiceName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		service string
		want    string
	}{
		{name: "bare", service: "payments", want: "svc:payments"},
		{name: "prefixed", service: "svc:payments", want: "svc:payments"},
		{name: "surrounding whitespace", service: "  payments  ", want: "svc:payments"},
		{name: "space after prefix", service: "svc: payments", want: "svc:payments"},
		{name: "multiple spaces after prefix", service: "svc:  payments", want: "svc:payments"},
		{name: "inner colon is not a prefix", service: "team:payments", want: "svc:team:payments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeServiceName(tc.service)
			if err != nil {
				t.Fatalf("NormalizeServiceName(%q): %v", tc.service, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeServiceName(%q) = %q, want %q", tc.service, got, tc.want)
			}
		})
	}
}

func TestNormalizeServiceNameRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, service := range []string{"", "   ", "svc:", "svc: ", "svc:  ", "svc:svc:payments", "pay\nments", "pay\tments"} {
		t.Run(service, func(t *testing.T) {
			t.Parallel()
			if _, err := registry.NormalizeServiceName(service); err == nil {
				t.Errorf("NormalizeServiceName(%q) error = nil, want rejection", service)
			}
		})
	}
}
