package seal_test

import (
	"encoding/base64"
	"errors"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/seal"
)

func init() { chassis.RequireMajor(11) }

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

func TestSignPanicsOnEmptySecret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty secret")
		}
	}()
	seal.Sign([]byte("payload"), "")
}

func TestVerifyEmptySecretIsFalse(t *testing.T) {
	if seal.Verify([]byte("payload"), seal.Sign([]byte("payload"), "k"), "") {
		t.Fatal("empty secret must never verify")
	}
}

func TestEncryptEmptyPassphraseErrors(t *testing.T) {
	if _, err := seal.Encrypt([]byte("data"), ""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestDecryptEmptyPassphraseErrors(t *testing.T) {
	env, err := seal.Encrypt([]byte("data"), "non-empty-passphrase")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := seal.Decrypt(env, ""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestDecryptMalformedEnvelopeNeverPanics(t *testing.T) {
	passphrase := "test-passphrase-32chars-minimum!"
	valid, err := seal.Encrypt([]byte("secret"), passphrase)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tests := map[string]func(seal.Envelope) seal.Envelope{
		"malformed salt base64": func(env seal.Envelope) seal.Envelope {
			env.Salt = "%%%"
			return env
		},
		"short salt": func(env seal.Envelope) seal.Envelope {
			env.Salt = base64.StdEncoding.EncodeToString(make([]byte, 15))
			return env
		},
		"long salt": func(env seal.Envelope) seal.Envelope {
			env.Salt = base64.StdEncoding.EncodeToString(make([]byte, 17))
			return env
		},
		"short nonce": func(env seal.Envelope) seal.Envelope {
			env.IV = base64.StdEncoding.EncodeToString([]byte{1})
			return env
		},
		"long nonce": func(env seal.Envelope) seal.Envelope {
			env.IV = base64.StdEncoding.EncodeToString(make([]byte, 13))
			return env
		},
		"short tag": func(env seal.Envelope) seal.Envelope {
			env.Tag = base64.StdEncoding.EncodeToString(make([]byte, 15))
			return env
		},
		"long tag": func(env seal.Envelope) seal.Envelope {
			env.Tag = base64.StdEncoding.EncodeToString(make([]byte, 17))
			return env
		},
		"empty ciphertext with mismatched tag": func(env seal.Envelope) seal.Envelope {
			env.CT = ""
			return env
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := seal.Decrypt(mutate(valid), passphrase)
			if !errors.Is(err, seal.ErrDecrypt) {
				t.Fatalf("Decrypt error = %v, want ErrDecrypt", err)
			}
		})
	}
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	const passphrase = "test-passphrase-32chars-minimum!"
	env, err := seal.Encrypt(nil, passphrase)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := seal.Decrypt(env, passphrase)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(plaintext) != 0 {
		t.Fatalf("Decrypt returned %d bytes, want empty plaintext", len(plaintext))
	}
}

func FuzzDecryptEnvelopeNeverPanics(f *testing.F) {
	const passphrase = "test-passphrase-32chars-minimum!"
	valid, err := seal.Encrypt([]byte("seed"), passphrase)
	if err != nil {
		f.Fatalf("Encrypt seed: %v", err)
	}
	f.Add(valid.Version, valid.Algorithm, valid.Salt, valid.IV, valid.Tag, valid.CT)
	f.Add(1, "aes-256-gcm", "%%%", "AQ==", "", "")
	f.Add(0, "", "", "", "", "")

	f.Fuzz(func(t *testing.T, version int, algorithm, salt, iv, tag, ct string) {
		_, _ = seal.Decrypt(seal.Envelope{
			Version:   version,
			Algorithm: algorithm,
			Salt:      salt,
			IV:        iv,
			Tag:       tag,
			CT:        ct,
		}, passphrase)
	})
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
