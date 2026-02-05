// Example 04-full-service demonstrates all chassis 4.0 modules wired together:
// config, logz, lifecycle, errors, secval, metrics, health, httpkit, grpckit, otel.
//
// Copy this directory to start a new service.
//
// Run:
//
//	go run ./examples/04-full-service
//
// Test:
//
//	curl http://localhost:9090/health
//	curl -X POST http://localhost:8080/v1/demo -d '{"input":"hello"}'
//	curl -X POST http://localhost:8080/v1/demo -d '{"__proto__":"evil"}'  # → 400
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/config"
	chassiserrors "github.com/ai8future/chassis-go/errors"
	"github.com/ai8future/chassis-go/guard"
	"github.com/ai8future/chassis-go/health"
	"github.com/ai8future/chassis-go/httpkit"
	"github.com/ai8future/chassis-go/lifecycle"
	"github.com/ai8future/chassis-go/logz"
	"github.com/ai8future/chassis-go/metrics"
	otelinit "github.com/ai8future/chassis-go/otel"
	"github.com/ai8future/chassis-go/secval"
)

type AppConfig struct {
	HTTPPort  int    `env:"HTTP_PORT" default:"8080"`
	AdminPort int    `env:"ADMIN_PORT" default:"9090"`
	LogLevel  string `env:"LOG_LEVEL" default:"info"`
}

func main() {
	chassis.RequireMajor(4)

	cfg := config.MustLoad[AppConfig]()
	logger := logz.New(cfg.LogLevel)
	logger.Info("starting full-service demo", "version", chassis.Version)

	// --- OTel bootstrap (traces + metrics via OTLP) ---
	shutdown := otelinit.Init(otelinit.Config{
		ServiceName:    "demosvc",
		ServiceVersion: chassis.Version,
	})
	defer shutdown(context.Background())

	// --- Metrics ---
	rec := metrics.New("demosvc", logger)

	// --- Health checks ---
	checks := map[string]health.Check{
		"self": func(_ context.Context) error { return nil },
	}

	// --- HTTP handler with secval + errors ---
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/demo", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Body size limit BEFORE secval (prevent DoS)
		r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024) // 2MB max
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeServiceError(w, r, chassiserrors.ValidationError("request body too large"))
			rec.RecordRequest(r.Context(), r.Method, "error", float64(time.Since(start).Milliseconds()), 0)
			return
		}

		// Security validation
		if err := secval.ValidateJSON(body); err != nil {
			writeServiceError(w, r, chassiserrors.ValidationError(err.Error()))
			rec.RecordRequest(r.Context(), r.Method, "error", float64(time.Since(start).Milliseconds()), float64(len(body)))
			return
		}

		// Parse request (second parse — acceptable for bounded input)
		var req struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeServiceError(w, r, chassiserrors.ValidationError("invalid JSON: "+err.Error()))
			rec.RecordRequest(r.Context(), r.Method, "error", float64(time.Since(start).Milliseconds()), float64(len(body)))
			return
		}

		// Business logic (trivial)
		result := fmt.Sprintf("processed: %s", req.Input)

		// Success response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"result": result}); err != nil {
			logger.Error("failed to encode response", "error", err)
		}
		rec.RecordRequest(r.Context(), r.Method, "200", float64(time.Since(start).Milliseconds()), float64(len(body)))
	})

	// Wrap with httpkit middleware: Recovery → Tracing → RequestID → Timeout → Logging → handler
	handler := httpkit.Recovery(logger)(
		httpkit.Tracing()(
			httpkit.RequestID(
				guard.Timeout(10*time.Second)(
					httpkit.Logging(logger)(mux),
				),
			),
		),
	)

	// --- Lifecycle orchestration ---
	err := lifecycle.Run(context.Background(),
		// HTTP server component
		func(ctx context.Context) error {
			addr := fmt.Sprintf(":%d", cfg.HTTPPort)
			srv := &http.Server{Addr: addr, Handler: handler}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			logger.Info("HTTP server listening", "addr", ln.Addr().String())

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()

			select {
			case <-ctx.Done():
				logger.Info("shutting down HTTP server")
				return srv.Shutdown(context.Background())
			case err := <-errCh:
				return err
			}
		},
		// Admin server (health only — metrics flow via OTLP)
		func(ctx context.Context) error {
			adminMux := http.NewServeMux()
			adminMux.Handle("GET /health", health.Handler(checks))

			addr := fmt.Sprintf(":%d", cfg.AdminPort)
			srv := &http.Server{Addr: addr, Handler: adminMux}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			logger.Info("admin server listening", "addr", ln.Addr().String())

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()

			select {
			case <-ctx.Done():
				logger.Info("shutting down admin server")
				return srv.Shutdown(context.Background())
			case err := <-errCh:
				return err
			}
		},
	)

	if err != nil {
		logger.Error("service exited with error", "error", err)
	}
}

// writeServiceError writes a ServiceError as an RFC 9457 Problem Details response.
func writeServiceError(w http.ResponseWriter, r *http.Request, svcErr *chassiserrors.ServiceError) {
	httpkit.JSONProblem(w, r, svcErr)
}
