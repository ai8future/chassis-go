package grpckit

import (
	"context"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthChecker is a function that performs health checks and returns an error
// when any check fails. This decouples grpckit from the health package —
// callers typically pass the result of health.All(checks).
type HealthChecker func(ctx context.Context) error

// RegisterHealth registers a grpc.health.v1.Health service on the given gRPC
// server. The Check RPC calls the provided checker and maps the result to a
// gRPC health status: SERVING when the checker returns nil, NOT_SERVING when
// it returns an error.
func RegisterHealth(server *grpc.Server, checker HealthChecker) {
	healthpb.RegisterHealthServer(server, &healthServer{checker: checker})
}

type healthServer struct {
	healthpb.UnimplementedHealthServer
	checker HealthChecker
}

func (h *healthServer) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	err := h.checker(ctx)

	st := healthpb.HealthCheckResponse_SERVING
	if err != nil {
		st = healthpb.HealthCheckResponse_NOT_SERVING
	}

	return &healthpb.HealthCheckResponse{Status: st}, nil
}
