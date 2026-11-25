package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

type historyDocument struct {
	Command string         `json:"command"`
	Status  string         `json:"status"`
	Tailnet string         `json:"tailnet"`
	Events  *[]eventOutput `json:"events"`
}

func runHistoryJSON(t *testing.T, env map[string]string, args ...string) (historyDocument, string) {
	t.Helper()
	got := runBackendCLI(t, env, append([]string{commandRegistry, "history", "--format", formatJSON}, args...)...)
	if got.code != exitOK {
		t.Fatalf("history = %+v", got)
	}
	var document historyDocument
	if err := decodeData([]byte(got.stdout), &document); err != nil {
		t.Fatalf("decode %q: %v", got.stdout, err)
	}
	return document, got.stdout
}

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	if got := runBackendCLI(t, nil, args...); got.code != exitOK {
		t.Fatalf("%v = %+v", args, got)
	}
}

func eventKeys(events []eventOutput) []string {
	keys := []string{}
	for _, event := range events {
		keys = append(keys, event.Action+" "+event.Key)
	}
	return keys
}

func TestHistoryRecordsChangesOnly(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	mustRun(t, "site-id", "reserve", "7", "hq-vpc", "--ticket", "OPS-1", "--registry", url)
	mustRun(t, "site-id", "reserve", "7", "hq-vpc", "--registry", url)
	if got := runBackendCLI(
		t, nil, "site-id", "reserve", "7", "branch-office", "--registry", url,
	); got.code != exitFailure {
		t.Fatalf("collision = %+v", got)
	}
	mustRun(t, "site-id", "release", "7", "--yes", "--registry", url)

	document, stdout := runHistoryJSON(t, nil, "--key", "7", "--registry", url)
	if document.Command != commandRegistryHistory || document.Status != statusOK || document.Events == nil {
		t.Fatalf("document = %+v", document)
	}
	events := *document.Events
	if got, want := eventKeys(events), []string{"release 7", "reserve 7"}; !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	reserved := events[1]
	if reserved.Kind != "site_id" || reserved.Before != nil || reserved.After == nil ||
		reserved.After.Target != "hq-vpc" || reserved.After.Ticket != "OPS-1" || reserved.Actor != nil {
		t.Errorf("reserve = %+v", reserved)
	}
	for _, want := range []string{`"actor":null`, `"before":null`, `"after":null`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %s: %s", want, stdout)
		}
	}
}

func TestHistoryHumanOutput(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	stamp := []string{"--reserved-at", "2026-03-01T00:00:00Z", "--registry", url}
	mustRun(t, append([]string{"subnet", "reserve", "10.20.0.0/24", "hq-vpc", "--ticket", "OPS-1"}, stamp...)...)
	mustRun(t, append([]string{"subnet", "reserve", "10.20.0.0/24", "hq-vpc", "--ticket", "OPS-2"}, stamp...)...)
	mustRun(t, "port", "reserve", "41642", "hq-fw01", "--site", "hq-vpc", "--registry", url)

	got := runBackendCLI(t, nil, commandRegistry, "history", "--registry", url)
	if got.code != exitOK || got.stderr != "" {
		t.Fatalf("history = %+v", got)
	}
	want := []string{
		"reserve\tudp_port\t41642\thq-vpc\t-\thq-fw01\t-",
		"reserve\tsubnet_pool\t10.20.0.0/24\t-\thq-vpc ticket=OPS-1 reserved=2026-03-01T00:00:00Z" +
			"\thq-vpc ticket=OPS-2 reserved=2026-03-01T00:00:00Z\t-",
		"reserve\tsubnet_pool\t10.20.0.0/24\t-\t-\thq-vpc ticket=OPS-1 reserved=2026-03-01T00:00:00Z\t-",
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	for i, line := range lines {
		_, lines[i], _ = strings.Cut(line, "\t")
	}
	if !slices.Equal(lines, want) {
		t.Errorf("lines after the time column = %q\nwant %q", lines, want)
	}
}

func TestHistoryFilters(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	mustRun(t, "site-id", "reserve", "7", "hq-vpc", "--registry", url)
	mustRun(t, "subnet", "reserve", "10.20.0.0/24", "hq-vpc", "--registry", url)
	mustRun(t, "service", "reserve", "100.100.100.8", "dashboard", "--registry", url)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"all", nil, []string{"reserve 100.100.100.8", "reserve 10.20.0.0/24", "reserve 7"}},
		{
			"kind and since", []string{"--kind", "subnet_pool", "--since", "2026-01-01T00:00:00Z"},
			[]string{"reserve 10.20.0.0/24"},
		},
		{"limit", []string{"--limit", "1"}, []string{"reserve 100.100.100.8"}},
		{"nothing matches", []string{"--since", "2999-01-01T00:00:00Z"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			document, stdout := runHistoryJSON(t, nil, append(tc.args, "--registry", url)...)
			if document.Events == nil {
				t.Fatalf("events is absent: %s", stdout)
			}
			if got := eventKeys(*document.Events); !slices.Equal(got, tc.want) {
				t.Errorf("events = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHistoryRefusals(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	mustRun(t, "site-id", "reserve", "7", "hq-vpc", "--registry", url)
	dir := t.TempDir()
	mustRun(t, "site-id", "reserve", "7", "hq-vpc", "--registry-dir", dir)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown kind", []string{"--kind", "vlan", "--registry", url}, "--kind"},
		{"bad since", []string{"--since", "yesterday", "--registry", url}, "--since"},
		{"zero limit", []string{"--limit", "0", "--registry", url}, "--limit must be at least 1"},
		{"positional argument", []string{"7", "--registry", url}, "unknown command"},
		{
			"file backend", []string{"--registry-dir", dir},
			"registry history requires the sqlite backend; a file registry's history lives in version control",
		},
		{"file backend without a registry", []string{"--registry-dir", t.TempDir()}, "requires the sqlite backend"},
		{"missing registry", []string{"--registry", url, "--tailnet", "staging"}, "no registry for tailnet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runBackendCLI(t, nil, append([]string{commandRegistry, "history"}, tc.args...)...)
			assertUsageFailure(t, got.code, got.stdout)
			wantCode := codeValidationError
			if tc.name == "missing registry" {
				wantCode = codeRegistryFailed
			}
			if !strings.Contains(got.stderr, tc.want) || got.failure != wantCode {
				t.Errorf("run = %+v, want %s with %q", got, wantCode, tc.want)
			}
			if advised := strings.Contains(got.stderr, fixHistoryUnsupported); advised !=
				strings.HasPrefix(tc.name, "file backend") {
				t.Errorf("stderr = %q, advice to select sqlite = %t", got.stderr, advised)
			}
		})
	}
}

func TestHistoryIsSanitized(t *testing.T) {
	t.Parallel()
	url, path := sqliteURL(t)
	store, err := sqlitestore.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ledger := registry.NewLedger(store, nil)
	ctx := registry.WithActor(t.Context(), "user:carol@example.com")
	metadata := registry.ReservationMetadata{Owner: "alice@example.com"}
	const tailnet = "acme-alias"
	if _, _, _, _, err = ledger.ReserveSiteID(ctx, tailnet, "7", "acme-hq", metadata); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err = ledger.ReserveSubnetPool(ctx, tailnet, "10.20.0.0/24", "acme-branch", metadata); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err = ledger.ReserveServiceAddress(
		ctx, tailnet, "100.100.100.8", "acme-dashboard", metadata,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err = ledger.ReserveUDPPort(ctx, tailnet, "acme-lab", "41642", "acme-fw01"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	private := []string{
		"acme-hq", "acme-branch", "acme-dashboard", "acme-lab", "acme-fw01", "acme-alias",
		"alice@example.com", "carol@example.com", "10.20.0.0", "100.100.100.8",
	}
	for _, format := range []string{formatHuman, formatJSON} {
		got := runBackendCLI(t, map[string]string{
			sanitize.LevelEnv: string(sanitize.Identity), sanitize.KeyEnv: testSanitizeKey,
		}, commandRegistry, "history", "--tailnet", tailnet, "--registry", url, "--format", format)
		if got.code != exitOK {
			t.Fatalf("%s = %+v", format, got)
		}
		for _, value := range private {
			if strings.Contains(got.stdout, value) {
				t.Errorf("%s output leaks %q: %s", format, value, got.stdout)
			}
		}
		for _, visible := range []string{"site_id", "subnet_pool", "41642"} {
			if !strings.Contains(got.stdout, visible) {
				t.Errorf("%s output lost %q: %s", format, visible, got.stdout)
			}
		}
	}
}
