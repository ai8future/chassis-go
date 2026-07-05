package authkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

type cryptoFixture struct {
	Vectors []struct {
		KeyID      string `json:"key_id"`
		Secret     string `json:"secret"`
		StoredHash string `json:"stored_hash"`
		Token      string `json:"token"`
	} `json:"vectors"`
}

func loadCryptoFixture(t *testing.T) cryptoFixture {
	t.Helper()
	path := filepath.Join("..", "testdata", "windmill", "contracts", "fixtures", "crypto-vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crypto vectors: %v", err)
	}
	var fixture cryptoFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode crypto vectors: %v", err)
	}
	return fixture
}

func TestStaticVerifierCryptoVectors(t *testing.T) {
	fixture := loadCryptoFixture(t)
	var keys []Key
	for _, vector := range fixture.Vectors {
		keys = append(keys, Key{ID: vector.KeyID, StoredHash: vector.StoredHash, Scopes: []string{"jobs:write"}})
	}
	verifier, err := NewStaticVerifier(keys)
	if err != nil {
		t.Fatalf("NewStaticVerifier: %v", err)
	}
	for _, vector := range fixture.Vectors {
		principal, err := verifier.VerifyBearer(context.Background(), "Bearer "+vector.Token, "jobs:write")
		if err != nil {
			t.Fatalf("VerifyBearer(%s): %v", vector.KeyID, err)
		}
		if principal.KeyID != vector.KeyID {
			t.Fatalf("principal key = %q, want %q", principal.KeyID, vector.KeyID)
		}
	}
	decomposed := "wm_nfc.cafe\u0301-latte\u0301"
	if _, err := verifier.VerifyBearer(context.Background(), "Bearer "+decomposed, "jobs:write"); err != nil {
		t.Fatalf("decomposed NFC secret should verify: %v", err)
	}
}

func TestHTTPMiddlewarePrincipalAndProblems(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash, Scopes: []string{"jobs:write"}}})

	handler := HTTPMiddleware(verifier, "jobs:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.KeyID != fixture.Vectors[0].KeyID {
			t.Fatalf("missing principal in handler")
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.Vectors[0].Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/jobs", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rec.Code)
	}
	var problem map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem["code"] != "auth_unauthorized" {
		t.Fatalf("problem code = %v, want auth_unauthorized", problem["code"])
	}
}

func TestHTTPMiddlewareMissingScope(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash, Scopes: []string{"jobs:read"}}})
	handler := HTTPMiddleware(verifier, "jobs:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))
	req := httptest.NewRequest(http.MethodPost, "/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.Vectors[0].Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestUnaryServerInterceptor(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash, Scopes: []string{"jobs:write"}}})
	interceptor := UnaryServerInterceptor(verifier, "jobs:write")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+fixture.Vectors[0].Token))
	_, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/test.Service/Call"}, func(ctx context.Context, req any) (any, error) {
		if _, ok := PrincipalFromContext(ctx); !ok {
			t.Fatal("missing principal")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
}

func TestStreamServerInterceptor(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash, Scopes: []string{"jobs:write"}}})
	interceptor := StreamServerInterceptor(verifier, "jobs:write")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+fixture.Vectors[0].Token))
	err := interceptor("service", fakeServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, func(srv any, stream grpc.ServerStream) error {
		if _, ok := PrincipalFromContext(stream.Context()); !ok {
			t.Fatal("missing principal")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream interceptor returned error: %v", err)
	}
}

type fakeServerStream struct {
	ctx context.Context
}

func (s fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s fakeServerStream) SetTrailer(metadata.MD)       {}
func (s fakeServerStream) Context() context.Context     { return s.ctx }
func (s fakeServerStream) SendMsg(any) error            { return nil }
func (s fakeServerStream) RecvMsg(any) error            { return nil }

func TestParseStaticKeys(t *testing.T) {
	fixture := loadCryptoFixture(t)
	text := fixture.Vectors[0].KeyID + "=" + fixture.Vectors[0].StoredHash + ":jobs:read,jobs:write"
	keys, err := ParseStaticKeys(text)
	if err != nil {
		t.Fatalf("ParseStaticKeys: %v", err)
	}
	if len(keys) != 1 || len(keys[0].Scopes) != 2 {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestStaticVerifierRejectsInvalidScryptHash(t *testing.T) {
	if _, err := NewStaticVerifier([]Key{{ID: "bad", StoredHash: "scrypt$3$8$1$c2FsdA==$a2V5", Scopes: []string{"jobs:write"}}}); err == nil {
		t.Fatal("expected invalid scrypt N to be rejected")
	}
}
