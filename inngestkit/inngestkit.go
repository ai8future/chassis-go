// Package inngestkit provides thin setup glue for wiring inngest's Go SDK
// into a chassis service. It handles config loading, startup validation,
// client construction, HTTP mount, and event sending.
//
// inngestkit does NOT wrap inngest's function creation, step definitions,
// retry policies, or any other SDK feature. Use the inngestgo SDK directly
// for those. inngestkit only provides the chassis-flavored setup.
package inngestkit

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/inngest/inngestgo"
)

// Config holds environment-driven configuration for the inngest client.
// Embed this in your service config struct for automatic population via
// config.MustLoad.
type Config struct {
	BaseURL    string `env:"INNGEST_BASE_URL"    required:"true"`
	AppID      string `env:"INNGEST_APP_ID"      required:"true"`
	EventKey   string `env:"INNGEST_EVENT_KEY"   required:"true"`
	SigningKey string `env:"INNGEST_SIGNING_KEY" required:"true"`

	SigningKeyFallback string `env:"INNGEST_SIGNING_KEY_FALLBACK" required:"false"`
	ServeOrigin        string `env:"INNGEST_SERVE_ORIGIN"         required:"false"`
	ServePath          string `env:"INNGEST_SERVE_PATH"           required:"false" default:"/api/inngest"`
}

// Kit wires inngest into a chassis service. Construct with New.
type Kit struct {
	client inngestgo.Client
	cfg    Config
}

// New constructs a Kit, validates the config, and returns an error on
// misconfiguration. Call this during service startup.
func New(cfg Config) (*Kit, error) {
	chassis.AssertVersionChecked()

	if cfg.ServePath == "" {
		cfg.ServePath = "/api/inngest"
	}

	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("inngestkit: %w", err)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	dev := false
	signingKey := "signkey-prod-" + cfg.SigningKey

	opts := inngestgo.ClientOpts{
		AppID:           cfg.AppID,
		EventKey:        &cfg.EventKey,
		SigningKey:      &signingKey,
		APIBaseURL:      &baseURL,
		EventAPIBaseURL: &baseURL,
		RegisterURL:     inngestgo.StrPtr(baseURL + "/fn/register"),
		Dev:             &dev,
	}

	if cfg.SigningKeyFallback != "" {
		fallback := "signkey-prod-" + cfg.SigningKeyFallback
		opts.SigningKeyFallback = &fallback
	}

	client, err := inngestgo.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("inngestkit: create client: %w", err)
	}

	return &Kit{client: client, cfg: cfg}, nil
}

// Client returns the underlying inngestgo client. Use this for defining
// functions with inngestgo.CreateFunction(kit.Client(), ...).
func (k *Kit) Client() inngestgo.Client { return k.client }

// Mount attaches the inngest serve handler to an http.ServeMux at the
// configured ServePath. The handler responds to GET (introspection) and
// PUT (sync) from the inngest server, and POST (function invocation).
//
// If ServeOrigin is set, it tells inngest where to reach this app for
// function invocations (e.g. "http://myservice.lan:8080"). If unset,
// the SDK infers the origin from incoming requests.
func (k *Kit) Mount(mux *http.ServeMux) {
	opts := inngestgo.ServeOpts{
		Path: &k.cfg.ServePath,
	}
	if k.cfg.ServeOrigin != "" {
		opts.Origin = &k.cfg.ServeOrigin
	}
	handler := k.client.ServeWithOpts(opts)
	mux.Handle(k.cfg.ServePath, handler)
}

// Send emits one or more events into inngest. Convenience wrapper around
// the native client's Send/SendMany.
func (k *Kit) Send(ctx context.Context, events ...inngestgo.Event) ([]string, error) {
	if len(events) == 0 {
		return nil, nil
	}
	if len(events) == 1 {
		id, err := k.client.Send(ctx, events[0])
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}
	batch := make([]any, len(events))
	for i, e := range events {
		batch[i] = e
	}
	return k.client.SendMany(ctx, batch)
}

// validateConfig checks all required fields and format constraints.
func validateConfig(cfg Config) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("INNGEST_BASE_URL is required")
	}
	if !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return fmt.Errorf("INNGEST_BASE_URL must start with http:// or https://")
	}
	if cfg.AppID == "" {
		return fmt.Errorf("INNGEST_APP_ID is required")
	}
	if cfg.EventKey == "" {
		return fmt.Errorf("INNGEST_EVENT_KEY is required")
	}
	if cfg.SigningKey == "" {
		return fmt.Errorf("INNGEST_SIGNING_KEY is required")
	}
	if err := validateHexKey("INNGEST_SIGNING_KEY", cfg.SigningKey); err != nil {
		return err
	}
	if cfg.SigningKeyFallback != "" {
		if err := validateHexKey("INNGEST_SIGNING_KEY_FALLBACK", cfg.SigningKeyFallback); err != nil {
			return err
		}
	}
	if cfg.ServePath != "" && !strings.HasPrefix(cfg.ServePath, "/") {
		return fmt.Errorf("INNGEST_SERVE_PATH must start with /")
	}
	return nil
}

// validateHexKey checks that a signing key is a valid even-length hex string.
func validateHexKey(name, key string) error {
	if len(key)%2 != 0 {
		return fmt.Errorf("%s must be even-length hex (got %d chars)", name, len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("%s must be valid hex: %w", name, err)
	}
	return nil
}
