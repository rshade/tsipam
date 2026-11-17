package hujsonstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

const registryTailnet = "example.ts.net"

func regPath(t testing.TB) string {
	t.Helper()
	return hujsonstore.Path(t.TempDir(), registryTailnet)
}

func ledgerAt(path string) *registry.Ledger {
	return registry.NewLedger(hujsonstore.New(filepath.Dir(path), nil), nil)
}

func regCtx() context.Context { return context.Background() }
