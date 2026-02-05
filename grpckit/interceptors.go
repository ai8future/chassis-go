// Package grpckit provides gRPC server utilities including standard
// interceptors for logging, panic recovery, and health-check registration.
package grpckit

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const tracerName = "github.com/ai8future/chassis-go/grpckit"

var (
	rpcDurationOnce      sync.Once
	rpcDurationHistogram metric.Float64Histogram
)

func getRPCDurationHistogram() metric.Float64Histogram {
	rpcDurationOnce.Do(func() {
		meter := otelapi.GetMeterProvider().Meter(tracerName)
		var err error
		rpcDurationHistogram, err = meter.Float64Histogram(
			"rpc.server.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Duration of gRPC server requests"),
		)
		if err != nil {
			otelapi.Handle(err)
		}
	})
	return rpcDurationHistogram
}

// UnaryLogging returns a unary server interceptor that logs the method name,
// duration, and error (if any) for each RPC at Info level.
func UnaryLogging(logger *slog.Logger) grpc.UnaryServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.Duration("duration", duration),
		}
		if err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
		}

		logger.LogAttrs(ctx, slog.LevelInfo, "unary RPC", attrs...)
		return resp, err
	}
}

// UnaryRecovery returns a unary server interceptor that catches panics in the
// handler, logs them at Error level, and returns a codes.Internal gRPC status.
func UnaryRecovery(logger *slog.Logger) grpc.UnaryServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.LogAttrs(ctx, slog.LevelError, "panic recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// StreamLogging returns a stream server interceptor that logs the method name
// and duration for each stream RPC at Info level.
func StreamLogging(logger *slog.Logger) grpc.StreamServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		err := handler(srv, ss)
		duration := time.Since(start)

		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.Duration("duration", duration),
		}
		if err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
		}

		logger.LogAttrs(ctx(ss), slog.LevelInfo, "stream RPC", attrs...)
		return err
	}
}

// StreamRecovery returns a stream server interceptor that catches panics in the
// handler, logs them at Error level, and returns a codes.Internal gRPC status.
func StreamRecovery(logger *slog.Logger) grpc.StreamServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.LogAttrs(ctx(ss), slog.LevelError, "panic recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, ss)
	}
}

// ctx extracts the context from a ServerStream for logging purposes.
func ctx(ss grpc.ServerStream) context.Context {
	return ss.Context()
}

// UnaryMetrics returns a unary server interceptor that records rpc.server.duration
// as an OTel histogram with method and status code attributes.
func UnaryMetrics() grpc.UnaryServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		grpcCode := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				grpcCode = st.Code()
			}
		}

		if h := getRPCDurationHistogram(); h != nil {
			h.Record(ctx, duration,
				metric.WithAttributes(
					attribute.String("rpc.method", info.FullMethod),
					attribute.String("rpc.system", "grpc"),
					attribute.Int("rpc.grpc.status_code", int(grpcCode)),
				),
			)
		}

		return resp, err
	}
}

// StreamMetrics returns a stream server interceptor that records rpc.server.duration
// as an OTel histogram with method and status code attributes.
func StreamMetrics() grpc.StreamServerInterceptor {
	chassis.AssertVersionChecked()
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		err := handler(srv, ss)
		duration := time.Since(start).Seconds()

		grpcCode := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				grpcCode = st.Code()
			}
		}

		if h := getRPCDurationHistogram(); h != nil {
			h.Record(ctx(ss), duration,
				metric.WithAttributes(
					attribute.String("rpc.method", info.FullMethod),
					attribute.String("rpc.system", "grpc"),
					attribute.Int("rpc.grpc.status_code", int(grpcCode)),
				),
			)
		}

		return err
	}
}

// metadataCarrier adapts gRPC incoming metadata to the OTel TextMapCarrier
// interface so that propagation.Extract can read W3C traceparent headers.
type metadataCarrier struct {
	md metadata.MD
}

func (c metadataCarrier) Get(key string) string {
	vals := c.md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (c metadataCarrier) Set(key, value string) {
	c.md.Set(key, value)
}

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.md))
	for k := range c.md {
		keys = append(keys, k)
	}
	return keys
}

// extractTraceContext extracts W3C trace context from incoming gRPC metadata
// into the Go context using the global OTel text map propagator.
func extractTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	return otelapi.GetTextMapPropagator().Extract(ctx, metadataCarrier{md: md})
}

// UnaryTracing returns a unary server interceptor that creates an OpenTelemetry
// span for each RPC, recording the method name, gRPC status code, and any error.
// It extracts incoming W3C trace context from gRPC metadata so that spans are
// parented correctly in distributed traces.
func UnaryTracing() grpc.UnaryServerInterceptor {
	chassis.AssertVersionChecked()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = extractTraceContext(ctx)
		tracer := otelapi.GetTracerProvider().Tracer(tracerName)
		ctx, span := tracer.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", info.FullMethod),
			),
		)
		defer span.End()

		resp, err := handler(ctx, req)
		if err != nil {
			st, _ := status.FromError(err)
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", int(st.Code())))
			span.SetStatus(otelcodes.Error, st.Message())
		} else {
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", int(codes.OK)))
		}
		return resp, err
	}
}

// StreamTracing returns a stream server interceptor that creates an OpenTelemetry
// span for each stream RPC, recording the method name, gRPC status code, and any error.
// It extracts incoming W3C trace context from gRPC metadata so that spans are
// parented correctly in distributed traces.
func StreamTracing() grpc.StreamServerInterceptor {
	chassis.AssertVersionChecked()
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		sctx := extractTraceContext(ss.Context())
		tracer := otelapi.GetTracerProvider().Tracer(tracerName)
		sctx, span := tracer.Start(sctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.method", info.FullMethod),
			),
		)
		defer span.End()

		wrapped := &tracedStream{ServerStream: ss, ctx: sctx}
		err := handler(srv, wrapped)
		if err != nil {
			st, _ := status.FromError(err)
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", int(st.Code())))
			span.SetStatus(otelcodes.Error, st.Message())
		} else {
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", int(codes.OK)))
		}
		return err
	}
}

// tracedStream wraps a grpc.ServerStream to override its Context with one
// that carries the tracing span.
type tracedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tracedStream) Context() context.Context {
	return s.ctx
}
