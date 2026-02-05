// Example 03-client demonstrates the call package with retries and
// circuit breaking.
//
// Run with defaults (hits httpbin.org):
//
//	go run ./examples/03-client
//
// Point at a local service:
//
//	TARGET_URL=http://localhost:8080/ go run ./examples/03-client
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/call"
	"github.com/ai8future/chassis-go/config"
	"github.com/ai8future/chassis-go/logz"
)

type ClientConfig struct {
	TargetURL string `env:"TARGET_URL" default:"https://httpbin.org/status/200"`
	LogLevel  string `env:"LOG_LEVEL" default:"info"`
}

func main() {
	chassis.RequireMajor(4)
	cfg := config.MustLoad[ClientConfig]()
	logger := logz.New(cfg.LogLevel)

	// Build a resilient HTTP client with retry and circuit breaker.
	client := call.New(
		call.WithTimeout(5*time.Second),
		call.WithRetry(3, 500*time.Millisecond),
		call.WithCircuitBreaker("demo", 5, 30*time.Second),
	)

	logger.Info("starting client demo",
		"target", cfg.TargetURL,
	)

	// Make a few requests to demonstrate resilience features.
	for i := range 3 {
		req, err := http.NewRequest(http.MethodGet, cfg.TargetURL, nil)
		if err != nil {
			logger.Error("failed to create request", "error", err)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			logger.Error("request failed",
				"attempt", i+1,
				"error", err,
			)
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB max
		resp.Body.Close()
		if err != nil {
			logger.Error("failed to read response body",
				"attempt", i+1,
				"error", err,
			)
			continue
		}

		logger.Info(fmt.Sprintf("request %d complete", i+1),
			"status", resp.StatusCode,
			"body_length", len(body),
		)
	}

	logger.Info("client demo finished")
}
