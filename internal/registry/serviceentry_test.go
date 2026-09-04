package registry_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func TestNormalizeServiceNameRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid UTF-8 byte", input: "svc\xff"},
		{name: "lone surrogate", input: "svc\xed\xa0\x80"},
		{name: "invalid UTF-8 after svc prefix", input: "svc:svc\xff"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizeServiceName(test.input)
			if err == nil {
				t.Errorf("NormalizeServiceName(%q) error = nil, want an error containing UTF-8", test.input)
			}
			if !strings.Contains(err.Error(), "UTF-8") {
				t.Errorf("NormalizeServiceName(%q) error = %q, want it to contain UTF-8", test.input, err)
			}
		})
	}
}

func TestNormalizeServiceNameAcceptsValidMultiByteUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "umlaut", input: "büro", want: "svc:büro"},
		{name: "umlaut with svc prefix", input: "svc:büro", want: "svc:büro"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeServiceName(test.input)
			if err != nil {
				t.Errorf("NormalizeServiceName(%q) error = %v, want nil", test.input, err)
			}
			if got != test.want {
				t.Errorf("NormalizeServiceName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// TestNormalizeServiceNamePrefixBehaviorUnchanged pins the existing svc:
// selector handling (single prefix accepted, doubled prefix rejected) so the
// new UTF-8 check above does not shift where in the function that behavior
// lives.
func TestNormalizeServiceNamePrefixBehaviorUnchanged(t *testing.T) {
	t.Parallel()

	t.Run("bare name gains the svc prefix", func(t *testing.T) {
		t.Parallel()
		got, err := registry.NormalizeServiceName("payments")
		if err != nil || got != "svc:payments" {
			t.Errorf("NormalizeServiceName(%q) = (%q, %v), want (%q, nil)", "payments", got, err, "svc:payments")
		}
	})

	t.Run("svc-prefixed name is preserved", func(t *testing.T) {
		t.Parallel()
		got, err := registry.NormalizeServiceName("svc:payments")
		if err != nil || got != "svc:payments" {
			t.Errorf("NormalizeServiceName(%q) = (%q, %v), want (%q, nil)", "svc:payments", got, err, "svc:payments")
		}
	})

	t.Run("doubled prefix is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := registry.NormalizeServiceName("svc:svc:payments"); err == nil {
			t.Error("NormalizeServiceName(\"svc:svc:payments\") error = nil, want rejection")
		}
	})
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
