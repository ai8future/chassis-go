// Package authkit provides scoped inbound bearer-token validation for services
// that need Windmill-callable HTTP or gRPC endpoints.
package authkit

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	chassis "github.com/ai8future/chassis-go/v11"
	chassiserrors "github.com/ai8future/chassis-go/v11/errors"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	DefaultEnvVar = "CHASSIS_AUTHKIT_KEYS"
	scryptParts   = 6
)

type contextKey struct{}

// Key describes one static bearer credential and the scopes it grants.
type Key struct {
	ID         string
	StoredHash string
	Scopes     []string
}

// Principal is attached to request contexts after successful authentication.
type Principal struct {
	KeyID  string
	Scopes []string
}

// StaticVerifier verifies token strings in the form <key-id>.<secret> against
// stored scrypt hashes.
type StaticVerifier struct {
	keys map[string]Key
}

// NewStaticVerifier builds a verifier from static key records.
func NewStaticVerifier(keys []Key) (*StaticVerifier, error) {
	chassis.AssertVersionChecked()
	v := &StaticVerifier{keys: make(map[string]Key, len(keys))}
	for _, key := range keys {
		if key.ID == "" {
			return nil, fmt.Errorf("authkit: key id is required")
		}
		if key.StoredHash == "" {
			return nil, fmt.Errorf("authkit: stored hash is required for key %q", key.ID)
		}
		if _, err := parseStoredHash(key.StoredHash); err != nil {
			return nil, fmt.Errorf("authkit: key %q: %w", key.ID, err)
		}
		key.Scopes = dedupe(key.Scopes)
		v.keys[key.ID] = key
	}
	return v, nil
}

// MustStaticVerifier is like NewStaticVerifier but panics on invalid keys.
func MustStaticVerifier(keys []Key) *StaticVerifier {
	chassis.AssertVersionChecked()
	v, err := NewStaticVerifier(keys)
	if err != nil {
		panic(err)
	}
	return v
}

// LoadStaticKeysFromEnv parses CHASSIS_AUTHKIT_KEYS by default. The format is:
//
//	key-id=scrypt$N$r$p$salt_b64$derived_key_b64:scope1,scope2;other=...
func LoadStaticKeysFromEnv(envVar ...string) ([]Key, error) {
	chassis.AssertVersionChecked()
	name := DefaultEnvVar
	if len(envVar) > 0 && envVar[0] != "" {
		name = envVar[0]
	}
	return ParseStaticKeys(os.Getenv(name))
}

// ParseStaticKeys parses the static key environment format.
func ParseStaticKeys(value string) ([]Key, error) {
	chassis.AssertVersionChecked()
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	entries := strings.Split(value, ";")
	keys := make([]Key, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, rest, ok := strings.Cut(entry, "=")
		if !ok || id == "" || rest == "" {
			return nil, fmt.Errorf("authkit: invalid key entry %q", entry)
		}
		hash, scopeText, _ := strings.Cut(rest, ":")
		var scopes []string
		for _, scope := range strings.Split(scopeText, ",") {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
		keys = append(keys, Key{ID: strings.TrimSpace(id), StoredHash: hash, Scopes: scopes})
	}
	return keys, nil
}

// VerifyBearer validates an Authorization header value and returns a principal.
func (v *StaticVerifier) VerifyBearer(ctx context.Context, authorization string, requiredScopes ...string) (*Principal, error) {
	chassis.AssertVersionChecked()
	_ = ctx
	if v == nil {
		return nil, unauthorized("auth verifier is not configured")
	}
	prefix, token, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if !ok || !strings.EqualFold(prefix, "Bearer") || token == "" {
		return nil, unauthorized("missing bearer token")
	}
	keyID, secret, ok := strings.Cut(token, ".")
	if !ok || keyID == "" || secret == "" {
		return nil, unauthorized("malformed bearer token")
	}
	key, ok := v.keys[keyID]
	if !ok {
		return nil, unauthorized("unknown bearer token")
	}
	if ok, err := verifyStoredHash(key.StoredHash, secret); err != nil {
		return nil, unauthorized("invalid bearer token")
	} else if !ok {
		return nil, unauthorized("invalid bearer token")
	}
	principal := &Principal{KeyID: key.ID, Scopes: append([]string(nil), key.Scopes...)}
	if missing := missingScopes(principal.Scopes, requiredScopes); len(missing) > 0 {
		return nil, chassiserrors.ForbiddenError("missing required scope").
			WithClass("auth_scope_missing").
			WithDetail("missing_scopes", missing)
	}
	return principal, nil
}

// ContextWithPrincipal returns a child context containing p.
func ContextWithPrincipal(ctx context.Context, p *Principal) context.Context {
	chassis.AssertVersionChecked()
	return context.WithValue(ctx, contextKey{}, p)
}

// PrincipalFromContext returns the authenticated principal, if any.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	chassis.AssertVersionChecked()
	p, ok := ctx.Value(contextKey{}).(*Principal)
	return p, ok
}

// HTTPMiddleware authenticates HTTP requests before invoking next.
func HTTPMiddleware(verifier *StaticVerifier, requiredScopes ...string) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := verifier.VerifyBearer(r.Context(), r.Header.Get("Authorization"), requiredScopes...)
			if err != nil {
				chassiserrors.WriteProblem(w, r, err, "")
				return
			}
			next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), principal)))
		})
	}
}

// UnaryServerInterceptor authenticates gRPC unary calls.
func UnaryServerInterceptor(verifier *StaticVerifier, requiredScopes ...string) grpc.UnaryServerInterceptor {
	chassis.AssertVersionChecked()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		principal, err := verifyMetadata(ctx, verifier, requiredScopes)
		if err != nil {
			return nil, err
		}
		return handler(ContextWithPrincipal(ctx, principal), req)
	}
}

// StreamServerInterceptor authenticates gRPC streaming calls.
func StreamServerInterceptor(verifier *StaticVerifier, requiredScopes ...string) grpc.StreamServerInterceptor {
	chassis.AssertVersionChecked()
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		principal, err := verifyMetadata(ss.Context(), verifier, requiredScopes)
		if err != nil {
			return err
		}
		return handler(srv, &principalServerStream{ServerStream: ss, ctx: ContextWithPrincipal(ss.Context(), principal)})
	}
}

type principalServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *principalServerStream) Context() context.Context { return s.ctx }

func verifyMetadata(ctx context.Context, verifier *StaticVerifier, requiredScopes []string) (*Principal, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) == 0 {
		return nil, unauthorized("missing bearer token")
	}
	return verifier.VerifyBearer(ctx, values[0], requiredScopes...)
}

func unauthorized(message string) error {
	return chassiserrors.UnauthorizedError(message).WithClass("auth_unauthorized")
}

type parsedHash struct {
	N, R, P int
	Salt    []byte
	Key     []byte
}

func parseStoredHash(stored string) (parsedHash, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != scryptParts || parts[0] != "scrypt" {
		return parsedHash{}, fmt.Errorf("invalid stored hash format")
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return parsedHash{}, fmt.Errorf("invalid N: %w", err)
	}
	r, err := strconv.Atoi(parts[2])
	if err != nil {
		return parsedHash{}, fmt.Errorf("invalid r: %w", err)
	}
	p, err := strconv.Atoi(parts[3])
	if err != nil {
		return parsedHash{}, fmt.Errorf("invalid p: %w", err)
	}
	if n <= 1 || n&(n-1) != 0 {
		return parsedHash{}, fmt.Errorf("invalid N: must be a power of two greater than one")
	}
	if r <= 0 {
		return parsedHash{}, fmt.Errorf("invalid r: must be positive")
	}
	if p <= 0 {
		return parsedHash{}, fmt.Errorf("invalid p: must be positive")
	}
	salt, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return parsedHash{}, fmt.Errorf("invalid salt: %w", err)
	}
	derived, err := base64.StdEncoding.DecodeString(parts[5])
	if err != nil {
		return parsedHash{}, fmt.Errorf("invalid derived key: %w", err)
	}
	if len(salt) == 0 {
		return parsedHash{}, fmt.Errorf("invalid salt: empty")
	}
	if len(derived) == 0 {
		return parsedHash{}, fmt.Errorf("invalid derived key: empty")
	}
	return parsedHash{N: n, R: r, P: p, Salt: salt, Key: derived}, nil
}

func verifyStoredHash(stored, secret string) (bool, error) {
	parsed, err := parseStoredHash(stored)
	if err != nil {
		return false, err
	}
	secret = norm.NFC.String(secret)
	derived, err := scrypt.Key([]byte(secret), parsed.Salt, parsed.N, parsed.R, parsed.P, len(parsed.Key))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(derived, parsed.Key) == 1, nil
}

func missingScopes(granted, required []string) []string {
	set := make(map[string]bool, len(granted))
	for _, scope := range granted {
		set[scope] = true
	}
	var missing []string
	for _, scope := range required {
		if scope != "" && !set[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
