package grpckit

import (
	"context"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ai8future/chassis-go/health"
)

// RegisterHealth registers a grpc.health.v1.Health service on the given gRPC
// server. The Check RPC runs all provided health checks via health.All and
// maps the aggregate result to a gRPC health status: SERVING when all checks
// pass, NOT_SERVING when any check fails.
func RegisterHealth(server *grpc.Server, checks map[string]health.Check) {
	run := health.All(checks)
	healthpb.RegisterHealthServer(server, &healthServer{run: run})
}

type healthServer struct {
	healthpb.UnimplementedHealthServer
	run func(ctx context.Context) ([]health.Result, error)
}

func (h *healthServer) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	_, err := h.run(ctx)

	st := healthpb.HealthCheckResponse_SERVING
	if err != nil {
		st = healthpb.HealthCheckResponse_NOT_SERVING
	}

	return &healthpb.HealthCheckResponse{Status: st}, nil
}
