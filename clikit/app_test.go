package clikit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/registry"
)

func init() {
	chassis.RequireMajor(11)
}

type greetOptions struct {
	Name string `flag:"name" env:"GREET_NAME" default:"world" usage:"name to greet"`
}

func TestAppDispatchAndJSONContracts(t *testing.T) {
	var seenOptions any
	opts := &greetOptions{}
	app := New(Config{Name: "demo"}).Command(Command{
		Name:  "greet",
		Short: "greet someone",
		Flags: opts,
		Run: func(ctx context.Context, cctx *Context) error {
			seenOptions = cctx.Options
			cctx.Log.InfoContext(ctx, "diagnostic")
			return cctx.Out.Emit(map[string]any{"name": cctx.Options.(*greetOptions).Name, "args": cctx.Args})
		},
	})

	code, out, errOut := captureRun(t, app, []string{"demo", "--json", "greet", "--name", "Ada", "pos1", "pos2"})
	if code != ExitOK {
		t.Fatalf("Run code = %d, stderr = %q", code, errOut)
	}
	if seenOptions != opts {
		t.Fatalf("Context.Options pointer mismatch")
	}
	if strings.TrimSpace(errOut) == "" || strings.Contains(errOut, "Ada") {
		t.Fatalf("diagnostic stderr = %q", errOut)
	}
	if !json.Valid([]byte(out)) || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout = %q, want one JSON doc", out)
	}
	var decoded struct {
		Name string   `json:"name"`
		Args []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Name != "Ada" || len(decoded.Args) != 2 || decoded.Args[0] != "pos1" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestAppHelpAndUsageRouting(t *testing.T) {
	app := New(Config{Name: "demo"}).Command(Command{Name: "ok", Short: "works", Run: func(context.Context, *Context) error { return nil }})

	code, out, errOut := captureRun(t, app, []string{"demo"})
	if code != ExitOK || !strings.Contains(out, "Usage:") || errOut != "" {
		t.Fatalf("empty argv code/out/err = %d/%q/%q", code, out, errOut)
	}

	code, out, errOut = captureRun(t, app, []string{"demo", "ok", "--help"})
	if code != ExitOK || !strings.Contains(out, "Usage:") || errOut != "" {
		t.Fatalf("cmd help code/out/err = %d/%q/%q", code, out, errOut)
	}

	code, out, errOut = captureRun(t, app, []string{"demo", "missing"})
	if code != ExitUsage || out != "" || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown cmd code/out/err = %d/%q/%q", code, out, errOut)
	}
}

func TestAppUnknownFlagAndGlobalFlagPosition(t *testing.T) {
	app := New(Config{Name: "demo"}).Command(Command{Name: "ok", Run: func(context.Context, *Context) error { return nil }})

	code, _, errOut := captureRun(t, app, []string{"demo", "ok", "--unknown"})
	if code != ExitUsage || !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("unknown flag code/err = %d/%q", code, errOut)
	}

	code, _, errOut = captureRun(t, app, []string{"demo", "ok", "--json"})
	if code != ExitUsage || !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("post-command global flag code/err = %d/%q", code, errOut)
	}
}

func TestAppTerminatorPreservesPositionals(t *testing.T) {
	var got []string
	app := New(Config{Name: "demo"}).Command(Command{Name: "echo", Run: func(_ context.Context, cctx *Context) error {
		got = append([]string(nil), cctx.Args...)
		return nil
	}})
	code, _, errOut := captureRun(t, app, []string{"demo", "echo", "--", "--not-a-flag"})
	if code != ExitOK {
		t.Fatalf("code = %d stderr = %q", code, errOut)
	}
	if len(got) != 1 || got[0] != "--not-a-flag" {
		t.Fatalf("args = %#v", got)
	}
}

func TestAppErrors(t *testing.T) {
	t.Run("generic", func(t *testing.T) {
		app := New(Config{Name: "demo"}).Command(Command{Name: "fail", Run: func(context.Context, *Context) error { return errors.New("boom") }})
		code, _, errOut := captureRun(t, app, []string{"demo", "fail"})
		if code != ExitError || !strings.Contains(errOut, "boom") {
			t.Fatalf("code/err = %d/%q", code, errOut)
		}
	})

	t.Run("exit err", func(t *testing.T) {
		app := New(Config{Name: "demo"}).Command(Command{Name: "fail", Run: func(context.Context, *Context) error { return ExitErr{Code: 7, Err: errors.New("custom")} }})
		code, _, errOut := captureRun(t, app, []string{"demo", "fail"})
		if code != 7 || !strings.Contains(errOut, "custom") {
			t.Fatalf("code/err = %d/%q", code, errOut)
		}
	})

	t.Run("panic", func(t *testing.T) {
		app := New(Config{Name: "demo"}).Command(Command{Name: "panic", Run: func(context.Context, *Context) error { panic("kapow") }})
		code, _, errOut := captureRun(t, app, []string{"demo", "panic"})
		if code != ExitError || !strings.Contains(errOut, "panic: kapow") {
			t.Fatalf("code/err = %d/%q", code, errOut)
		}
	})

	t.Run("nil run", func(t *testing.T) {
		app := New(Config{Name: "demo"}).Command(Command{Name: "bad"})
		code, _, errOut := captureRun(t, app, []string{"demo", "bad"})
		if code != ExitUsage || !strings.Contains(errOut, "nil Run") {
			t.Fatalf("code/err = %d/%q", code, errOut)
		}
	})

	t.Run("no os exit", func(t *testing.T) {
		app := New(Config{Name: "demo"}).Command(Command{Name: "ok", Run: func(context.Context, *Context) error { return nil }})
		code, _, _ := captureRun(t, app, []string{"demo", "ok"})
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}
	})
}

func TestAppResultStdoutDiagnosticStderr(t *testing.T) {
	app := New(Config{Name: "demo"}).Command(Command{Name: "run", Run: func(ctx context.Context, cctx *Context) error {
		cctx.Log.WarnContext(ctx, "warn")
		cctx.Out.Println("result")
		return nil
	}})
	code, out, errOut := captureRun(t, app, []string{"demo", "run"})
	if code != ExitOK || strings.TrimSpace(out) != "result" || !strings.Contains(errOut, "warn") {
		t.Fatalf("code/out/err = %d/%q/%q", code, out, errOut)
	}
	if strings.Contains(out, "warn") || strings.Contains(errOut, "result") {
		t.Fatalf("stdout/stderr crossed: out=%q err=%q", out, errOut)
	}
}

func TestAppDoesNotRegisterVersion(t *testing.T) {
	app := New(Config{Name: "demo"}).Command(Command{Name: "ok", Run: func(context.Context, *Context) error { return nil }})
	code, _, errOut := captureRun(t, app, []string{"demo", "--version"})
	if code != ExitUsage || !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("--version code/err = %d/%q", code, errOut)
	}
}

func TestAppUseRegistryRecordsCLICompletion(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		code, manifest := runRegistryApp(t, "clikit-registry-success", func(context.Context, *Context) error { return nil })
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}
		if manifest.Mode != "cli" || manifest.Status != "completed" || manifest.ExitCode == nil || *manifest.ExitCode != 0 {
			t.Fatalf("manifest = %#v", manifest)
		}
	})

	t.Run("failure", func(t *testing.T) {
		code, manifest := runRegistryApp(t, "clikit-registry-failure", func(context.Context, *Context) error {
			return ExitErr{Code: 7, Err: errors.New("registry failure")}
		})
		if code != 7 {
			t.Fatalf("code = %d", code)
		}
		if manifest.Mode != "cli" || manifest.Status != "failed" || manifest.ExitCode == nil || *manifest.ExitCode != 7 {
			t.Fatalf("manifest = %#v", manifest)
		}
	})
}

func ExampleSuppressAutoRebuild() {
	old, had := os.LookupEnv("CHASSIS_NO_REBUILD")
	defer func() {
		if had {
			_ = os.Setenv("CHASSIS_NO_REBUILD", old)
		} else {
			_ = os.Unsetenv("CHASSIS_NO_REBUILD")
		}
	}()
	SuppressAutoRebuild()
	fmt.Println("suppressed")
	// Output: suppressed
}

type registryManifest struct {
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code"`
}

func runRegistryApp(t *testing.T, serviceName string, run func(context.Context, *Context) error) (int, registryManifest) {
	t.Helper()
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	t.Cleanup(func() { registry.ResetForTest(t.TempDir()) })
	t.Setenv("CHASSIS_SERVICE_NAME", serviceName)

	app := New(Config{Name: "demo", UseRegistry: true}).Command(Command{Name: "run", Run: run})
	code, _, _ := captureRun(t, app, []string{"demo", "run"})

	matches, err := filepath.Glob(filepath.Join(tmp, serviceName, "*.json"))
	if err != nil {
		t.Fatalf("glob manifest: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("manifest matches = %v, want one retained CLI manifest", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest registryManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v\n%s", err, data)
	}
	return code, manifest
}
