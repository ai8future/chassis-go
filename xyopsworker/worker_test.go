package xyopsworker_test

import (
	"context"
	"testing"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/xyopsworker"
)

func init() { chassis.RequireMajor(10) }

func TestNewWorker(t *testing.T) {
	w := xyopsworker.New(xyopsworker.Config{
		MasterURL: "wss://xyops.example.com:5523",
		SecretKey: "test-secret",
	})
	if w == nil {
		t.Fatal("expected non-nil worker")
	}
}

func TestHandleAndDispatch(t *testing.T) {
	w := xyopsworker.New(xyopsworker.Config{
		MasterURL: "wss://test",
		SecretKey: "secret",
	})

	var called bool
	var gotParams map[string]string
	w.Handle("deploy", func(ctx context.Context, job xyopsworker.Job) error {
		called = true
		gotParams = job.Params
		return nil
	})

	job := xyopsworker.Job{
		ID:      "job-1",
		EventID: "deploy",
		Params:  map[string]string{"env": "prod"},
	}

	err := w.Dispatch(context.Background(), job)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Fatal("handler not called")
	}
	if gotParams["env"] != "prod" {
		t.Fatalf("expected env=prod, got %v", gotParams)
	}
}

func TestDispatchNoHandler(t *testing.T) {
	w := xyopsworker.New(xyopsworker.Config{
		MasterURL: "wss://test",
		SecretKey: "secret",
	})

	err := w.Dispatch(context.Background(), xyopsworker.Job{EventID: "unknown"})
	if err == nil {
		t.Fatal("expected error for unregistered handler")
	}
}

func TestDispatchShellEnabledNoHandler(t *testing.T) {
	w := xyopsworker.New(xyopsworker.Config{
		MasterURL:    "wss://test",
		SecretKey:    "secret",
		ShellEnabled: true,
	})

	err := w.Dispatch(context.Background(), xyopsworker.Job{EventID: "unknown-cmd"})
	if err == nil {
		t.Fatal("expected error for shell not-yet-implemented")
	}
}

func TestHasHandler(t *testing.T) {
	w := xyopsworker.New(xyopsworker.Config{
		MasterURL: "wss://test",
		SecretKey: "secret",
	})

	if w.HasHandler("deploy") {
		t.Fatal("expected no handler before registration")
	}

	w.Handle("deploy", func(ctx context.Context, job xyopsworker.Job) error { return nil })

	if !w.HasHandler("deploy") {
		t.Fatal("expected handler after registration")
	}
}

func TestJobMethodsNilCallbacks(t *testing.T) {
	job := xyopsworker.Job{
		ID:      "job-1",
		EventID: "test",
	}
	// Job methods should not panic even without callbacks set
	job.Progress(50, "halfway")
	job.Log("test log")
	job.SetOutput("result")
}
