package ipamhttp

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	goa "goa.design/goa/v3/pkg"
)

const tracerName = "github.com/rshade/tsipam/internal/ipamhttp"

// extractTrace continues the caller's trace. The header is used only for
// correlation, never for identity or authorization.
func extractTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := propagation.TraceContext{}.Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// traceEndpoint runs each service call inside a span named for the method. The
// span holds no payload, URL, or error text, because all of them carry tailnet
// identities.
func traceEndpoint(provider trace.TracerProvider) func(goa.Endpoint) goa.Endpoint {
	tracer := provider.Tracer(tracerName)
	return func(next goa.Endpoint) goa.Endpoint {
		return func(ctx context.Context, request any) (any, error) {
			method, _ := ctx.Value(goa.MethodKey).(string)
			ctx, span := tracer.Start(ctx, method, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
			result, err := next(ctx, request)
			if err != nil {
				span.SetStatus(codes.Error, errorName(err))
			}
			return result, err
		}
	}
}

// errorName is the goa name of a declared error, or "" for any other error.
func errorName(err error) string {
	if serviceErr, ok := errors.AsType[*goa.ServiceError](err); ok {
		return serviceErr.Name
	}
	return ""
}
