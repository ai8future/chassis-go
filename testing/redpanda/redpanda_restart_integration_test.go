//go:build integration

package redpandaintegration_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	"github.com/ai8future/chassis-go/v11/kafkakit"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	restartRequiredEnv  = "CHASSIS_REDPANDA_RESTART_REQUIRED"
	restartBootstrapEnv = "CHASSIS_REDPANDA_RESTART_BOOTSTRAP"
	restartAdminURLEnv  = "CHASSIS_REDPANDA_RESTART_ADMIN_URL"
	restartContainerEnv = "CHASSIS_REDPANDA_RESTART_CONTAINER"
)

func TestRedpandaModuleClientRestartProbe(t *testing.T) {
	chassis.RequireMajor(11)
	if os.Getenv(restartRequiredEnv) != "1" {
		t.Skipf("nightly Redpanda restart probe requires %s=1", restartRequiredEnv)
	}
	values := integrationtest.RequireEnv(t, restartBootstrapEnv, restartAdminURLEnv, restartContainerEnv)
	integrationtest.RequireDocker(t, "redpanda restart probe")

	admin := newKafkaClient(t, values[restartBootstrapEnv])
	defer admin.Close()
	beforeTopic := uniqueTopic("restart-before")
	afterTopic := uniqueTopic("restart-after")
	createTopic(t, admin, beforeTopic, 1)
	createTopic(t, admin, afterTopic, 1)

	publisher, err := kafkakit.NewPublisher(kafkakit.Config{
		BootstrapServers: values[restartBootstrapEnv],
		Source:           "nightly-restart-probe",
		Publisher:        kafkakit.PublisherConfig{Acks: "all", DisableLinger: true},
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer publisher.Close()

	assertRestartModuleRoundTrip(t, publisher, values[restartBootstrapEnv], beforeTopic, "before")
	t.Log("CHASSIS_REDPANDA_RESTART_PROBE:before")
	restartContainer(t, values[restartContainerEnv])
	waitForRestartReady(t, admin, values[restartAdminURLEnv])
	assertRestartModuleRoundTrip(t, publisher, values[restartBootstrapEnv], afterTopic, "after")
	t.Log("CHASSIS_REDPANDA_RESTART_PROBE:after")

	if stats := publisher.Stats(); stats.EventsPublishedTotal != 2 || stats.ErrorsTotal != 0 {
		t.Fatalf("continuous publisher stats after restart = %+v, want 2 published and 0 errors", stats)
	}
}

func assertRestartModuleRoundTrip(t *testing.T, publisher *kafkakit.Publisher, bootstrap, topic, phase string) {
	t.Helper()
	events := make(chan kafkakit.Event, 1)
	subscriber := startModuleSubscriber(t, bootstrap, uniqueTopic("restart-group"), map[string]kafkakit.HandlerFunc{
		topic: func(_ context.Context, event kafkakit.Event) error {
			events <- event
			return nil
		},
	})
	defer subscriber.stop(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wantData := map[string]any{"phase": phase}
	if err := publisher.Publish(ctx, topic, wantData); err != nil {
		t.Fatalf("Publish %s restart event: %v", phase, err)
	}
	event := receiveEvent(t, events, 30*time.Second)
	if event.Subject != topic || event.Source != "nightly-restart-probe" || !reflect.DeepEqual(event.Data, wantData) {
		t.Fatalf("%s restart event = %+v", phase, event)
	}
}

func restartContainer(t *testing.T, container string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "restart", container).CombinedOutput()
	if err != nil {
		t.Fatalf("restart Redpanda container %s: %v\n%s", container, err, out)
	}
	t.Logf("restarted Redpanda container %s: %s", container, out)
}

func waitForRestartReady(t *testing.T, admin *kgo.Client, adminURL string) {
	t.Helper()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	waitFor(t, 90*time.Second, func() (bool, string) {
		resp, err := httpClient.Get(adminURL + "/v1/status/ready")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK, fmt.Sprintf("admin status=%d", resp.StatusCode)
	})
	waitFor(t, 60*time.Second, func() (bool, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req := kmsg.NewPtrMetadataRequest()
		req.Topics = []kmsg.MetadataRequestTopic{}
		_, err := req.RequestWith(ctx, admin)
		if err != nil {
			return false, err.Error()
		}
		return true, "metadata ok after restart"
	})
}
