// Package grpckit provides gRPC server utilities including standard
// interceptors for logging, panic recovery, and health-check registration.
package grpckit

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryLogging returns a unary server interceptor that logs the method name,
// duration, and error (if any) for each RPC at Info level.
func UnaryLogging(logger *slog.Logger) grpc.UnaryServerInterceptor {
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
