package backend_test

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/backend"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

func TestParseTakesThePathVerbatim(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "file://.", want: "."},
		{raw: "file://./regs", want: "./regs"},
		{raw: "file:///srv/regs", want: "/srv/regs"},
		{raw: "sqlite:///abs/x.db", want: "/abs/x.db"},
		{raw: "sqlite://./x.db", want: "./x.db"},
		{raw: `sqlite://C:\data\x.db`, want: `C:\data\x.db`},
		{raw: "sqlite://a?b=c#d%20e.db", want: "a?b=c#d%20e.db"},
		{raw: "sqlite://a://b.db", want: "a://b.db"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			selector, err := backend.Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := selector.Locations("local")[0]; got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRejectsInvalidURLs(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"", "./x.db", "sqlite:x.db", "sqlite:/x.db", "sqlite://", "file://", "://x", "SQLITE://x.db",
		"postgres://user:secret@db.example.com/ipam",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := backend.Parse(raw)
			if err == nil {
				t.Fatal("accepted")
			}
			for _, scheme := range []string{"file://", "sqlite://"} {
				if !strings.Contains(err.Error(), scheme) {
					t.Errorf("error = %v, want it to name %s", err, scheme)
				}
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error echoes the URL: %v", err)
			}
		})
	}
}

func TestOpenSelectsTheStoreWithoutTouchingStorage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "tsipam.db")
	sqlite, err := backend.Parse("sqlite://" + path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		selector backend.Selector
		want     registry.Store
	}{
		{name: "file", selector: backend.File(dir), want: &hujsonstore.Store{}},
		{name: "sqlite", selector: sqlite, want: &sqlitestore.Store{}},
	}
	for _, tc := range cases {
		store, openErr := backend.Open(tc.selector, nil)
		if openErr != nil {
			t.Fatalf("%s: %v", tc.name, openErr)
		}
		if got, want := reflect.TypeOf(store), reflect.TypeOf(tc.want); got != want {
			t.Errorf("%s: store is %v, want %v", tc.name, got, want)
		}
		if closer, ok := store.(io.Closer); ok {
			if closeErr := closer.Close(); closeErr != nil {
				t.Errorf("%s: %v", tc.name, closeErr)
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Open created %v", entries)
	}
}

func TestLocationsNameTheRegistryFile(t *testing.T) {
	t.Parallel()
	sqlite, err := backend.Parse("sqlite:///srv/acme-ipam/tsipam.db")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sqlite.Locations("local"), []string{"/srv/acme-ipam/tsipam.db", "tsipam.db"}; !reflect.DeepEqual(
		got, want,
	) {
		t.Errorf("sqlite locations = %v, want %v", got, want)
	}
	want := []string{"regs", filepath.Join("regs", "local.ipam.hujson"), "local.ipam.hujson"}
	if got := backend.File("regs").Locations("local"); !reflect.DeepEqual(got, want) {
		t.Errorf("file locations = %v, want %v", got, want)
	}
}
