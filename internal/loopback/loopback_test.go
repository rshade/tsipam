package loopback_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/loopback"
)

func TestRunServesOneRequestOverTheGeneratedClient(t *testing.T) {
	t.Parallel()
	registryDir := t.TempDir()

	err := loopback.RunWithLogger(
		context.Background(), registryDir, nil,
		func(ctx context.Context, client *ipamclient.Client) error {
			res, callErr := client.ReserveSiteID()(
				ctx,
				&ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "1", Site: "hq-vpc"},
			)
			if callErr != nil {
				return callErr
			}
			result, ok := res.(*ipam.ReserveSiteIDResult)
			if !ok {
				t.Fatalf("ReserveSiteID result type = %T, want *ipam.ReserveSiteIDResult", res)
			}
			if result.ID != "1" || result.Site != "hq-vpc" || !result.Changed {
				t.Errorf("ReserveSiteID result = %+v, want {ID:1 Site:hq-vpc Changed:true}", result)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(registryDir, "acme.ipam.hujson")); statErr != nil {
		t.Errorf("registry file was not written: %v", statErr)
	}
}

func TestRunLeavesNoSocketDirectoryBehind(t *testing.T) {
	// Not t.Parallel(): TMPDIR is process environment, so a parallel sibling
	// creating its own socket dir under a shared TMPDIR would race this
	// test's before/after directory listing. Redirecting TMPDIR to a
	// TempDir() private to this test is what makes the listing meaningful at
	// all — os.MkdirTemp("", ...) resolves its parent through os.TempDir(),
	// which honors $TMPDIR on Unix.
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	registryDir := t.TempDir()

	err := loopback.RunWithLogger(
		context.Background(), registryDir, nil,
		func(ctx context.Context, client *ipamclient.Client) error {
			payload := &ipam.ReserveSiteIDPayload{Tailnet: "local", ID: "1", Site: "hq-vpc"}
			_, callErr := client.ReserveSiteID()(ctx, payload)
			return callErr
		})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	leftover, err := filepath.Glob(filepath.Join(tmpDir, "tsipam-*"))
	if err != nil {
		t.Fatalf("globbing after Run: %v", err)
	}
	if len(leftover) != 0 {
		t.Errorf("socket temp dirs left behind after Run = %v, want none", leftover)
	}
}
