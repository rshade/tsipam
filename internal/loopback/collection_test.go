package loopback_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	goa "goa.design/goa/v3/pkg"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/ipamhttp"
	"github.com/rshade/tsipam/internal/ipamsvc"
	"github.com/rshade/tsipam/internal/loopback"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

// slowResponse outlasts slowReadTimeout, so the read ends by timeout.
const (
	slowResponse    = 2 * time.Second
	slowReadTimeout = 50 * time.Millisecond
)

// collectionAPI answers for the Tailscale API. The tailnet name in the path
// selects the behavior, so parallel cases share one server.
type collectionAPI struct {
	mu      sync.Mutex
	headers map[string][]http.Header
}

func (a *collectionAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	tailnet := strings.Split(request.URL.Path, "/")[4]
	a.mu.Lock()
	a.headers[tailnet] = append(a.headers[tailnet], request.Header.Clone())
	a.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	body := `{"devices":[],"vipServices":[]}`
	switch tailnet {
	case "unavailable.example.com":
		writer.WriteHeader(http.StatusServiceUnavailable)
		body = `{"message":"secret-body"}`
	case "gateway.example.com":
		writer.WriteHeader(http.StatusBadGateway)
		body = "<html>secret-body</html>"
	case "forbidden.example.com":
		writer.WriteHeader(http.StatusForbidden)
		body = `{"message":"secret-body"}`
	case "slow.example.com":
		select {
		case <-request.Context().Done():
		case <-time.After(slowResponse):
		}
	}
	_, _ = writer.Write([]byte(body))
}

func (a *collectionAPI) seen(tailnet string) []http.Header {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.headers[tailnet])
}

//nolint:gochecknoglobals // tscli reads its base URL from viper's process-global config, so one server serves the package
var fakeAPI = &collectionAPI{headers: map[string][]http.Header{}}

// TestMain points tscli at fakeAPI before any parallel test starts.
func TestMain(m *testing.M) {
	server := httptest.NewServer(fakeAPI)
	viper.Set("base-url", server.URL)
	code := m.Run()
	server.Close()
	os.Exit(code)
}

func planOverLoopback(
	ctx context.Context, t *testing.T, env map[string]string, readTimeout time.Duration, opts ...ipamhttp.Option,
) error {
	t.Helper()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "alias")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	collect := func(ctx context.Context, _ string, logger diagnostics.Logger) (devices.Snapshot, error) {
		if readTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, readTimeout)
			defer cancel()
		}
		return devices.CollectSnapshot(ctx, func(key string) string { return env[key] }, logger)
	}
	handler := ipamhttp.NewHandler(&ipamsvc.Service{
		Ledger: registry.NewLedger(hujsonstore.New(dir, nil), nil), CollectLive: collect,
	}, opts...)
	return loopback.Run(ctx, handler, nil, func(ctx context.Context, client *ipamclient.Client) error {
		_, err := client.Plan()(ctx, &ipam.PlanPayload{
			Tailnet: "alias", LiveTailnet: env["TAILSCALE_TAILNET"],
		})
		return err
	})
}

func TestCollectionFailuresCrossTheLoopback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, tailnet, apiKey string
		readTimeout           time.Duration
		timeout, temporary    bool
		message               string
	}{
		{name: "timeout", tailnet: "slow.example.com", apiKey: "key", readTimeout: slowReadTimeout,
			timeout: true, message: "deadline exceeded"},
		{name: "503", tailnet: "unavailable.example.com", apiKey: "key", temporary: true, message: "HTTP 503"},
		{name: "502 without a JSON body", tailnet: "gateway.example.com", apiKey: "key", temporary: true,
			message: "decoding failed"},
		{name: "403", tailnet: "forbidden.example.com", apiKey: "key", message: "HTTP 403"},
		{name: "missing credentials", tailnet: "unused.example.com", message: "TAILSCALE_API_KEY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := planOverLoopback(t.Context(), t, map[string]string{
				"TAILSCALE_TAILNET": test.tailnet, "TAILSCALE_API_KEY": test.apiKey,
			}, test.readTimeout)
			failure, ok := errors.AsType[*goa.ServiceError](err)
			if !ok || failure.Name != "comparison_failed" {
				t.Fatalf("error = %v, want a comparison_failed service error", err)
			}
			if failure.Timeout != test.timeout || failure.Temporary != test.temporary {
				t.Errorf("timeout = %t, temporary = %t; want %t and %t",
					failure.Timeout, failure.Temporary, test.timeout, test.temporary)
			}
			if !strings.Contains(failure.Message, test.message) || strings.Contains(failure.Message, "secret-body") {
				t.Errorf("message = %q, want %q and no response body", failure.Message, test.message)
			}
		})
	}
}

func TestCollectionSpansAreChildrenOfTheCall(t *testing.T) {
	t.Parallel()
	const tailnet = "traced.example.com"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, command := provider.Tracer("test").Start(t.Context(), "tsipam plan")
	err := planOverLoopback(ctx, t, map[string]string{
		"TAILSCALE_TAILNET": tailnet, "TAILSCALE_API_KEY": "key",
	}, 0, ipamhttp.WithTracerProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	command.End()

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	plan := spans["plan"]
	if plan == nil || plan.Parent().SpanID() != command.SpanContext().SpanID() {
		t.Fatalf("plan span = %v, want a child of the command", plan)
	}
	for _, name := range []string{"list_devices", "list_services"} {
		span := spans[name]
		if span == nil || span.Parent().SpanID() != plan.SpanContext().SpanID() {
			t.Fatalf("%s span = %v, want a child of plan", name, span)
		}
		if len(span.Attributes()) != 0 || len(span.Events()) != 0 {
			t.Errorf("%s carries attributes %v and events %v, want none", name, span.Attributes(), span.Events())
		}
	}

	requests := fakeAPI.seen(tailnet)
	if len(requests) != 2 {
		t.Fatalf("API saw %d requests, want 2", len(requests))
	}
	for _, header := range requests {
		if header.Get("Traceparent") != "" || header.Get("Tracestate") != "" {
			t.Errorf("API request carries a trace header: %v", header)
		}
	}
}
