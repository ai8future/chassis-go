// otel/otel_test.go
package otel_test

import (
	"context"
	"testing"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/otel"
)

func TestInitReturnsShutdownFunc(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(2)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}
