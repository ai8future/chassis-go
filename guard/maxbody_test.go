package guard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai8future/chassis-go/v5/guard"
)

func TestMaxBodyRejectsOversizedRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for oversized body")
	})

	handler := guard.MaxBody(10)(inner)
	body := strings.NewReader("this body exceeds the 10 byte limit easily")
	req := httptest.NewRequest("POST", "/", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}

	var pd map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
		t.Fatalf("failed to decode problem detail: %v", err)
	}
	if pd["type"] != "https://chassis.ai8future.com/errors/payload-too-large" {
		t.Errorf("type = %v", pd["type"])
	}
	if pd["title"] != "Payload Too Large" {
		t.Errorf("title = %v", pd["title"])
	}
	if int(pd["status"].(float64)) != 413 {
		t.Errorf("status = %v", pd["status"])
	}
	if pd["detail"] != "request body too large" {
		t.Errorf("detail = %v", pd["detail"])
	}
}

func TestMaxBodyAllowsSmallRequest(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := guard.MaxBody(1024)(inner)
	body := strings.NewReader("small")
	req := httptest.NewRequest("POST", "/", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called for small body")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMaxBodyAllowsGETWithNoBody(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := guard.MaxBody(10)(inner)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called for GET")
	}
}

func TestMaxBodyPanicsOnZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on MaxBody(0)")
		}
	}()
	guard.MaxBody(0)
}

func TestMaxBodyPanicsOnNegative(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on MaxBody(-1)")
		}
	}()
	guard.MaxBody(-1)
}
