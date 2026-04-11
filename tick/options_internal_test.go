package tick

import (
	"context"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() { chassis.RequireMajor(11) }

func TestOptionsConfigureConfig(t *testing.T) {
	cfg := &config{onError: Skip}

	Immediate()(cfg)
	Jitter(25 * time.Millisecond)(cfg)
	OnError(Stop)(cfg)
	Label("monitoring-loop")(cfg)

	if !cfg.immediate {
		t.Fatal("expected Immediate() to set config.immediate")
	}
	if cfg.jitter != 25*time.Millisecond {
		t.Fatalf("expected jitter=25ms, got %v", cfg.jitter)
	}
	if cfg.onError != Stop {
		t.Fatalf("expected onError=Stop, got %v", cfg.onError)
	}
	if cfg.label != "monitoring-loop" {
		t.Fatalf("expected label=monitoring-loop, got %q", cfg.label)
	}
}

func TestEveryRejectsNonPositiveInterval(t *testing.T) {
	component := Every(0, func(context.Context) error {
		t.Fatal("handler should not be called when interval is invalid")
		return nil
	})

	err := component(context.Background())
	if err == nil {
		t.Fatal("expected error for non-positive interval")
	}
	if !strings.Contains(err.Error(), "interval must be positive") {
		t.Fatalf("unexpected error: %v", err)
	}
}
