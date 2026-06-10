package clikit

import (
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"
)

type matrixOptions struct {
	Name    string        `flag:"name" env:"CLI_NAME" default:"default-name"`
	Count   int           `flag:"count" env:"CLI_COUNT" default:"7"`
	Big     int64         `flag:"big" env:"CLI_BIG" default:"8"`
	Ratio   float64       `flag:"ratio" env:"CLI_RATIO" default:"1.5"`
	Enabled bool          `flag:"enabled" env:"CLI_ENABLED" default:"true"`
	Delay   time.Duration `flag:"delay" env:"CLI_DELAY" default:"2s"`
	Tags    []string      `flag:"tag" env:"CLI_TAGS" default:"default-a,default-b"`
}

func TestBindResolvePrecedenceMatrix(t *testing.T) {
	t.Setenv("CLI_NAME", "env-name")
	t.Setenv("CLI_COUNT", "41")
	t.Setenv("CLI_BIG", "42")
	t.Setenv("CLI_RATIO", "3.25")
	t.Setenv("CLI_ENABLED", "true")
	t.Setenv("CLI_DELAY", "4s")
	t.Setenv("CLI_TAGS", "env-a,env-b")

	var opts matrixOptions
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	binding, err := Bind(fs, &opts)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if err := fs.Parse([]string{
		"--name", "flag-name",
		"--count", "11",
		"--big", "12",
		"--ratio", "2.5",
		"--enabled=false",
		"--delay", "5s",
		"--tag", "flag-a",
		"--tag", "flag-b, flag-c",
	}); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if err := binding.Resolve(fs); err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	want := matrixOptions{
		Name:    "flag-name",
		Count:   11,
		Big:     12,
		Ratio:   2.5,
		Enabled: false,
		Delay:   5 * time.Second,
		Tags:    []string{"flag-a", "flag-b", "flag-c"},
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("opts = %#v, want %#v", opts, want)
	}
}

func TestBindResolveEnvBeatsDefaultAndDefaultApplies(t *testing.T) {
	t.Setenv("CLI_NAME", "env-name")
	t.Setenv("CLI_COUNT", "41")

	var opts matrixOptions
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	binding, err := Bind(fs, &opts)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if err := binding.Resolve(fs); err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if opts.Name != "env-name" || opts.Count != 41 {
		t.Fatalf("env values not applied: %#v", opts)
	}
	if opts.Big != 8 || opts.Ratio != 1.5 || opts.Enabled != true || opts.Delay != 2*time.Second {
		t.Fatalf("defaults not applied: %#v", opts)
	}
	if !reflect.DeepEqual(opts.Tags, []string{"default-a", "default-b"}) {
		t.Fatalf("default tags = %#v", opts.Tags)
	}
}

func TestBindEnvOnlyAndValidation(t *testing.T) {
	t.Setenv("CLI_HOST", "host-01")
	type opts struct {
		Host string `env:"CLI_HOST" validate:"pattern=^[a-z0-9\\-]+$"`
		Port int    `flag:"port" default:"8080" validate:"min=1,max=65535"`
	}
	var o opts
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	binding, err := Bind(fs, &o)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if err := binding.Resolve(fs); err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if o.Host != "host-01" || o.Port != 8080 {
		t.Fatalf("resolved opts = %#v", o)
	}
}

func TestBindErrorsAreClean(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		type opts struct {
			A string `flag:"same" default:"a"`
			B string `flag:"same" default:"b"`
		}
		_, err := Bind(flag.NewFlagSet("test", flag.ContinueOnError), &opts{})
		if err == nil || !strings.Contains(err.Error(), "duplicate flag") {
			t.Fatalf("Bind duplicate error = %v", err)
		}
	})

	t.Run("reserved", func(t *testing.T) {
		type opts struct {
			Verbose bool `flag:"v" default:"false"`
		}
		_, err := Bind(flag.NewFlagSet("test", flag.ContinueOnError), &opts{})
		if err == nil || !strings.Contains(err.Error(), "reserved global flag") {
			t.Fatalf("Bind reserved error = %v", err)
		}
	})

	t.Run("missing required", func(t *testing.T) {
		type opts struct {
			Name string `flag:"name"`
		}
		var o opts
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		binding, err := Bind(fs, &o)
		if err != nil {
			t.Fatalf("Bind returned error: %v", err)
		}
		if err := binding.Resolve(fs); err == nil || !isUsage(err) {
			t.Fatalf("Resolve missing required err = %v", err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		type opts struct {
			Port int `flag:"port" default:"0" validate:"min=1"`
		}
		var o opts
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		binding, err := Bind(fs, &o)
		if err != nil {
			t.Fatalf("Bind returned error: %v", err)
		}
		if err := binding.Resolve(fs); err == nil || !isUsage(err) {
			t.Fatalf("Resolve validation err = %v", err)
		}
	})
}
