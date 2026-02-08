package flagz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/flagz"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(5)
	os.Exit(m.Run())
}

func TestFromEnvReadsPrefixedVars(t *testing.T) {
	t.Setenv("FLAG_NEW_FEATURE", "true")
	t.Setenv("FLAG_BETA_MODE", "false")
	t.Setenv("UNRELATED_VAR", "ignored")

	src := flagz.FromEnv("FLAG")
	f := flagz.New(src)

	if !f.Enabled("new-feature") {
		t.Error("expected new-feature to be enabled")
	}
	if f.Enabled("beta-mode") {
		t.Error("expected beta-mode to be disabled (value is 'false')")
	}
	if f.Enabled("unrelated-var") {
		t.Error("expected unrelated-var to not exist")
	}
}

func TestFromMapLookup(t *testing.T) {
	src := flagz.FromMap(map[string]string{
		"dark-mode": "true",
		"v2-api":    "false",
	})
	f := flagz.New(src)

	if !f.Enabled("dark-mode") {
		t.Error("expected dark-mode to be enabled")
	}
	if f.Enabled("v2-api") {
		t.Error("expected v2-api to be disabled")
	}
	if f.Enabled("nonexistent") {
		t.Error("expected nonexistent flag to be disabled")
	}
}

func TestFromJSONReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flags.json")
	os.WriteFile(path, []byte(`{"new-ui": "true", "old-api": "false"}`), 0644)

	src := flagz.FromJSON(path)
	f := flagz.New(src)

	if !f.Enabled("new-ui") {
		t.Error("expected new-ui to be enabled")
	}
	if f.Enabled("old-api") {
		t.Error("expected old-api to be disabled")
	}
}

func TestFromJSONPanicsOnMissingFile(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for missing JSON file")
		}
	}()
	flagz.FromJSON("/nonexistent/path.json")
}

func TestFromJSONPanicsOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`not json`), 0644)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid JSON")
		}
	}()
	flagz.FromJSON(path)
}

func TestMultiLayering(t *testing.T) {
	base := flagz.FromMap(map[string]string{
		"feature-a": "true",
		"feature-b": "true",
	})
	override := flagz.FromMap(map[string]string{
		"feature-b": "false",
		"feature-c": "true",
	})

	src := flagz.Multi(base, override)
	f := flagz.New(src)

	if !f.Enabled("feature-a") {
		t.Error("feature-a should come from base")
	}
	if f.Enabled("feature-b") {
		t.Error("feature-b should be overridden to false")
	}
	if !f.Enabled("feature-c") {
		t.Error("feature-c should come from override")
	}
}

func TestEnabledTrueFalse(t *testing.T) {
	src := flagz.FromMap(map[string]string{
		"on":  "true",
		"off": "false",
	})
	f := flagz.New(src)

	if !f.Enabled("on") {
		t.Error("expected 'on' to be enabled")
	}
	if f.Enabled("off") {
		t.Error("expected 'off' to be disabled")
	}
	if f.Enabled("missing") {
		t.Error("expected missing flag to be disabled")
	}
}

func TestEnabledForPercentage(t *testing.T) {
	src := flagz.FromMap(map[string]string{"rollout": "true"})
	f := flagz.New(src)
	ctx := context.Background()

	// 0% should always be false.
	if f.EnabledFor(ctx, "rollout", flagz.Context{UserID: "user-1", Percent: 0}) {
		t.Error("0% should always be disabled")
	}

	// 100% should always be true (when flag value is "true").
	if !f.EnabledFor(ctx, "rollout", flagz.Context{UserID: "user-1", Percent: 100}) {
		t.Error("100% should always be enabled")
	}
}

func TestEnabledForConsistentBucketing(t *testing.T) {
	src := flagz.FromMap(map[string]string{"experiment": "true"})
	f := flagz.New(src)
	ctx := context.Background()

	fctx := flagz.Context{UserID: "stable-user", Percent: 50}

	// Run the same evaluation multiple times — result should be deterministic.
	first := f.EnabledFor(ctx, "experiment", fctx)
	for range 100 {
		if got := f.EnabledFor(ctx, "experiment", fctx); got != first {
			t.Fatal("EnabledFor should be deterministic for the same user+flag")
		}
	}
}

func TestVariantDefaultAndPresent(t *testing.T) {
	src := flagz.FromMap(map[string]string{
		"color": "blue",
	})
	f := flagz.New(src)

	if got := f.Variant("color", "red"); got != "blue" {
		t.Errorf("Variant(color) = %q, want %q", got, "blue")
	}
	if got := f.Variant("missing", "fallback"); got != "fallback" {
		t.Errorf("Variant(missing) = %q, want %q", got, "fallback")
	}
}

func TestNilSourcePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil source")
		}
	}()
	flagz.New(nil)
}

func TestEnabled_CaseSensitive(t *testing.T) {
	src := flagz.FromMap(map[string]string{
		"my-flag": "true",
	})
	f := flagz.New(src)

	if !f.Enabled("my-flag") {
		t.Error("expected my-flag to be enabled")
	}
	if f.Enabled("MY-FLAG") {
		t.Error("flag lookup should be case-sensitive")
	}
	if f.Enabled("My-Flag") {
		t.Error("flag lookup should be case-sensitive")
	}
}

func TestEnabledFor_FalseValue(t *testing.T) {
	src := flagz.FromMap(map[string]string{
		"my-flag": "false",
	})
	f := flagz.New(src)
	ctx := context.Background()

	if f.EnabledFor(ctx, "my-flag", flagz.Context{UserID: "u1", Percent: 100}) {
		t.Error("EnabledFor should return false when flag value is 'false'")
	}
}

func TestFromMap_CopiesInput(t *testing.T) {
	m := map[string]string{"flag": "true"}
	src := flagz.FromMap(m)
	f := flagz.New(src)

	// Mutate the original map after construction.
	m["flag"] = "false"

	if !f.Enabled("flag") {
		t.Error("FromMap should copy the input map; mutation should not affect the source")
	}
}
