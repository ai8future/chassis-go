package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/v11/kafkakit"
	"github.com/ai8future/chassis-go/v11/registry"
)

func resetKafkaTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	registry.HeartbeatInterval = 1
	registry.CmdPollInterval = 1
	AnnounceTimeout = 100 * time.Millisecond // short timeout for tests without real broker
	t.Cleanup(func() {
		registry.ResetForTest(t.TempDir())
		AnnounceTimeout = 5 * time.Second
	})
	return tmp
}

func TestRunWithKafkaConfigDisabled(t *testing.T) {
	resetKafkaTest(t)

	// Empty BootstrapServers means kafkakit is disabled -- Run should still work.
	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp, WithKafkaConfig(kafkakit.Config{}))
	if err != nil {
		t.Fatalf("expected nil error with disabled kafkakit, got %v", err)
	}
}

func TestRunWithKafkaConfigEnabled(t *testing.T) {
	resetKafkaTest(t)

	// Provide a valid-looking config; NewPublisher will succeed (franz-go
	// does not connect until first produce). The component exits immediately.
	// Announcements will time out quickly due to short AnnounceTimeout.
	cfg := kafkakit.Config{
		BootstrapServers: "localhost:19092",
		Source:           "test-svc",
	}

	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp, WithKafkaConfig(cfg))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunWithKafkaConfigSourceDefaultsToServiceName(t *testing.T) {
	resetKafkaTest(t)

	// Config without Source -- should default to the resolved service name.
	cfg := kafkakit.Config{
		BootstrapServers: "localhost:19092",
	}

	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp, WithKafkaConfig(cfg))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunWithServiceNameOption(t *testing.T) {
	resetKafkaTest(t)

	cfg := kafkakit.Config{
		BootstrapServers: "localhost:19092",
	}

	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp,
		WithKafkaConfig(cfg),
		WithServiceName("my-custom-svc"),
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunWithKafkaConfigAndComponentError(t *testing.T) {
	resetKafkaTest(t)

	cfg := kafkakit.Config{
		BootstrapServers: "localhost:19092",
		Source:           "test-svc",
	}

	compErr := errors.New("component-failed")
	comp := func(ctx context.Context) error {
		return compErr
	}

	err := Run(context.Background(), comp, WithKafkaConfig(cfg))
	if !errors.Is(err, compErr) {
		t.Fatalf("expected %v, got %v", compErr, err)
	}
}

func TestRunWithKafkaConfigRegistryIntegration(t *testing.T) {
	tmp := resetKafkaTest(t)

	name := os.Getenv("CHASSIS_SERVICE_NAME")
	if name == "" {
		wd, _ := os.Getwd()
		name = filepath.Base(wd)
	}
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")

	cfg := kafkakit.Config{
		BootstrapServers: "localhost:19092",
		Source:           "test-svc",
	}

	comp := func(ctx context.Context) error {
		// Registry PID file should exist during Run.
		if _, err := os.Stat(pidFile); err != nil {
			return errors.New("PID file should exist during Run: " + err.Error())
		}
		return nil
	}

	err := Run(context.Background(), comp, WithKafkaConfig(cfg))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// PID file should be cleaned up after shutdown.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Run completes")
	}
}

func TestRunOptionsMixedWithComponents(t *testing.T) {
	resetKafkaTest(t)

	// Verify that options can be mixed freely with components.
	comp1 := func(ctx context.Context) error { return nil }
	comp2 := Component(func(ctx context.Context) error { return nil })

	err := Run(context.Background(),
		WithServiceName("test-svc"),
		comp1,
		WithKafkaConfig(kafkakit.Config{}), // disabled
		comp2,
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestResolveName(t *testing.T) {
	// Without CHASSIS_SERVICE_NAME set, should return cwd basename.
	orig := os.Getenv("CHASSIS_SERVICE_NAME")
	os.Unsetenv("CHASSIS_SERVICE_NAME")
	defer func() {
		if orig != "" {
			os.Setenv("CHASSIS_SERVICE_NAME", orig)
		}
	}()

	name := resolveName()
	wd, _ := os.Getwd()
	expected := filepath.Base(wd)
	if name != expected {
		t.Fatalf("resolveName() = %q, want %q", name, expected)
	}
}

func TestResolveNameFromEnv(t *testing.T) {
	orig := os.Getenv("CHASSIS_SERVICE_NAME")
	os.Setenv("CHASSIS_SERVICE_NAME", "env-svc-name")
	defer func() {
		if orig != "" {
			os.Setenv("CHASSIS_SERVICE_NAME", orig)
		} else {
			os.Unsetenv("CHASSIS_SERVICE_NAME")
		}
	}()

	name := resolveName()
	if name != "env-svc-name" {
		t.Fatalf("resolveName() = %q, want %q", name, "env-svc-name")
	}
}

func TestWithKafkaConfigOption(t *testing.T) {
	cfg := kafkakit.Config{
		BootstrapServers: "broker:9092",
		Source:           "my-svc",
	}

	var o options
	WithKafkaConfig(cfg)(&o)

	if o.kafkaCfg == nil {
		t.Fatal("expected kafkaCfg to be set")
	}
	if o.kafkaCfg.BootstrapServers != "broker:9092" {
		t.Fatalf("expected BootstrapServers=broker:9092, got %s", o.kafkaCfg.BootstrapServers)
	}
	if o.kafkaCfg.Source != "my-svc" {
		t.Fatalf("expected Source=my-svc, got %s", o.kafkaCfg.Source)
	}
}

func TestWithServiceNameOption_Unit(t *testing.T) {
	var o options
	WithServiceName("custom-name")(&o)

	if o.serviceName != "custom-name" {
		t.Fatalf("expected serviceName=custom-name, got %s", o.serviceName)
	}
}
