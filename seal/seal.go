// Package seal provides cryptographic primitives: AES-256-GCM encryption,
// HMAC-SHA256 signing, and temporary signed tokens.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"golang.org/x/crypto/scrypt"
)

// Envelope is the output of Encrypt — a self-describing encrypted payload.
type Envelope struct {
	Version   int    `json:"v"`
	Algorithm string `json:"alg"`
	Salt      string `json:"salt"` // base64
	IV        string `json:"iv"`   // base64
	Tag       string `json:"tag"`  // base64
	CT        string `json:"ct"`   // base64
}

var (
	ErrDecrypt      = errors.New("seal: decryption failed")
	ErrTokenExpired = errors.New("seal: token expired")
	ErrTokenInvalid = errors.New("seal: invalid token")
	ErrSignature    = errors.New("seal: signature verification failed")
)

const (
	scryptN      = 16384
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	saltLen      = 16
	ivLen        = 12
)

// Encrypt encrypts plaintext using AES-256-GCM with a scrypt-derived key.
func Encrypt(plaintext []byte, passphrase string) (Envelope, error) {
	chassis.AssertVersionChecked()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return Envelope{}, fmt.Errorf("seal: generate salt: %w", err)
	}

	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return Envelope{}, fmt.Errorf("seal: derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, fmt.Errorf("seal: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, fmt.Errorf("seal: create GCM: %w", err)
	}

	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return Envelope{}, fmt.Errorf("seal: generate IV: %w", err)
	}

	sealed := gcm.Seal(nil, iv, plaintext, nil)
	tagStart := len(sealed) - gcm.Overhead()
	ct := sealed[:tagStart]
	tag := sealed[tagStart:]

	return Envelope{
		Version:   1,
		Algorithm: "aes-256-gcm",
		Salt:      base64.StdEncoding.EncodeToString(salt),
		IV:        base64.StdEncoding.EncodeToString(iv),
		Tag:       base64.StdEncoding.EncodeToString(tag),
		CT:        base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// Decrypt decrypts an Envelope using the given passphrase.
func Decrypt(env Envelope, passphrase string) ([]byte, error) {
	if env.Version != 1 {
		return nil, fmt.Errorf("%w: unsupported envelope version %d", ErrDecrypt, env.Version)
	}
	if env.Algorithm != "aes-256-gcm" {
		return nil, fmt.Errorf("%w: unsupported algorithm %q", ErrDecrypt, env.Algorithm)
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid salt", ErrDecrypt)
	}
	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid IV", ErrDecrypt)
	}
	tag, err := base64.StdEncoding.DecodeString(env.Tag)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid tag", ErrDecrypt)
	}
	ct, err := base64.StdEncoding.DecodeString(env.CT)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ciphertext", ErrDecrypt)
	}

	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("%w: key derivation failed", ErrDecrypt)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: cipher creation failed", ErrDecrypt)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: GCM creation failed", ErrDecrypt)
	}

	sealed := append(ct, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}

	return plaintext, nil
}

// Sign computes an HMAC-SHA256 signature of payload using secret.
func Sign(payload []byte, secret string) string {
	chassis.AssertVersionChecked()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks that signature is a valid HMAC-SHA256 of payload using secret.
func Verify(payload []byte, signature string, secret string) bool {
	expected := Sign(payload, secret)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// Claims is the decoded payload of a temporary token.
type Claims = map[string]any

// NewToken creates a signed, expiring token.
func NewToken(claims Claims, secret string, ttlSeconds int) (string, error) {
	chassis.AssertVersionChecked()
	payload := make(Claims, len(claims)+2)
	for k, v := range claims {
		payload[k] = v
	}
	payload["exp"] = time.Now().Unix() + int64(ttlSeconds)

	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", fmt.Errorf("seal: generate jti: %w", err)
	}
	payload["jti"] = hex.EncodeToString(jtiBytes)

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("seal: marshal claims: %w", err)
	}

	sig := Sign(body, secret)
	token := base64.RawURLEncoding.EncodeToString(body) + "." + sig
	return token, nil
}

// ValidateToken verifies a token's signature and expiry, returning the claims.
func ValidateToken(token string, secret string) (Claims, error) {
	parts := splitToken(token)
	if len(parts) != 2 {
		return nil, ErrTokenInvalid
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: decode failed", ErrTokenInvalid)
	}

	if !Verify(body, parts[1], secret) {
		return nil, ErrSignature
	}

	var claims Claims
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("%w: unmarshal failed", ErrTokenInvalid)
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, fmt.Errorf("%w: missing exp claim", ErrTokenInvalid)
	}
	if time.Now().Unix() > int64(exp) {
		return nil, ErrTokenExpired
	}

	return claims, nil
}

func splitToken(token string) []string {
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			return []string{token[:i], token[i+1:]}
		}
	}
	return nil
}
