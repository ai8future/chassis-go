// Example 01-cli demonstrates a simple CLI tool using config + logz.
//
// Run with defaults:
//
//	go run ./examples/01-cli
//
// Override via environment:
//
//	APP_NAME=my-app LOG_LEVEL=debug GREETING="Hey there!" go run ./examples/01-cli
package main

import (
	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/config"
	"github.com/ai8future/chassis-go/logz"
)

type AppConfig struct {
	AppName  string `env:"APP_NAME" default:"demo-cli"`
	LogLevel string `env:"LOG_LEVEL" default:"info"`
	Greeting string `env:"GREETING" default:"Hello from chassis-go!"`
}

func main() {
	chassis.RequireMajor(4)
	cfg := config.MustLoad[AppConfig]()
	logger := logz.New(cfg.LogLevel)

	logger.Info("application started",
		"app", cfg.AppName,
	)
	logger.Info(cfg.Greeting)
	logger.Info("application finished")
}
