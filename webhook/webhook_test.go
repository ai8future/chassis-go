package webhook_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/webhook"
)

func init() { chassis.RequireMajor(11) }

func TestSendAndVerify(t *testing.T) {
	secret := "test-webhook-secret"
	var receivedBody []byte
	var receivedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sender := webhook.NewSender(webhook.MaxAttempts(1))
	id, err := sender.Send(srv.URL, map[string]string{"event": "test"}, secret)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty delivery ID")
	}

	if receivedHeaders.Get("X-Webhook-Id") == "" {
		t.Fatal("missing X-Webhook-Id")
	}
	if receivedHeaders.Get("X-Webhook-Signature") == "" {
		t.Fatal("missing X-Webhook-Signature")
	}
	if receivedHeaders.Get("X-Webhook-Timestamp") == "" {
		t.Fatal("missing X-Webhook-Timestamp")
	}

	verified, err := webhook.VerifyPayload(receivedHeaders, receivedBody, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	var payload map[string]string
	json.Unmarshal(verified, &payload)
	if payload["event"] != "test" {
		t.Fatalf("expected event=test, got %v", payload)
	}
}

func TestSendRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sender := webhook.NewSender(webhook.MaxAttempts(5))
	_, err := sender.Send(srv.URL, "payload", "secret")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestSendNoRetryOn4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(400)
	}))
	defer srv.Close()

	sender := webhook.NewSender(webhook.MaxAttempts(5))
	_, err := sender.Send(srv.URL, "payload", "secret")
	if err == nil {
		t.Fatal("expected error on 4xx")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt (no retry on 4xx), got %d", attempts.Load())
	}
}

func TestDeliveryTracking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sender := webhook.NewSender()
	id, _ := sender.Send(srv.URL, "data", "secret")

	status, ok := sender.Status(id)
	if !ok {
		t.Fatal("expected delivery found")
	}
	if status.Status != "delivered" {
		t.Fatalf("expected delivered, got %q", status.Status)
	}
}

func TestVerifyBadSignature(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Webhook-Signature", "sha256=bad")
	headers.Set("X-Webhook-Timestamp", "9999999999")
	_, err := webhook.VerifyPayload(headers, []byte("body"), "secret")
	if err == nil {
		t.Fatal("expected error for bad signature")
	}
}
