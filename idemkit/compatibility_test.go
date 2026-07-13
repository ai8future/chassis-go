package idemkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/idemkit"
)

func init() { chassis.RequireMajor(11) }

var _ idemkit.Store = (*thirdPartyV11Store)(nil)

// thirdPartyV11Store intentionally implements only the original v11 Store
// interface. This external-package test prevents the additive LeaseStore API
// from accidentally becoming mandatory.
type thirdPartyV11Store struct {
	completes atomic.Int32
}

func (*thirdPartyV11Store) Begin(context.Context, string, string, string) (idemkit.Claim, error) {
	return idemkit.Claim{Result: idemkit.Started}, nil
}

func (s *thirdPartyV11Store) Complete(context.Context, string, string, string, idemkit.StoredResponse) error {
	s.completes.Add(1)
	return nil
}

func (*thirdPartyV11Store) Release(context.Context, string, string) error { return nil }

func TestOriginalStoreImplementationRemainsCompatible(t *testing.T) {
	store := &thirdPartyV11Store{}
	if _, ok := any(store).(idemkit.LeaseStore); ok {
		t.Fatal("original Store unexpectedly implements LeaseStore")
	}
	h := idemkit.Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("visible only after Complete"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/operation", strings.NewReader("body"))
	req.Header.Set(idemkit.DefaultHeader, "legacy-compatible")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || rec.Body.String() != "visible only after Complete" {
		t.Fatalf("response = %d/%q", rec.Code, rec.Body.String())
	}
	if store.completes.Load() != 1 {
		t.Fatalf("Complete calls = %d, want 1", store.completes.Load())
	}
}
