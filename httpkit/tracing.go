package httpkit

import (
	"fmt"
	"net/http"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/internal/otelutil"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ai8future/chassis-go/v5/httpkit"

var getHTTPDurationHistogram = otelutil.LazyHistogram(
	tracerName,
	"http.server.request.duration",
	metric.WithUnit("s"),
	metric.WithDescription("Duration of HTTP server requests"),
)

// Tracing returns middleware that creates OpenTelemetry server spans for each
// HTTP request. It extracts incoming trace context from request headers using
// the globally configured propagator and records HTTP semantic convention
// attributes (method, path, status code). Responses with 5xx status codes
// cause the span status to be set to Error. It also records the
// http.server.request.duration metric as an OTel histogram.
func Tracing() func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			propagator := otelapi.GetTextMapPropagator()
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			tracer := otelapi.GetTracerProvider().Tracer(tracerName)
			spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)

			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
				),
			)
			defer span.End()

			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))
			duration := time.Since(start).Seconds()

			span.SetAttributes(semconv.HTTPResponseStatusCode(rw.statusCode))
			if rw.statusCode >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rw.statusCode))
			}

			if h := getHTTPDurationHistogram(); h != nil {
				h.Record(ctx, duration,
					metric.WithAttributes(
						semconv.HTTPRequestMethodKey.String(r.Method),
						semconv.HTTPResponseStatusCode(rw.statusCode),
					),
				)
			}
		})
	}
}
