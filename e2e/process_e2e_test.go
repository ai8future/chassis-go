//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/testkit"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func TestShippedExecutablesBuildAndReportVersion(t *testing.T) {
	bins := []struct {
		name       string
		pkg        string
		appVersion string
	}{
		{name: "01-cli", pkg: "./examples/01-cli", appVersion: chassis.Version},
		{name: "02-service", pkg: "./examples/02-service", appVersion: chassis.Version},
		{name: "03-client", pkg: "./examples/03-client", appVersion: chassis.Version},
		{name: "04-full-service", pkg: "./examples/04-full-service", appVersion: chassis.Version},
		{name: "05-clikit", pkg: "./examples/05-clikit", appVersion: readTrimmed(t, filepath.Join(repoRoot(t), "examples", "05-clikit", "VERSION"))},
		{name: "demo-shutdown", pkg: "./cmd/demo-shutdown", appVersion: chassis.Version},
	}
	for _, bin := range bins {
		t.Run(bin.name, func(t *testing.T) {
			exe := buildBinary(t, bin.name, bin.pkg)
			stdout, stderr, code := runCommand(t, 5*time.Second, nil, exe, "--version")
			want := fmt.Sprintf("%s %s (chassis-go %s)\n", filepath.Base(exe), bin.appVersion, chassis.Version)
			if code != 0 || stdout != want || stderr != "" {
				t.Fatalf("--version code/stdout/stderr = %d/%q/%q, want 0/%q/empty", code, stdout, stderr, want)
			}
		})
	}
}

func TestExample01CLIRunsWithConfiguredEnvironment(t *testing.T) {
	exe := buildBinary(t, "01-cli", "./examples/01-cli")
	stdout, stderr, code := runCommand(t, 5*time.Second, map[string]string{
		"APP_NAME":  "e2e-cli",
		"LOG_LEVEL": "debug",
		"GREETING":  "hello from e2e",
	}, exe)
	combined := stdout + stderr
	if code != 0 {
		t.Fatalf("01-cli code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"application started", "e2e-cli", "hello from e2e", "application finished"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("01-cli output missing %q: stdout=%q stderr=%q", want, stdout, stderr)
		}
	}
}

func TestExample03ClientUsesLoopbackAndReportsFailures(t *testing.T) {
	var hits int
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})}
	ln := listenLoopback(t)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	exe := buildBinary(t, "03-client", "./examples/03-client")
	stdout, stderr, code := runCommand(t, 10*time.Second, map[string]string{
		"TARGET_URL": "http://" + ln.Addr().String(),
		"LOG_LEVEL":  "debug",
	}, exe)
	combined := stdout + stderr
	if code != 0 || hits != 3 || !strings.Contains(combined, "request 3 complete") || !strings.Contains(combined, "client demo finished") {
		t.Fatalf("loopback client code=%d hits=%d stdout=%q stderr=%q", code, hits, stdout, stderr)
	}

	stdout, stderr, code = runCommand(t, 8*time.Second, map[string]string{
		"TARGET_URL": "http://127.0.0.1:1/",
		"LOG_LEVEL":  "debug",
	}, exe)
	combined = stdout + stderr
	if code != 0 || !strings.Contains(combined, "request failed") || !strings.Contains(combined, "client demo finished") {
		t.Fatalf("failure-path client code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestExample02GRPCHealthReadinessShutdownAndPortRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal assertions use POSIX signals")
	}
	port := freePort(t)
	exe := buildBinary(t, "02-service", "./examples/02-service")
	proc := startProcess(t, exe, map[string]string{"PORT": fmt.Sprint(port), "LOG_LEVEL": "debug"})
	defer proc.cleanup()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn := waitForGRPCServing(t, addr, 12*time.Second)
	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: "unknown-service-current-contract"})
	cancel()
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("unknown-service health current contract status=%v err=%v", resp.GetStatus(), err)
	}
	_ = conn.Close()

	proc.signalAndWait(t, syscall.SIGTERM, 8*time.Second)
	assertPortReleased(t, port, 5*time.Second)
}

func TestExample04FullServiceHTTPReadinessBehaviorShutdownAndPortRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal assertions use POSIX signals")
	}
	httpPort := freePort(t)
	adminPort := freePort(t)
	exe := buildBinary(t, "04-full-service", "./examples/04-full-service")
	proc := startProcess(t, exe, map[string]string{
		"HTTP_PORT":  fmt.Sprint(httpPort),
		"ADMIN_PORT": fmt.Sprint(adminPort),
		"LOG_LEVEL":  "debug",
	})
	defer proc.cleanup()

	adminURL := fmt.Sprintf("http://127.0.0.1:%d/health", adminPort)
	waitForHTTPStatus(t, adminURL, http.StatusOK, 12*time.Second)
	body, status := postJSON(t, fmt.Sprintf("http://127.0.0.1:%d/v1/demo", httpPort), `{"input":"hello"}`)
	if status != http.StatusOK || !strings.Contains(body, "processed: hello") {
		t.Fatalf("success response status=%d body=%q", status, body)
	}
	body, status = postJSON(t, fmt.Sprintf("http://127.0.0.1:%d/v1/demo", httpPort), `{"__proto__":"evil"}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "dangerous key") {
		t.Fatalf("invalid response status=%d body=%q", status, body)
	}

	proc.signalAndWait(t, syscall.SIGTERM, 15*time.Second)
	assertPortReleased(t, httpPort, 5*time.Second)
	assertPortReleased(t, adminPort, 5*time.Second)
}

func TestDemoShutdownHandlesSignalAndDrains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal assertions use POSIX signals")
	}
	exe := buildBinary(t, "demo-shutdown", "./cmd/demo-shutdown")
	proc := startProcess(t, exe, nil)
	defer proc.cleanup()
	time.Sleep(1200 * time.Millisecond)
	proc.signalAndWait(t, syscall.SIGTERM, 8*time.Second)
	combined := proc.stdout.String() + proc.stderr.String()
	for _, want := range []string{"received shutdown signal", "worker 1 drained", "worker 2 drained", "clean shutdown complete"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("demo-shutdown output missing %q: %s", want, combined)
		}
	}
}

func buildBinary(t *testing.T, name, pkg string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), exeName(name))
	cmd := exec.Command("go", "build", "-o", exe, pkg)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "CHASSIS_NO_REBUILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s failed: %v\n%s", pkg, err, out)
	}
	return exe
}

func runCommand(t *testing.T, timeout time.Duration, env map[string]string, exe string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = commandEnv(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command timed out: %s %v stdout=%q stderr=%q", exe, args, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), exitCode(err)
}

type runningProcess struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func startProcess(t *testing.T, exe string, env map[string]string, args ...string) *runningProcess {
	t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Env = commandEnv(env)
	p := &runningProcess{cmd: cmd}
	cmd.Stdout = &p.stdout
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", exe, err)
	}
	return p
}

func (p *runningProcess) signalAndWait(t *testing.T, sig os.Signal, timeout time.Duration) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal process: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if code := exitCode(err); code != 0 {
			t.Fatalf("process exit code=%d err=%v stdout=%q stderr=%q", code, err, p.stdout.String(), p.stderr.String())
		}
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		t.Fatalf("process did not stop after %s stdout=%q stderr=%q", timeout, p.stdout.String(), p.stderr.String())
	}
}

func (p *runningProcess) cleanup() {
	if p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

func waitForGRPCServing(t *testing.T, addr string, timeout time.Duration) *grpc.ClientConn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
		cancel()
		if err == nil {
			client := healthpb.NewHealthClient(conn)
			ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
			resp, checkErr := client.Check(ctx, &healthpb.HealthCheckRequest{})
			cancel()
			if checkErr == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
				return conn
			}
			lastErr = checkErr
			_ = conn.Close()
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("gRPC health at %s not ready within %s: %v", addr, timeout, lastErr)
	return nil
}

func waitForHTTPStatus(t *testing.T, url string, want int, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(body))
			if resp.StatusCode == want {
				cancel()
				return string(body)
			}
		} else {
			last = err.Error()
		}
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d within %s; last=%s", url, want, timeout, last)
	return ""
}

func postJSON(t *testing.T, url, payload string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(body), resp.StatusCode
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback: %v", err)
	}
	return ln
}

func freePort(t *testing.T) int {
	t.Helper()
	port, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	return port
}

func assertPortReleased(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var lastErr error
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d was not released within %s: %v", port, timeout, lastErr)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func commandEnv(extra map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "CHASSIS_NO_REBUILD=1")
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
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

func assertJSONContains(t *testing.T, body string, key string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("response is not JSON: %v body=%q", err, body)
	}
	if _, ok := m[key]; !ok {
		t.Fatalf("response missing key %q: %q", key, body)
	}
}
