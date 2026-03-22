// Package announcekit provides standardized lifecycle events for services and jobs.
// It publishes structured events to Kafka subjects following the pattern
// "ai8.infra.{service}.lifecycle.{state}" and "ai8.infra.{service}.job.{state}".
package announcekit

import (
	"context"

	chassis "github.com/ai8future/chassis-go/v9"
)

// publisher interface for dependency inversion — matches kafkakit.Publisher's Publish method.
type publisher interface {
	Publish(ctx context.Context, subject string, data any) error
}

// serviceName must be set before calling lifecycle functions.
var serviceName string

// SetServiceName configures the service identity for lifecycle events.
func SetServiceName(name string) {
	chassis.AssertVersionChecked()
	serviceName = name
}

// --- Service lifecycle events ---

// Started publishes a service-started lifecycle event.
func Started(ctx context.Context, pub publisher) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.started", map[string]any{
		"service": serviceName, "state": "started",
	})
}

// Ready publishes a service-ready lifecycle event.
func Ready(ctx context.Context, pub publisher) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.ready", map[string]any{
		"service": serviceName, "state": "ready",
	})
}

// Stopping publishes a service-stopping lifecycle event.
func Stopping(ctx context.Context, pub publisher) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.stopping", map[string]any{
		"service": serviceName, "state": "stopping",
	})
}

// Failed publishes a service-failed lifecycle event including the error message.
func Failed(ctx context.Context, pub publisher, err error) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.failed", map[string]any{
		"service": serviceName, "state": "failed", "error": err.Error(),
	})
}

// --- Job lifecycle events ---

// JobStarted publishes a job-started event.
func JobStarted(ctx context.Context, pub publisher, jobName, jobID string) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".job.started", map[string]any{
		"service": serviceName, "job_name": jobName, "job_id": jobID, "state": "started",
	})
}

// JobComplete publishes a job-complete event with optional result data.
func JobComplete(ctx context.Context, pub publisher, jobName, jobID string, result map[string]any) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".job.complete", map[string]any{
		"service": serviceName, "job_name": jobName, "job_id": jobID, "state": "complete", "result": result,
	})
}

// JobFailed publishes a job-failed event including the error message.
func JobFailed(ctx context.Context, pub publisher, jobName, jobID string, err error) error {
	return pub.Publish(ctx, "ai8.infra."+serviceName+".job.failed", map[string]any{
		"service": serviceName, "job_name": jobName, "job_id": jobID, "state": "failed", "error": err.Error(),
	})
}
