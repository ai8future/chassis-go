package config

import (
	"os"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(3)
	os.Exit(m.Run())
}

// ---------- helper types ----------

type fullConfig struct {
	Host     string        `env:"TEST_HOST"`
	Port     int           `env:"TEST_PORT"`
	Workers  int64         `env:"TEST_WORKERS"`
	Temp     float64       `env:"TEST_TEMP"`
	Debug    bool          `env:"TEST_DEBUG"`
	Timeout  time.Duration `env:"TEST_TIMEOUT"`
	Features []string      `env:"TEST_FEATURES"`
}

type withDefaults struct {
	Host string `env:"TEST_HOST" default:"localhost"`
	Port int    `env:"TEST_PORT" default:"8080"`
}

type withRequired struct {
	Secret string `env:"TEST_SECRET"` // required by default
}

type withOptional struct {
	Nickname string `env:"TEST_NICKNAME" required:"false"`
}

type emptyStruct struct{}

type mixedConfig struct {
	Name    string `env:"TEST_NAME"`
	Label   string // no env tag — should be skipped
	Visible bool   `env:"TEST_VISIBLE" default:"true"`
}

// ---------- tests ----------

func TestMustLoad_AllFieldTypes(t *testing.T) {
	t.Setenv("TEST_HOST", "example.com")
	t.Setenv("TEST_PORT", "9090")
	t.Setenv("TEST_WORKERS", "42")
	t.Setenv("TEST_TEMP", "0.7")
	t.Setenv("TEST_DEBUG", "true")
	t.Setenv("TEST_TIMEOUT", "5s")
	t.Setenv("TEST_FEATURES", "alpha, beta, gamma")

	cfg := MustLoad[fullConfig]()

	if cfg.Host != "example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "example.com")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9090)
	}
	if cfg.Workers != 42 {
		t.Errorf("Workers = %d, want %d", cfg.Workers, 42)
	}
	if cfg.Temp != 0.7 {
		t.Errorf("Temp = %f, want %f", cfg.Temp, 0.7)
	}
	if cfg.Debug != true {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
	if len(cfg.Features) != 3 || cfg.Features[0] != "alpha" || cfg.Features[1] != "beta" || cfg.Features[2] != "gamma" {
		t.Errorf("Features = %v, want [alpha beta gamma]", cfg.Features)
	}
}

func TestMustLoad_DefaultValues(t *testing.T) {
	// Do NOT set TEST_HOST or TEST_PORT — defaults should apply.
	cfg := MustLoad[withDefaults]()

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
}

func TestMustLoad_DefaultOverriddenByEnv(t *testing.T) {
	t.Setenv("TEST_HOST", "prod.example.com")
	t.Setenv("TEST_PORT", "443")

	cfg := MustLoad[withDefaults]()

	if cfg.Host != "prod.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "prod.example.com")
	}
	if cfg.Port != 443 {
		t.Errorf("Port = %d, want %d", cfg.Port, 443)
	}
}

func TestMustLoad_PanicsOnMissingRequired(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for missing required env var, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %v", r)
		}
		if !contains(msg, "TEST_SECRET") {
			t.Errorf("panic message %q does not mention the missing var TEST_SECRET", msg)
		}
	}()

	_ = MustLoad[withRequired]()
}

func TestMustLoad_OptionalFieldNoEnv(t *testing.T) {
	// TEST_NICKNAME is not set and required:"false" — should not panic.
	cfg := MustLoad[withOptional]()

	if cfg.Nickname != "" {
		t.Errorf("Nickname = %q, want empty string", cfg.Nickname)
	}
}

func TestMustLoad_EmptyStruct(t *testing.T) {
	// Should succeed with no env vars needed.
	_ = MustLoad[emptyStruct]()
}

func TestMustLoad_SkipsFieldsWithoutEnvTag(t *testing.T) {
	t.Setenv("TEST_NAME", "hello")

	cfg := MustLoad[mixedConfig]()

	if cfg.Name != "hello" {
		t.Errorf("Name = %q, want %q", cfg.Name, "hello")
	}
	if cfg.Label != "" {
		t.Errorf("Label = %q, want empty (should be skipped)", cfg.Label)
	}
	if cfg.Visible != true {
		t.Errorf("Visible = %v, want true (from default)", cfg.Visible)
	}
}

func TestMustLoad_InvalidInt(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid int, got none")
		}
	}()

	t.Setenv("TEST_PORT", "not-a-number")

	type cfg struct {
		Port int `env:"TEST_PORT"`
	}
	_ = MustLoad[cfg]()
}

func TestMustLoad_InvalidBool(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid bool, got none")
		}
	}()

	t.Setenv("TEST_FLAG", "maybe")

	type cfg struct {
		Flag bool `env:"TEST_FLAG"`
	}
	_ = MustLoad[cfg]()
}

func TestMustLoad_InvalidDuration(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid duration, got none")
		}
	}()

	t.Setenv("TEST_DUR", "notaduration")

	type cfg struct {
		Dur time.Duration `env:"TEST_DUR"`
	}
	_ = MustLoad[cfg]()
}

func TestMustLoad_BoolFalseExplicit(t *testing.T) {
	t.Setenv("TEST_DEBUG", "false")

	type cfg struct {
		Debug bool `env:"TEST_DEBUG"`
	}
	c := MustLoad[cfg]()
	if c.Debug != false {
		t.Errorf("Debug = %v, want false", c.Debug)
	}
}

func TestMustLoad_SliceStringSingleElement(t *testing.T) {
	t.Setenv("TEST_TAGS", "solo")

	type cfg struct {
		Tags []string `env:"TEST_TAGS"`
	}
	c := MustLoad[cfg]()
	if len(c.Tags) != 1 || c.Tags[0] != "solo" {
		t.Errorf("Tags = %v, want [solo]", c.Tags)
	}
}

func TestMustLoad_InvalidFloat(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid float64, got none")
		}
	}()

	t.Setenv("TEST_TEMP", "not-a-float")

	type cfg struct {
		Temp float64 `env:"TEST_TEMP"`
	}
	_ = MustLoad[cfg]()
}

// ---------- helpers ----------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
