package ipamsvc_test

import (
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

func testLedger(dir string) *registry.Ledger {
	return registry.NewLedger(hujsonstore.New(dir, nil), nil)
}
