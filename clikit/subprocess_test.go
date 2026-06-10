package clikit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestExampleSmokeBuildsAndRuns(t *testing.T) {
	root := repoRoot(t)
	exe := filepath.Join(t.TempDir(), exeName("clikit-demo"))
	cmd := exec.Command("go", "build", "-o", exe, "./examples/05-clikit")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build example failed: %v\n%s", err, out)
	}

	versionBytes, err := os.ReadFile(filepath.Join(root, "examples", "05-clikit", "VERSION"))
	if err != nil {
		t.Fatalf("read example VERSION: %v", err)
	}
	out, errOut, code := runBinary(t, exe, []string{"--version"}, nil)
	wantVersion := fmt.Sprintf("clikit-demo %s (chassis-go %s)\n", strings.TrimSpace(string(versionBytes)), chassis.Version)
	if code != ExitOK || out != wantVersion || errOut != "" {
		t.Fatalf("example --version code/out/err = %d/%q/%q, want %q", code, out, errOut, wantVersion)
	}

	out, errOut, code = runBinary(t, exe, []string{"--json", "greet"}, nil)
	if code != ExitOK {
		t.Fatalf("example code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if !json.Valid([]byte(out)) || strings.Count(out, "\n") != 1 {
		t.Fatalf("example stdout = %q, want one JSON document", out)
	}
}

func TestConsumerFixtureVersionJSONFailuresSignalAndFreshness(t *testing.T) {
	fixture := writeConsumerFixture(t, "1.0.0")
	demo := buildFixtureBinary(t, fixture, "demo")
	suppress := buildFixtureBinary(t, fixture, "suppress")

	out, errOut, code := runBinary(t, demo, []string{"--version"}, cleanChassisEnv())
	wantVersion := fmt.Sprintf("demo 1.0.0 (chassis-go %s)\n", chassis.Version)
	if code != ExitOK || out != wantVersion || errOut != "" {
		t.Fatalf("--version code/out/err = %d/%q/%q, want %q", code, out, errOut, wantVersion)
	}

	out, errOut, code = runBinary(t, demo, []string{"--json", "greet"}, cleanChassisEnv())
	if code != ExitOK || !json.Valid([]byte(out)) || strings.Count(out, "\n") != 1 || strings.Contains(out, "diagnostic") || !strings.Contains(errOut, "diagnostic") {
		t.Fatalf("json greet code/out/err = %d/%q/%q", code, out, errOut)
	}

	out, _, code = runBinary(t, demo, []string{"--json", "leak"}, cleanChassisEnv())
	if code != ExitOK || json.Valid([]byte(out)) || !strings.Contains(out, "raw stdout leak") {
		t.Fatalf("leak should demonstrate broken JSON contract, code/out = %d/%q", code, out)
	}

	failureCases := [][]string{
		{"missing"},
		{"greet", "--bogus"},
		{"need"},
		{"dupe"},
		{"nil"},
	}
	for _, args := range failureCases {
		_, errOut, code := runBinary(t, demo, args, cleanChassisEnv())
		if code != ExitUsage {
			t.Fatalf("%v code=%d stderr=%q, want usage", args, code, errOut)
		}
	}

	assertSignalCancels(t, demo)

	if err := os.WriteFile(filepath.Join(fixture, "VERSION"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatalf("write stale VERSION: %v", err)
	}
	out, errOut, code = runBinary(t, demo, []string{"--json", "version"}, cleanChassisEnv())
	if code != ExitOK || !strings.Contains(errOut, "stale binary") {
		t.Fatalf("freshness rebuild code/out/err = %d/%q/%q", code, out, errOut)
	}
	var rebuilt struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &rebuilt); err != nil {
		t.Fatalf("rebuilt stdout is not JSON: %v; %q", err, out)
	}
	if rebuilt.Version != "1.0.1" {
		t.Fatalf("rebuilt version = %q, want 1.0.1; stderr=%q", rebuilt.Version, errOut)
	}

	out, errOut, code = runBinary(t, suppress, []string{"--json", "version"}, cleanChassisEnv())
	if code != ExitOK || strings.Contains(errOut, "stale binary") {
		t.Fatalf("suppressed freshness code/out/err = %d/%q/%q", code, out, errOut)
	}
	var suppressed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &suppressed); err != nil {
		t.Fatalf("suppressed stdout is not JSON: %v; %q", err, out)
	}
	if suppressed.Version != "1.0.0" {
		t.Fatalf("suppressed compiled version = %q, want stale 1.0.0", suppressed.Version)
	}
}

func writeConsumerFixture(t *testing.T, version string) string {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), fmt.Sprintf(`module example.com/consumer

go 1.26.0

require github.com/ai8future/chassis-go/v11 v11.0.0

replace github.com/ai8future/chassis-go/v11 => %s
`, filepath.ToSlash(root)))
	mustWrite(t, filepath.Join(dir, "VERSION"), version+"\n")
	mustWrite(t, filepath.Join(dir, "appversion.go"), `package consumer

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawAppVersion string

var AppVersion = strings.TrimSpace(rawAppVersion)
`)
	mustWrite(t, filepath.Join(dir, "cmd", "demo", "main.go"), consumerMain(false))
	mustWrite(t, filepath.Join(dir, "cmd", "suppress", "main.go"), consumerMain(true))

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy fixture failed: %v\n%s", err, out)
	}
	return dir
}

func consumerMain(suppress bool) string {
	prefix := ""
	if suppress {
		prefix = "\n\tclikit.SuppressAutoRebuild()"
	}
	return fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"os"

	consumer "example.com/consumer"
	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/clikit"
)

type needOptions struct { Name string `+"`"+`flag:"name"`+"`"+` }
type dupeOptions struct { A string `+"`"+`flag:"same" default:"a"`+"`"+`; B string `+"`"+`flag:"same" default:"b"`+"`"+` }

func main() {%s
	chassis.SetAppVersion(consumer.AppVersion)
	chassis.RequireMajor(11)
	app := clikit.New(clikit.Config{Name: "demo"})
	app.Command(clikit.Command{Name: "greet", Run: func(ctx context.Context, c *clikit.Context) error { c.Log.InfoContext(ctx, "diagnostic"); return c.Out.Emit(map[string]string{"message": "hello"}) }})
	app.Command(clikit.Command{Name: "leak", Run: func(_ context.Context, c *clikit.Context) error { fmt.Println("raw stdout leak"); return c.Out.Emit(map[string]string{"message": "hello"}) }})
	app.Command(clikit.Command{Name: "version", Run: func(_ context.Context, c *clikit.Context) error { return c.Out.Emit(map[string]string{"version": consumer.AppVersion}) }})
	app.Command(clikit.Command{Name: "sleep", Run: func(ctx context.Context, _ *clikit.Context) error { <-ctx.Done(); return clikit.ExitErr{Code: 130, Err: ctx.Err()} }})
	app.Command(clikit.Command{Name: "need", Flags: &needOptions{}, Run: func(context.Context, *clikit.Context) error { return nil }})
	app.Command(clikit.Command{Name: "dupe", Flags: &dupeOptions{}, Run: func(context.Context, *clikit.Context) error { return nil }})
	app.Command(clikit.Command{Name: "nil"})
	os.Exit(app.Run(os.Args))
}
`, prefix)
}

func buildFixtureBinary(t *testing.T, fixture, name string) string {
	t.Helper()
	exe := filepath.Join(fixture, "cmd", name, exeName(name))
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/"+name)
	cmd.Dir = fixture
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s failed: %v\n%s", name, err, out)
	}
	return exe
}

func assertSignalCancels(t *testing.T, exe string) {
	t.Helper()
	cmd := exec.Command(exe, "sleep")
	cmd.Env = cleanChassisEnv()
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("interrupt sleep: %v", err)
	}
	err := cmd.Wait()
	if code := processExitCode(err); code != 130 {
		t.Fatalf("signal code=%d err=%v stdout=%q stderr=%q", code, err, out.String(), errOut.String())
	}
}

func runBinary(t *testing.T, exe string, args []string, env []string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(exe, args...)
	if env != nil {
		cmd.Env = env
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), processExitCode(err)
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func cleanChassisEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CHASSIS_NO_REBUILD=") || strings.HasPrefix(kv, "CHASSIS_REBUILD_GUARD=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
