//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
)

const dockerRequiredEnv = "CHASSIS_E2E_DOCKER_REQUIRED"

func TestFullServiceDockerBuildRunHealthBehaviorAndStop(t *testing.T) {
	chassis.RequireMajor(11)
	requireDockerE2E(t)

	tag := fmt.Sprintf("chassis-go-full-service-e2e:%d", time.Now().UnixNano())
	name := fmt.Sprintf("chassis-go-full-service-e2e-%d", time.Now().UnixNano())
	var imageOwned, containerOwned bool
	t.Cleanup(func() {
		if containerOwned {
			integrationtest.CleanupDocker(t, name, "full-service E2E")
		}
		if imageOwned {
			integrationtest.CleanupDockerImage(t, tag, "full-service E2E")
		}
	})

	adminHostPort := freePort(t)
	httpHostPort := freePort(t)
	var diagnostics bytes.Buffer

	root := repoRoot(t)
	if err := runDockerIn(root, &diagnostics, 5*time.Minute, "build", "-f", "examples/04-full-service/Dockerfile", "-t", tag, "."); err != nil {
		if _, _, inspectErr := dockerOutput(10*time.Second, "image", "inspect", tag); inspectErr == nil {
			imageOwned = true
		}
		t.Fatalf("docker build failed: %v\n%s", err, diagnostics.String())
	}
	imageOwned = true
	if err := runDocker(&diagnostics, 30*time.Second, "run", "-d", "--name", name, "-p", fmt.Sprintf("%d:8080", httpHostPort), "-p", fmt.Sprintf("%d:9090", adminHostPort), tag); err != nil {
		if _, _, inspectErr := dockerOutput(10*time.Second, "inspect", name); inspectErr == nil {
			containerOwned = true
		}
		dockerFailure(t, name, diagnostics.String(), "docker run failed: %v", err)
	}
	containerOwned = true

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", adminHostPort)
	healthBody := waitForHTTPStatus(t, healthURL, http.StatusOK, 45*time.Second)
	assertJSONContains(t, healthBody, "checks")
	waitForDockerHealth(t, name, 35*time.Second)

	stdout, stderr, err := dockerExec(10*time.Second, name, "/server", "--version")
	wantVersion := fmt.Sprintf("server %s (chassis-go %s)\n", chassis.Version, chassis.Version)
	if err != nil || stdout != wantVersion || stderr != "" {
		dockerFailure(t, name, diagnostics.String(), "docker exec --version err=%v stdout=%q stderr=%q want=%q", err, stdout, stderr, wantVersion)
	}

	body, status := postJSON(t, fmt.Sprintf("http://127.0.0.1:%d/v1/demo", httpHostPort), `{"input":"docker"}`)
	if status != http.StatusOK || !strings.Contains(body, "processed: docker") {
		dockerFailure(t, name, diagnostics.String(), "docker host success response status=%d body=%q", status, body)
	}
	body, status = postJSON(t, fmt.Sprintf("http://127.0.0.1:%d/v1/demo", httpHostPort), `{"__proto__":"evil"}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "dangerous key") {
		dockerFailure(t, name, diagnostics.String(), "docker host error response status=%d body=%q", status, body)
	}

	if err := runDocker(&diagnostics, 20*time.Second, "stop", "--time", "8", name); err != nil {
		dockerFailure(t, name, diagnostics.String(), "docker stop failed: %v", err)
	}
}

func requireDockerE2E(t *testing.T) {
	t.Helper()
	required := false
	switch strings.TrimSpace(os.Getenv(dockerRequiredEnv)) {
	case "", "0":
	case "1":
		required = true
	default:
		t.Fatalf("%s must be 0 or 1", dockerRequiredEnv)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		if required {
			t.Fatalf("required T1 Docker E2E needs the Docker CLI: %v", err)
		}
		t.Skipf("docker unavailable; explicit optional T1 Docker E2E skip: %v", err)
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		if required {
			t.Fatalf("required T1 Docker E2E needs a healthy Docker daemon: %v\n%s", err, out)
		}
		t.Skipf("docker daemon unavailable; explicit optional T1 Docker E2E skip: %v\n%s", err, out)
	}
}

func waitForDockerHealth(t *testing.T, container string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		stdout, stderr, err := dockerOutput(5*time.Second, "inspect", "--format={{.State.Health.Status}}", container)
		last = strings.TrimSpace(stdout + stderr)
		if err == nil && strings.TrimSpace(stdout) == "healthy" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container %s did not report healthy within %s; last=%s", container, timeout, last)
}

func dockerOutput(timeout time.Duration, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}

func dockerExec(timeout time.Duration, container string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}

func runDocker(log *bytes.Buffer, timeout time.Duration, args ...string) error {
	return runDockerIn("", log, timeout, args...)
}

func runDockerIn(dir string, log *bytes.Buffer, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	log.WriteString("$ docker " + strings.Join(args, " ") + "\n")
	log.Write(out.Bytes())
	if ctx.Err() == context.DeadlineExceeded {
		return ctx.Err()
	}
	return err
}

func dockerFailure(t *testing.T, container, prior, format string, args ...any) {
	t.Helper()
	var diag bytes.Buffer
	diag.WriteString(prior)
	_ = runDocker(&diag, 10*time.Second, "inspect", container)
	_ = runDocker(&diag, 10*time.Second, "logs", container)
	_ = runDocker(&diag, 10*time.Second, "inspect", "--format={{json .State.Health}}", container)
	t.Fatalf(format+"\nDocker diagnostics:\n%s", append(args, diag.String())...)
}
