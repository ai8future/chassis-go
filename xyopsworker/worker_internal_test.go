package xyopsworker

import (
	"context"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
)

func init() { chassis.RequireMajor(10) }

func TestJobMethodsInvokeCallbacks(t *testing.T) {
	var (
		gotPct    int
		gotMsg    string
		gotLog    string
		gotOutput string
	)

	job := Job{
		progress: func(pct int, msg string) {
			gotPct = pct
			gotMsg = msg
		},
		log: func(msg string) {
			gotLog = msg
		},
		output: func(data string) {
			gotOutput = data
		},
	}

	job.Progress(60, "halfway")
	job.Log("worker log")
	job.SetOutput("done")

	if gotPct != 60 || gotMsg != "halfway" {
		t.Fatalf("unexpected progress callback values: pct=%d msg=%q", gotPct, gotMsg)
	}
	if gotLog != "worker log" {
		t.Fatalf("unexpected log callback value: %q", gotLog)
	}
	if gotOutput != "done" {
		t.Fatalf("unexpected output callback value: %q", gotOutput)
	}
}

func TestRunReturnsNilOnContextCancellation(t *testing.T) {
	w := New(Config{
		MasterURL: "wss://xyops.example.com:5523",
		SecretKey: "test-secret",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- w.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
