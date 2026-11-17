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
	"time"

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
		tailnet := "example.ts.net"
		viewWant := fmt.Sprintf("registry directory %q does not exist: no such file or directory", dir)
		updateWant := fmt.Sprintf(
			"registry directory %q does not exist: acquiring lock: no such file or directory", dir,
		)
		logger := &captureLogger{}
		store := hujsonstore.New(dir, logger)
		viewErr := store.View(context.Background(), tailnet, registry.TxOptions{}, func(registry.Snapshot) error {
			return nil
		})
		assertNotFound(t, viewErr, viewWant)
		updateErr := store.Update(context.Background(), tailnet, registry.TxOptions{}, func(registry.Tx) error {
			return nil
		})
		assertNotFound(t, updateErr, updateWant)
		if len(logger.events) == 0 || logger.events[0].msg != "acquiring registry lock" {
			t.Fatalf("events = %+v, want acquiring registry lock before the missing-directory failure", logger.events)
		}
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
		assertNotFound(t, err, want)
	}
}

func assertNotFound(t *testing.T, err error, want string) {
	t.Helper()
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

func TestUpdateWaitsForLockBeforeNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const tailnet = "example.ts.net"
	path := hujsonstore.Path(dir, tailnet)
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	done := make(chan error, 1)
	go func() {
		done <- store.Update(context.Background(), tailnet, registry.TxOptions{}, func(registry.Tx) error {
			return nil
		})
	}()
	select {
	case err = <-done:
		t.Fatalf("Update returned while the lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err = os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("Update after the file appeared: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Update did not finish after the lock was released")
	}
}

func TestListAndPlanningDebugAttributes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := &captureLogger{}
	ledger := registry.NewLedger(hujsonstore.New(dir, logger), logger)
	ctx := context.Background()
	const tailnet = "example.ts.net"
	if _, _, _, _, err := ledger.ReserveSiteID(ctx, tailnet, "1", "hq", registry.ReservationMetadata{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ledger.ReserveUDPPort(ctx, tailnet, "hq", "41641", "fw"); err != nil {
		t.Fatal(err)
	}
	logger.reset()

	if _, err := ledger.ListSiteIDs(ctx, tailnet); err != nil {
		t.Fatal(err)
	}
	assertLoadedAttrs(t, logger.events, true)
	logger.reset()
	if _, err := ledger.ListUDPPorts(ctx, tailnet, ""); err != nil {
		t.Fatal(err)
	}
	assertLoadedAttrs(t, logger.events, true)
	logger.reset()
	if _, err := ledger.ListSubnetPools(ctx, tailnet); err != nil {
		t.Fatal(err)
	}
	assertLoadedAttrs(t, logger.events, false)
	logger.reset()
	if _, err := ledger.ListServiceAddresses(ctx, tailnet); err != nil {
		t.Fatal(err)
	}
	assertLoadedAttrs(t, logger.events, false)
	logger.reset()
	if _, err := ledger.LoadForPlanning(ctx, tailnet); err != nil {
		t.Fatal(err)
	}
	for _, event := range logger.events {
		if event.msg == "registry loaded" || event.msg == "registry created" {
			t.Fatalf("planning emitted %q", event.msg)
		}
	}
}

func assertLoadedAttrs(t *testing.T, events []logged, includeUDP bool) {
	t.Helper()
	const message = "registry loaded"
	var args []any
	for _, event := range events {
		if event.msg == message {
			args = event.args
		}
	}
	if args == nil {
		t.Fatalf("missing %q", message)
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
	for _, key := range []string{"site_id_count", "subnet_pool_count", "service_address_count"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s missing %s in %v", message, key, args)
		}
	}
	_, hasUDP := got["udp_port_count"]
	if hasUDP != includeUDP {
		t.Fatalf("%s udp_port_count present = %t, want %t (attrs %v)", message, hasUDP, includeUDP, args)
	}
}
