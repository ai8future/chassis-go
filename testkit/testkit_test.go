package testkit_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/config"
	"github.com/ai8future/chassis-go/v11/testkit"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

// testCfg is a small config struct used by SetEnv tests.
type testCfg struct {
	Host string `env:"TESTKIT_HOST"`
	Port int    `env:"TESTKIT_PORT"`
}

func TestNewLogger(t *testing.T) {
	logger := testkit.NewLogger(t)
	// Logging should not panic.
	logger.Info("hello from testkit", "key", "value")
	logger.Debug("debug message")
}

func TestSetEnv(t *testing.T) {
	testkit.SetEnv(t, map[string]string{
		"TESTKIT_HOST": "localhost",
		"TESTKIT_PORT": "9090",
	})
	cfg := config.MustLoad[testCfg]()
	if cfg.Host != "localhost" {
		t.Fatalf("expected Host=localhost, got %q", cfg.Host)
	}
	if cfg.Port != 9090 {
		t.Fatalf("expected Port=9090, got %d", cfg.Port)
	}
}

func TestSetEnvCleanup(t *testing.T) {
	// Use a sub-test so that its cleanup runs before we check the env vars.
	const envKey = "TESTKIT_CLEANUP_CHECK"

	t.Run("inner", func(t *testing.T) {
		testkit.SetEnv(t, map[string]string{
			envKey: "present",
		})
		// Env var should be set inside the test.
		if os.Getenv(envKey) != "present" {
			t.Fatal("env var should be set during the test")
		}
	})

	// After the inner sub-test returns, its cleanup has already run.
	if os.Getenv(envKey) != "" {
		t.Fatalf("expected env var %q to be unset after cleanup, got %q", envKey, os.Getenv(envKey))
	}
}

func TestGetFreePort(t *testing.T) {
	port, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("GetFreePort() error: %v", err)
	}
	if port <= 0 {
		t.Fatalf("expected port > 0, got %d", port)
	}

	// Verify the port is actually usable by listening on it.
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("could not listen on returned port %d: %v", port, err)
	}
	ln.Close()
}

func TestGetFreePortUnique(t *testing.T) {
	p1, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("first GetFreePort() error: %v", err)
	}
	p2, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("second GetFreePort() error: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("expected different ports, both returned %d", p1)
	}
}

func TestNewHTTPServer(t *testing.T) {
	srv := testkit.NewHTTPServer(t, testkit.Respond(200, `{"ok":true}`))

	resp, err := http.Get(srv.URL + "/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request recorded, got %d", len(reqs))
	}
	if reqs[0].Method != "GET" {
		t.Fatalf("expected GET, got %s", reqs[0].Method)
	}
	if reqs[0].Path != "/test" {
		t.Fatalf("expected /test, got %s", reqs[0].Path)
	}
}

func TestSequence(t *testing.T) {
	srv := testkit.NewHTTPServer(t, testkit.Sequence(
		testkit.Respond(500, "error"),
		testkit.Respond(200, "ok"),
	))

	r1, _ := http.Get(srv.URL)
	r1.Body.Close()
	if r1.StatusCode != 500 {
		t.Fatalf("expected 500, got %d", r1.StatusCode)
	}

	r2, _ := http.Get(srv.URL)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", r2.StatusCode)
	}
}
