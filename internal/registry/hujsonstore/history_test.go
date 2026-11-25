package hujsonstore_test

import (
	"errors"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

func TestHistoryIsUnsupported(t *testing.T) {
	t.Parallel()
	ledger := registry.NewLedger(hujsonstore.New(t.TempDir(), nil), nil)
	_, err := ledger.History(t.Context(), "example.ts.net", registry.HistoryFilter{Limit: 1})
	if !errors.Is(err, registry.ErrHistoryUnsupported) {
		t.Fatalf("error = %v, want ErrHistoryUnsupported", err)
	}
}
