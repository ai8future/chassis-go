// Command demo-shutdown verifies that lifecycle.Run handles SIGTERM correctly,
// cancels Contexts, and drains gracefully. This is the validation binary
// described in the implementation roadmap (Step 4).
//
// Run it and then send SIGTERM or press Ctrl+C:
//
//	go run ./cmd/demo-shutdown
//	# In another terminal:
//	kill -TERM <pid>
//
// Expected output:
//
//	{"level":"INFO","msg":"worker started","worker":1}
//	{"level":"INFO","msg":"worker started","worker":2}
//	... (workers log every second)
//	{"level":"INFO","msg":"signal received, shutting down","worker":1}
//	{"level":"INFO","msg":"worker drained","worker":1}
//	{"level":"INFO","msg":"signal received, shutting down","worker":2}
//	{"level":"INFO","msg":"worker drained","worker":2}
//	{"level":"INFO","msg":"clean shutdown complete"}
package main

import (
	"context"
	"fmt"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/lifecycle"
	"github.com/ai8future/chassis-go/v5/logz"
)

func main() {
	chassis.RequireMajor(5)
	logger := logz.New("info")

	logger.Info("demo-shutdown starting, send SIGTERM or press Ctrl+C to test graceful shutdown")

	// Create two worker components that simulate long-running work.
	worker := func(id int) lifecycle.Component {
		return func(ctx context.Context) error {
			logger.Info(fmt.Sprintf("worker %d started", id))

			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					logger.Info(fmt.Sprintf("worker %d received shutdown signal, draining", id))
					// Simulate drain time.
					time.Sleep(500 * time.Millisecond)
					logger.Info(fmt.Sprintf("worker %d drained", id))
					return nil
				case <-ticker.C:
					logger.Info(fmt.Sprintf("worker %d tick", id))
				}
			}
		}
	}

	err := lifecycle.Run(context.Background(), worker(1), worker(2))
	if err != nil {
		logger.Error("shutdown error", "error", err)
	} else {
		logger.Info("clean shutdown complete")
	}
}
