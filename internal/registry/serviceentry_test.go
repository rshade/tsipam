package registry_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

func TestServiceEntryMarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("metadata free stays a canonical string", func(t *testing.T) {
		t.Parallel()
		got, err := json.Marshal(registry.ServiceEntry{Service: "payments"})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(got) != `"svc:payments"` {
			t.Errorf("Marshal = %s, want canonical bare string", got)
		}
	})

	t.Run("metadata uses service object field", func(t *testing.T) {
		t.Parallel()
		entry := registry.ServiceEntry{
			Service: "svc:payments",
			ReservationMetadata: registry.ReservationMetadata{
				Owner: "platform", Description: "payments VIP", Ticket: "NET-237",
				ReservedAt: "2026-08-31T12:00:00Z",
			},
		}
		got, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var decoded map[string]string
		if decodeErr := json.Unmarshal(got, &decoded); decodeErr != nil {
			t.Fatalf("Unmarshal marshaled object: %v", decodeErr)
		}
		want := map[string]string{
			"service": "svc:payments", "owner": "platform", "description": "payments VIP",
			"ticket": "NET-237", "reservedAt": "2026-08-31T12:00:00Z",
		}
		if !reflect.DeepEqual(decoded, want) {
			t.Errorf("Marshal decoded = %#v, want %#v", decoded, want)
		}
		if _, hasSite := decoded["site"]; hasSite {
			t.Errorf("Marshal decoded = %#v, must not use the site field", decoded)
		}
	})
}

func TestServiceEntryUnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want registry.ServiceEntry
	}{
		{
			name: "bare string is preserved raw", raw: `"payments"`,
			want: registry.ServiceEntry{Service: "payments"},
		},
		{
			name: "object carries metadata and preserves raw service",
			raw:  `{"service":"svc:payments","owner":"platform","ticket":"NET-237"}`,
			want: registry.ServiceEntry{
				Service:             "svc:payments",
				ReservationMetadata: registry.ReservationMetadata{Owner: "platform", Ticket: "NET-237"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got registry.ServiceEntry
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("Unmarshal(%s) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestServiceEntryUnmarshalJSONRejectsMissingServiceAndUnsupportedShapes(t *testing.T) {
	t.Parallel()

	rejectCases := []string{
		`null`,
		`42`,
		`true`,
		`[]`,
	}
	for _, raw := range rejectCases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			var entry registry.ServiceEntry
			if err := json.Unmarshal([]byte(raw), &entry); err == nil {
				t.Errorf("Unmarshal(%s) error = nil, want rejection", raw)
			}
		})
	}

	preservesCases := []struct {
		raw  string
		want string
	}{
		{raw: `{"owner":"platform"}`, want: ""},
		{raw: `{"service":""}`, want: ""},
		{raw: `{"service":"svc:"}`, want: "svc:"},
		{raw: `{"service":"svc:svc:payments"}`, want: "svc:svc:payments"},
	}
	for _, tc := range preservesCases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			var entry registry.ServiceEntry
			if err := json.Unmarshal([]byte(tc.raw), &entry); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.raw, err)
			}
			if entry.Service != tc.want {
				t.Errorf("Unmarshal(%s).Service = %q, want %q (raw preserved)", tc.raw, entry.Service, tc.want)
			}
		})
	}
}

func TestServiceNormalizersReturnTypedInvalidInputErrors(t *testing.T) {
	t.Parallel()

	for _, normalize := range []func() error{
		func() error { _, err := registry.NormalizeServiceAddress("10.0.0.1/24"); return err },
		func() error { _, err := registry.NormalizeServiceName("svc:"); return err },
	} {
		err := normalize()
		var invalid *registry.InvalidInputError
		if !errors.As(err, &invalid) {
			t.Errorf("normalizer error = %v (%T), want *registry.InvalidInputError", err, err)
		}
	}
}
