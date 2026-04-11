// Package announcekit provides standardized lifecycle events for services and jobs.
// It publishes structured events to Kafka subjects following the pattern
// "ai8.infra.{service}.lifecycle.{state}" and "ai8.infra.{service}.job.{state}".
package announcekit

import (
	"context"
	"sync"

	chassis "github.com/ai8future/chassis-go/v11"
)

// publisher interface for dependency inversion — matches kafkakit.Publisher's Publish method.
type publisher interface {
	Publish(ctx context.Context, subject string, data any) error
}

// serviceName must be set before calling lifecycle functions.
var (
	serviceNameVal string
	serviceNameMu  sync.RWMutex
)

// svcName reads the service name under a read lock.
func svcName() string {
	serviceNameMu.RLock()
	defer serviceNameMu.RUnlock()
	if serviceNameVal == "" {
		return "unknown"
	}
	return serviceNameVal
}

// SetServiceName configures the service identity for lifecycle events.
func SetServiceName(name string) {
	chassis.AssertVersionChecked()
	serviceNameMu.Lock()
	serviceNameVal = name
	serviceNameMu.Unlock()
}

// --- Service lifecycle events ---

// Started publishes a service-started lifecycle event.
func Started(ctx context.Context, pub publisher) error {
	name := svcName()
	return pub.Publish(ctx, "ai8.infra."+name+".lifecycle.started", map[string]any{
		"service": name, "state": "started",
	})
}

// Ready publishes a service-ready lifecycle event.
func Ready(ctx context.Context, pub publisher) error {
	name := svcName()
	return pub.Publish(ctx, "ai8.infra."+name+".lifecycle.ready", map[string]any{
		"service": name, "state": "ready",
	})
}

// Stopping publishes a service-stopping lifecycle event.
func Stopping(ctx context.Context, pub publisher) error {
	name := svcName()
	return pub.Publish(ctx, "ai8.infra."+name+".lifecycle.stopping", map[string]any{
		"service": name, "state": "stopping",
	})
}

// Failed publishes a service-failed lifecycle event including the error message.
func Failed(ctx context.Context, pub publisher, err error) error {
	name := svcName()
	errMsg := "<nil>"
	if err != nil {
		errMsg = err.Error()
	}
	return pub.Publish(ctx, "ai8.infra."+name+".lifecycle.failed", map[string]any{
		"service": name, "state": "failed", "error": errMsg,
	})
}

// --- Job lifecycle events ---

// JobStarted publishes a job-started event.
func JobStarted(ctx context.Context, pub publisher, jobName, jobID string) error {
	name := svcName()
	return pub.Publish(ctx, "ai8.infra."+name+".job.started", map[string]any{
		"service": name, "job_name": jobName, "job_id": jobID, "state": "started",
	})
}

// JobComplete publishes a job-complete event with optional result data.
func JobComplete(ctx context.Context, pub publisher, jobName, jobID string, result map[string]any) error {
	name := svcName()
	return pub.Publish(ctx, "ai8.infra."+name+".job.complete", map[string]any{
		"service": name, "job_name": jobName, "job_id": jobID, "state": "complete", "result": result,
	})
}

// JobFailed publishes a job-failed event including the error message.
func JobFailed(ctx context.Context, pub publisher, jobName, jobID string, err error) error {
	name := svcName()
	errMsg := "<nil>"
	if err != nil {
		errMsg = err.Error()
	}
	return pub.Publish(ctx, "ai8.infra."+name+".job.failed", map[string]any{
		"service": name, "job_name": jobName, "job_id": jobID, "state": "failed", "error": errMsg,
	})
}
