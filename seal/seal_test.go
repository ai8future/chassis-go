package seal_test

import (
	"testing"

	chassis "github.com/ai8future/chassis-go/v8"
	"github.com/ai8future/chassis-go/v8/seal"
)

func init() { chassis.RequireMajor(8) }

func TestEncryptDecrypt(t *testing.T) {
	plaintext := []byte("hello, world")
	passphrase := "test-passphrase-32chars-minimum!"

	env, err := seal.Encrypt(plaintext, passphrase)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if env.Version != 1 {
		t.Fatalf("expected version 1, got %d", env.Version)
	}
	if env.Algorithm != "aes-256-gcm" {
		t.Fatalf("expected aes-256-gcm, got %s", env.Algorithm)
	}

	got, err := seal.Decrypt(env, passphrase)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("roundtrip failed: got %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	env, _ := seal.Encrypt([]byte("secret"), "correct-passphrase-is-long-enough")
	_, err := seal.Decrypt(env, "wrong-passphrase-is-also-long!!")
	if err == nil {
		t.Fatal("expected error for wrong passphrase")
	}
}

func TestEncryptProducesUniqueOutput(t *testing.T) {
	passphrase := "same-passphrase-for-both-calls!!"
	e1, _ := seal.Encrypt([]byte("same"), passphrase)
	e2, _ := seal.Encrypt([]byte("same"), passphrase)
	if e1.Salt == e2.Salt {
		t.Fatal("expected unique salt per encryption")
	}
	if e1.IV == e2.IV {
		t.Fatal("expected unique IV per encryption")
	}
}

func TestSignVerify(t *testing.T) {
	payload := []byte("important data")
	secret := "my-secret-key"

	sig := seal.Sign(payload, secret)
	if sig == "" {
		t.Fatal("expected non-empty signature")
	}
	if !seal.Verify(payload, sig, secret) {
		t.Fatal("valid signature rejected")
	}
	if seal.Verify(payload, sig, "wrong-secret") {
		t.Fatal("invalid secret accepted")
	}
	if seal.Verify([]byte("tampered"), sig, secret) {
		t.Fatal("tampered payload accepted")
	}
}

func TestNewTokenValidateToken(t *testing.T) {
	secret := "token-signing-secret"
	claims := map[string]any{"user": "alice", "role": "admin"}

	token, err := seal.NewToken(claims, secret, 5*60)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, err := seal.ValidateToken(token, secret)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if got["user"] != "alice" {
		t.Fatalf("expected user=alice, got %v", got["user"])
	}
	if got["role"] != "admin" {
		t.Fatalf("expected role=admin, got %v", got["role"])
	}
	if _, ok := got["jti"]; !ok {
		t.Fatal("expected jti claim")
	}
	if _, ok := got["exp"]; !ok {
		t.Fatal("expected exp claim")
	}
}

func TestValidateTokenWrongSecret(t *testing.T) {
	token, _ := seal.NewToken(map[string]any{}, "secret1", 300)
	_, err := seal.ValidateToken(token, "secret2")
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestValidateTokenExpired(t *testing.T) {
	token, _ := seal.NewToken(map[string]any{}, "secret", -1)
	_, err := seal.ValidateToken(token, "secret")
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}
