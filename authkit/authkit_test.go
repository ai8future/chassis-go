package authkit

import (
	"context"
	"encoding/json"
	"errors"
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

func TestLoadStaticKeysFromEnvUsesDefaultVariable(t *testing.T) {
	fixture := loadCryptoFixture(t)
	t.Setenv(DefaultEnvVar, fixture.Vectors[0].KeyID+"="+fixture.Vectors[0].StoredHash+":jobs:read")

	keys, err := LoadStaticKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadStaticKeysFromEnv: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != fixture.Vectors[0].KeyID || len(keys[0].Scopes) != 1 || keys[0].Scopes[0] != "jobs:read" {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestLoadStaticKeysFromEnvUsesNamedVariable(t *testing.T) {
	fixture := loadCryptoFixture(t)
	t.Setenv(DefaultEnvVar, "malformed")
	t.Setenv("AUTHKIT_TEST_KEYS", fixture.Vectors[0].KeyID+"="+fixture.Vectors[0].StoredHash)

	keys, err := LoadStaticKeysFromEnv("AUTHKIT_TEST_KEYS")
	if err != nil {
		t.Fatalf("LoadStaticKeysFromEnv: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != fixture.Vectors[0].KeyID {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestLoadStaticKeysFromEnvReturnsNoKeysWhenUnset(t *testing.T) {
	t.Setenv(DefaultEnvVar, "")

	keys, err := LoadStaticKeysFromEnv("")
	if err != nil {
		t.Fatalf("LoadStaticKeysFromEnv: %v", err)
	}
	if keys != nil {
		t.Fatalf("keys = %#v, want nil", keys)
	}
}

func TestLoadStaticKeysFromEnvRejectsMalformedEntry(t *testing.T) {
	t.Setenv(DefaultEnvVar, "missing-equals")

	if _, err := LoadStaticKeysFromEnv(); err == nil {
		t.Fatal("expected malformed entry error")
	}
}

func TestStaticVerifierDuplicateIDUsesLastKey(t *testing.T) {
	fixture := loadCryptoFixture(t)
	if len(fixture.Vectors) < 2 {
		t.Fatal("crypto fixture needs two vectors")
	}
	id := fixture.Vectors[0].KeyID
	verifier, err := NewStaticVerifier([]Key{
		{ID: id, StoredHash: fixture.Vectors[0].StoredHash, Scopes: []string{"first"}},
		{ID: id, StoredHash: fixture.Vectors[1].StoredHash, Scopes: []string{"second", "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	principal, err := verifier.VerifyBearer(context.Background(), "Bearer "+id+"."+fixture.Vectors[1].Secret, "second")
	if err != nil {
		t.Fatalf("last duplicate should verify: %v", err)
	}
	if len(principal.Scopes) != 1 || principal.Scopes[0] != "second" {
		t.Fatalf("scopes = %#v", principal.Scopes)
	}
	if _, err := verifier.VerifyBearer(context.Background(), "Bearer "+id+"."+fixture.Vectors[0].Secret); err == nil {
		t.Fatal("first duplicate secret should be replaced")
	}
}

func TestNewStaticVerifierRejectsIncompleteKeys(t *testing.T) {
	tests := []Key{
		{StoredHash: "scrypt$2$1$1$c2FsdA==$a2V5"},
		{ID: "missing-hash"},
	}
	for _, key := range tests {
		if _, err := NewStaticVerifier([]Key{key}); err == nil {
			t.Fatalf("NewStaticVerifier(%#v) succeeded", key)
		}
	}
}

func TestVerifyBearerRejectsMalformedCredentials(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash}})

	for _, authorization := range []string{"", "Basic token", "Bearer", "Bearer .secret", "Bearer key.", "Bearer unknown.secret"} {
		if _, err := verifier.VerifyBearer(context.Background(), authorization); err == nil {
			t.Fatalf("VerifyBearer(%q) succeeded", authorization)
		}
	}
	if _, err := (*StaticVerifier)(nil).VerifyBearer(context.Background(), "Bearer key.secret"); err == nil {
		t.Fatal("nil verifier succeeded")
	}
}

func TestParseStoredHashRejectsInvalidFields(t *testing.T) {
	tests := []string{
		"not-scrypt", "scrypt$x$1$1$c2FsdA==$a2V5", "scrypt$2$x$1$c2FsdA==$a2V5",
		"scrypt$2$1$x$c2FsdA==$a2V5", "scrypt$1$1$1$c2FsdA==$a2V5", "scrypt$2$0$1$c2FsdA==$a2V5",
		"scrypt$2$1$0$c2FsdA==$a2V5", "scrypt$2$1$1$!$a2V5", "scrypt$2$1$1$c2FsdA==$!",
		"scrypt$2$1$1$$a2V5", "scrypt$2$1$1$c2FsdA==$",
	}
	for _, stored := range tests {
		if _, err := parseStoredHash(stored); err == nil {
			t.Fatalf("parseStoredHash(%q) succeeded", stored)
		}
	}
}

func TestInterceptorsRejectMissingMetadata(t *testing.T) {
	fixture := loadCryptoFixture(t)
	verifier := MustStaticVerifier([]Key{{ID: fixture.Vectors[0].KeyID, StoredHash: fixture.Vectors[0].StoredHash}})
	if _, err := UnaryServerInterceptor(verifier)(context.Background(), nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) {
		return nil, errors.New("handler should not run")
	}); err == nil {
		t.Fatal("unary interceptor accepted missing metadata")
	}
	if err := StreamServerInterceptor(verifier)(nil, fakeServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{}, func(any, grpc.ServerStream) error {
		return errors.New("handler should not run")
	}); err == nil {
		t.Fatal("stream interceptor accepted missing metadata")
	}
}

func FuzzParseStaticKeysNeverPanics(f *testing.F) {
	f.Add("")
	f.Add("key=scrypt$2$1$1$c2FsdA==$a2V5:read,write")
	f.Add("malformed")
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = ParseStaticKeys(value)
	})
}
