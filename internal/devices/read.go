package devices

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/rshade/tsipam/internal/devices"

// read runs one Tailscale API read under APITimeout, inside a span named for
// the operation. The span carries no attributes: the request URL and the
// response hold tailnet identities. describe words the failure.
func read[T any](
	ctx context.Context,
	operation string,
	describe func(error) error,
	fn func(context.Context) (T, error),
) (T, error) {
	tracer := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, operation, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	// tscli's client has no request timeout of its own.
	ctx, cancel := context.WithTimeout(ctx, APITimeout)
	defer cancel()

	var status int
	result, err := fn(context.WithValue(ctx, statusKey{}, &status))
	if err != nil {
		span.SetStatus(codes.Error, "")
		return result, &CollectionError{
			Err: describe(err), Timeout: isTimeout(err), Temporary: isServerError(status),
		}
	}
	return result, nil
}

type statusKey struct{}

// statusTransport reports each response's HTTP status to the read that asked.
// The API client drops the status of an error response whose body is not JSON,
// as a gateway's usually is.
type statusTransport struct {
	base http.RoundTripper
}

func (t statusTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if status, ok := request.Context().Value(statusKey{}).(*int); ok && response != nil {
		*status = response.StatusCode
	}
	return response, err
}
