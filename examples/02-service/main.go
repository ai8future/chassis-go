// Example 02-service demonstrates a reference gRPC service using
// config + logz + lifecycle + grpckit + health.
//
// Run with defaults:
//
//	go run ./examples/02-service
//
// Then test with grpcurl:
//
//	grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
//
// Override port:
//
//	PORT=50052 go run ./examples/02-service
package main

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/config"
	"github.com/ai8future/chassis-go/grpckit"
	"github.com/ai8future/chassis-go/health"
	"github.com/ai8future/chassis-go/lifecycle"
	"github.com/ai8future/chassis-go/logz"
)

type ServiceConfig struct {
	Port     int    `env:"PORT" default:"50051"`
	LogLevel string `env:"LOG_LEVEL" default:"info"`
}

func main() {
	chassis.RequireMajor(3)
	cfg := config.MustLoad[ServiceConfig]()
	logger := logz.New(cfg.LogLevel)

	// Health checks — a dummy "db" check that always passes.
	checks := map[string]health.Check{
		"db": func(_ context.Context) error {
			return nil
		},
	}

	// Build a health checker function from health.All.
	// health.All returns ([]Result, error); wrap it to satisfy HealthChecker.
	runChecks := health.All(checks)
	checker := func(ctx context.Context) error {
		_, err := runChecks(ctx)
		return err
	}

	// Create the gRPC server with standard interceptors.
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			grpckit.UnaryRecovery(logger),
			grpckit.UnaryLogging(logger),
			grpckit.UnaryMetrics(logger),
		),
		grpc.ChainStreamInterceptor(
			grpckit.StreamRecovery(logger),
			grpckit.StreamLogging(logger),
			grpckit.StreamMetrics(logger),
		),
	)

	// Register the gRPC Health V1 service.
	grpckit.RegisterHealth(srv, checker)

	// Enable server reflection for tools like grpcurl.
	reflection.Register(srv)

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Info("starting gRPC server", "addr", addr)

	// Run the gRPC server as a lifecycle component.
	err := lifecycle.Run(context.Background(), func(ctx context.Context) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		logger.Info("listening", "addr", ln.Addr().String())

		// Serve in background; wait for context cancellation.
		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.Serve(ln)
		}()

		select {
		case <-ctx.Done():
			logger.Info("shutting down gracefully")
			srv.GracefulStop()
			return nil
		case err := <-errCh:
			return err
		}
	})

	if err != nil {
		logger.Error("server exited with error", "error", err)
	}
}
