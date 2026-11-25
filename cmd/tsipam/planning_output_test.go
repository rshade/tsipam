package main

import (
	"testing"

	"github.com/rshade/tsipam/internal/planner"
)

func TestComparisonKindsMatchPlanner(t *testing.T) {
	t.Parallel()
	for got, want := range map[string]string{
		comparisonKindSiteID:         planner.KindSiteID,
		comparisonKindServiceAddress: planner.KindServiceAddress,
	} {
		if got != want {
			t.Errorf("comparison kind = %q, planner reports %q", got, want)
		}
	}
}
