package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
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
		if !strings.Contains(msg, "TEST_SECRET") {
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

func TestConvertSupportedTypes(t *testing.T) {
	type namedString string
	type namedInt int
	type namedBool bool

	tests := []struct {
		name  string
		proto any
		raw   string
		want  any
	}{
		{name: "string", proto: "", raw: "hello", want: "hello"},
		{name: "named string", proto: namedString(""), raw: "hello", want: namedString("hello")},
		{name: "int", proto: int(0), raw: "42", want: int(42)},
		{name: "named int", proto: namedInt(0), raw: "42", want: namedInt(42)},
		{name: "int64", proto: int64(0), raw: "42", want: int64(42)},
		{name: "float64", proto: float64(0), raw: "3.5", want: float64(3.5)},
		{name: "bool", proto: false, raw: "true", want: true},
		{name: "named bool", proto: namedBool(false), raw: "true", want: namedBool(true)},
		{name: "duration", proto: time.Duration(0), raw: "5s", want: 5 * time.Second},
		{name: "string slice", proto: []string{}, raw: "alpha, beta,gamma", want: []string{"alpha", "beta", "gamma"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Convert(tt.proto, tt.raw)
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Convert(%T, %q) = %#v (%T), want %#v (%T)", tt.proto, tt.raw, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestConvertInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		proto any
		raw   string
	}{
		{name: "nil prototype", proto: nil, raw: "x"},
		{name: "unsupported type", proto: struct{}{}, raw: "x"},
		{name: "invalid int", proto: int(0), raw: "nope"},
		{name: "invalid int64", proto: int64(0), raw: "nope"},
		{name: "invalid float64", proto: float64(0), raw: "nope"},
		{name: "invalid bool", proto: false, raw: "maybe"},
		{name: "invalid duration", proto: time.Duration(0), raw: "notaduration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Convert(tt.proto, tt.raw); err == nil {
				t.Fatalf("Convert(%T, %q) returned nil error", tt.proto, tt.raw)
			}
		})
	}
}

func TestCheckValidationPasses(t *testing.T) {
	tests := []struct {
		name string
		v    any
		tag  string
	}{
		{name: "min", v: 10, tag: "min=1"},
		{name: "max", v: 10, tag: "max=20"},
		{name: "oneof", v: "info", tag: "oneof=debug info warn error"},
		{name: "pattern", v: "host-01", tag: "pattern=^[a-z0-9\\-]+$"},
		{name: "combined", v: 8080, tag: "min=1,max=65535"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Check("Field", tt.v, tt.tag); err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
		})
	}
}

func TestCheckValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		v       any
		tag     string
		wantMsg string
	}{
		{name: "min", v: 0, tag: "min=1", wantMsg: "below minimum"},
		{name: "max", v: 70000, tag: "max=65535", wantMsg: "exceeds maximum"},
		{name: "oneof", v: "verbose", tag: "oneof=debug info warn error", wantMsg: "not in allowed set"},
		{name: "pattern", v: "INVALID HOST!", tag: "pattern=^[a-z0-9\\-]+$", wantMsg: "does not match pattern"},
		{name: "invalid min tag", v: 1, tag: "min=nope", wantMsg: "invalid min value"},
		{name: "invalid max tag", v: 1, tag: "max=nope", wantMsg: "invalid max value"},
		{name: "invalid pattern tag", v: "x", tag: "pattern=[", wantMsg: "invalid pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check("Field", tt.v, tt.tag)
			if err == nil {
				t.Fatalf("Check(%q) returned nil error", tt.tag)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("Check error = %q, want substring %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

// ---------- validate tag tests ----------

func TestValidateMin(t *testing.T) {
	type Cfg struct {
		Port int `env:"PORT" default:"0" validate:"min=1"`
	}
	t.Setenv("PORT", "0")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for min validation")
		}
	}()
	MustLoad[Cfg]()
}

func TestValidateMax(t *testing.T) {
	type Cfg struct {
		Port int `env:"PORT" validate:"max=65535"`
	}
	t.Setenv("PORT", "70000")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for max validation")
		}
	}()
	MustLoad[Cfg]()
}

func TestValidateOneof(t *testing.T) {
	type Cfg struct {
		Level string `env:"LOG_LEVEL" validate:"oneof=debug info warn error"`
	}
	t.Setenv("LOG_LEVEL", "verbose")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for oneof validation")
		}
	}()
	MustLoad[Cfg]()
}

func TestValidateOneofPass(t *testing.T) {
	type Cfg struct {
		Level string `env:"LOG_LEVEL" validate:"oneof=debug info warn error"`
	}
	t.Setenv("LOG_LEVEL", "info")
	cfg := MustLoad[Cfg]()
	if cfg.Level != "info" {
		t.Fatalf("expected info, got %q", cfg.Level)
	}
}

func TestValidatePattern(t *testing.T) {
	type Cfg struct {
		Host string `env:"HOST" validate:"pattern=^[a-z0-9\\-]+$"`
	}
	t.Setenv("HOST", "INVALID HOST!")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for pattern validation")
		}
	}()
	MustLoad[Cfg]()
}

func TestValidateMinMax(t *testing.T) {
	type Cfg struct {
		Port int `env:"PORT" validate:"min=1,max=65535"`
	}
	t.Setenv("PORT", "8080")
	cfg := MustLoad[Cfg]()
	if cfg.Port != 8080 {
		t.Fatalf("expected 8080, got %d", cfg.Port)
	}
}
