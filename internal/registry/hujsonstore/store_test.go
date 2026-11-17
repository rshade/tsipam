package hujsonstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
	"github.com/rshade/tsipam/internal/registry/storetest"
)

func TestConformance(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) registry.Store {
		t.Helper()
		return hujsonstore.New(t.TempDir(), nil)
	})
}

func TestNotFoundMessages(t *testing.T) {
	t.Parallel()
	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "missing")
		want := fmt.Sprintf("registry directory %q does not exist: no such file or directory", dir)
		assertAbsent(t, dir, "example.ts.net", want)
	})
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		want := fmt.Sprintf(
			"no registry for tailnet %q in %q (a reserve command creates it): no such file or directory",
			"example.ts.net", dir,
		)
		assertAbsent(t, dir, "example.ts.net", want)
	})
}

func assertAbsent(t *testing.T, dir, tailnet, want string) {
	t.Helper()
	store := hujsonstore.New(dir, nil)
	for _, err := range []error{
		store.View(context.Background(), tailnet, registry.TxOptions{}, func(registry.Snapshot) error { return nil }),
		store.Update(context.Background(), tailnet, registry.TxOptions{}, func(registry.Tx) error { return nil }),
	} {
		if err == nil {
			t.Fatal("expected an error")
		}
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Fatalf("error contains a newline: %q", err.Error())
		}
		if !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("errors.Is ErrNotFound = false for %v", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("errors.Is os.ErrNotExist = false for %v", err)
		}
	}
}

func TestLedgerDebugEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := &captureLogger{}
	ledger := registry.NewLedger(hujsonstore.New(dir, logger), logger)
	ctx := context.Background()
	tailnet := "example.ts.net"
	if _, _, _, _, err := ledger.ReserveSiteID(ctx, tailnet, "1", "hq", registry.ReservationMetadata{}); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, logger.events, []string{
		"acquiring registry lock",
		"registry lock acquired",
		"registry created",
		"site-ID reservation written",
		"registry lock released",
	})
	assertCountAttrs(t, logger.events, "registry created", 0)
	logger.reset()
	if _, _, err := ledger.ReleaseSiteID(ctx, tailnet, "1"); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, logger.events, []string{
		"acquiring registry lock",
		"registry lock acquired",
		"registry loaded",
		"site-ID reservation removed",
		"registry lock released",
	})
	assertCountAttrs(t, logger.events, "registry loaded", 1)
}

func assertMessages(t *testing.T, events []logged, want []string) {
	t.Helper()
	if len(events) != len(want) {
		got := make([]string, len(events))
		for i, event := range events {
			got[i] = event.msg
		}
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i, event := range events {
		if event.msg != want[i] {
			t.Fatalf("event %d = %q, want %q", i, event.msg, want[i])
		}
	}
}

func assertCountAttrs(t *testing.T, events []logged, message string, siteIDs int) {
	t.Helper()
	var args []any
	for _, event := range events {
		if event.msg == message {
			args = event.args
		}
	}
	if args == nil {
		t.Fatalf("missing %q", message)
	}
	want := map[string]int{
		"site_id_count":         siteIDs,
		"subnet_pool_count":     0,
		"service_address_count": 0,
		"udp_port_count":        0,
	}
	got := map[string]int{}
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			t.Fatalf("attribute %d is %T, want string", i, args[i])
		}
		value, ok := args[i+1].(int)
		if !ok {
			t.Fatalf("attribute %q is %T, want int", key, args[i+1])
		}
		got[key] = value
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s %s = %d, want %d (attrs %v)", message, key, got[key], value, args)
		}
	}
}

type logged struct {
	msg  string
	args []any
}

type captureLogger struct {
	mu     sync.Mutex
	events []logged
}

func (l *captureLogger) Debug(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, logged{msg: msg, args: append([]any{}, args...)})
}

func (l *captureLogger) Warn(string, ...any) {}

func (l *captureLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = nil
}
