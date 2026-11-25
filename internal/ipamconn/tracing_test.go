package ipamconn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/ipamconn"
	"github.com/rshade/tsipam/internal/ipamhttp"
	"github.com/rshade/tsipam/internal/loopback"
)

type transport func(context.Context, *testing.T, http.Handler, func(context.Context, *ipamclient.Client) error) error

func overLoopback(
	ctx context.Context, t *testing.T, handler http.Handler,
	fn func(context.Context, *ipamclient.Client) error,
) error {
	t.Helper()
	return loopback.Run(ctx, handler, nil, fn)
}

func overHTTP(
	ctx context.Context, t *testing.T, handler http.Handler,
	fn func(context.Context, *ipamclient.Client) error,
) error {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	address, err := url.Parse(server.URL)
	if err != nil {
		return err
	}
	return fn(ctx, ipamconn.NewClient(address.Scheme, address.Host, server.Client()))
}

// TestServiceCallsAreChildSpans covers both ways a command reaches the service:
// the loopback socket and a listening server.
func TestServiceCallsAreChildSpans(t *testing.T) {
	t.Parallel()
	for name, send := range map[string]transport{"loopback": overLoopback, "httptest": overHTTP} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			ctx, command := provider.Tracer("test").Start(t.Context(), "tsipam site-id reserve")
			handler := ipamhttp.NewHandler(fakeService{}, ipamhttp.WithTracerProvider(provider))
			if err := send(ctx, t, handler, reserveThenCollide); err != nil {
				t.Fatal(err)
			}
			command.End()

			spans := recorder.Ended()
			if len(spans) != 3 {
				t.Fatalf("recorded %d spans, want two calls and the command", len(spans))
			}
			for _, span := range spans[:2] {
				assertChildSpan(t, span, command.SpanContext())
			}
			if got := spans[0].Status().Code; got != codes.Unset {
				t.Errorf("successful call status = %v, want unset", got)
			}
			want := sdktrace.Status{Code: codes.Error, Description: "collision"}
			if got := spans[1].Status(); got != want {
				t.Errorf("refused call status = %+v, want %+v", got, want)
			}
		})
	}
}

// reserveThenCollide makes one call that succeeds and one that is refused.
func reserveThenCollide(ctx context.Context, client *ipamclient.Client) error {
	call := client.ReserveSiteID()
	if _, err := call(ctx, &ipam.ReserveSiteIDPayload{
		Tailnet: "private-tailnet", ID: "7", Site: "private-site",
	}); err != nil {
		return err
	}
	if _, err := call(ctx, &ipam.ReserveSiteIDPayload{
		Tailnet: "private-tailnet", ID: "7", Site: "taken",
	}); err == nil {
		return errors.New("collision did not fail")
	}
	return nil
}

func assertChildSpan(t *testing.T, span sdktrace.ReadOnlySpan, parent trace.SpanContext) {
	t.Helper()
	if span.Name() != "reserve_site_id" {
		t.Errorf("span name = %q, want the service method", span.Name())
	}
	if span.Parent().SpanID() != parent.SpanID() || span.SpanContext().TraceID() != parent.TraceID() {
		t.Errorf("span parent = %v, want the command span %v", span.Parent(), parent)
	}
	if len(span.Attributes()) != 0 || len(span.Events()) != 0 {
		t.Errorf("span carries attributes %v and events %v, want none", span.Attributes(), span.Events())
	}
	if strings.Contains(span.Status().Description, "site ID") {
		t.Errorf("span status holds error text: %q", span.Status().Description)
	}
}

func TestClientSendsNoTraceHeaderWithoutASpan(t *testing.T) {
	t.Parallel()
	var header string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header = request.Header.Get("Traceparent")
		ipamhttp.NewHandler(fakeService{}).ServeHTTP(writer, request)
	})
	err := overHTTP(t.Context(), t, handler, func(ctx context.Context, client *ipamclient.Client) error {
		_, err := client.ReserveSiteID()(ctx, &ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "7", Site: "hq"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		t.Errorf("traceparent = %q, want none outside a trace", header)
	}
}
