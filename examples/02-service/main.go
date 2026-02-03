// Example 02-service demonstrates a reference HTTP service using
// config + logz + lifecycle + health + httpkit.
//
// Run with defaults:
//
//	go run ./examples/02-service
//
// Then test:
//
//	curl http://localhost:8080/
//	curl http://localhost:8080/healthz
//
// Override port:
//
//	PORT=9090 go run ./examples/02-service
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ai8future/chassis-go/config"
	"github.com/ai8future/chassis-go/health"
	"github.com/ai8future/chassis-go/httpkit"
	"github.com/ai8future/chassis-go/lifecycle"
	"github.com/ai8future/chassis-go/logz"
)

type ServiceConfig struct {
	Port     int    `env:"PORT" default:"8080"`
	LogLevel string `env:"LOG_LEVEL" default:"info"`
}

func main() {
	cfg := config.MustLoad[ServiceConfig]()
	logger := logz.New(cfg.LogLevel)

	// Health checks — a dummy "db" check that always passes.
	checks := map[string]health.Check{
		"db": func(_ context.Context) error {
			return nil
		},
	}

	// Build the router.
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Handler(checks))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message":    "Welcome to chassis-go!",
			"request_id": httpkit.RequestIDFrom(r.Context()),
		})
	})

	// Apply middleware: Recovery -> Logging -> RequestID -> handler.
	var handler http.Handler = mux
	handler = httpkit.RequestID(handler)
	handler = httpkit.Logging(logger)(handler)
	handler = httpkit.Recovery(logger)(handler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	logger.Info("starting server", "addr", addr)

	// Run the HTTP server as a lifecycle component.
	err := lifecycle.Run(context.Background(), func(ctx context.Context) error {
		// Start listening.
		ln, err := net.Listen("tcp", srv.Addr)
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
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			logger.Info("shutting down gracefully")
			return srv.Shutdown(shutdownCtx)
		case err := <-errCh:
			return err
		}
	})

	if err != nil {
		logger.Error("server exited with error", "error", err)
	}
}
