package storetest_test

import (
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/storetest"
)

func TestMemStoreContract(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) registry.Store {
		t.Helper()
		return storetest.NewMemStore()
	})
}
