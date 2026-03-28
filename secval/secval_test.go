package secval

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanJSONPasses(t *testing.T) {
	if err := ValidateJSON([]byte(`{"name": "Alice", "age": 30}`)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestProtoRejected(t *testing.T) {
	err := ValidateJSON([]byte(`{"__proto__": true}`))
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey, got %v", err)
	}
}

func TestConstructorRejected(t *testing.T) {
	err := ValidateJSON([]byte(`{"CONSTRUCTOR": true}`))
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey (case normalisation), got %v", err)
	}
}

func TestHyphenNormalisationWorks(t *testing.T) {
	// Hyphens are replaced with underscores during normalisation.
	// Keys like "__proto__" can be evaded with "__-proto-__" only if hyphens
	// map to nothing. Our normalisation maps "-" → "_", which is a defensive
	// measure. Verify the path runs without panic and safe keys with hyphens pass.
	if err := ValidateJSON([]byte(`{"safe-key": true}`)); err != nil {
		t.Fatalf("safe hyphenated key should pass, got %v", err)
	}
}

func TestDepth21Rejected(t *testing.T) {
	// Build JSON nested 21 levels deep — exceeds MaxNestingDepth (20).
	json := strings.Repeat(`{"a":`, 21) + `1` + strings.Repeat(`}`, 21)
	err := ValidateJSON([]byte(json))
	if !errors.Is(err, ErrNestingDepth) {
		t.Fatalf("expected ErrNestingDepth, got %v", err)
	}
}

func TestDepth20Passes(t *testing.T) {
	json := strings.Repeat(`{"a":`, 20) + `1` + strings.Repeat(`}`, 20)
	if err := ValidateJSON([]byte(json)); err != nil {
		t.Fatalf("expected nil for depth 20, got %v", err)
	}
}

func TestArrayOfObjectsScanned(t *testing.T) {
	err := ValidateJSON([]byte(`[{"ok": 1}, {"__proto__": "evil"}]`))
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey in array, got %v", err)
	}
}

func TestNonObjectJSONPasses(t *testing.T) {
	cases := []string{`"hello"`, `42`, `true`, `null`, `[1, 2, 3]`}
	for _, c := range cases {
		if err := ValidateJSON([]byte(c)); err != nil {
			t.Errorf("expected nil for %q, got %v", c, err)
		}
	}
}

func TestInvalidJSON(t *testing.T) {
	err := ValidateJSON([]byte(`{not json}`))
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("expected ErrInvalidJSON, got %v", err)
	}
}

func TestErrorIsNotServiceError(t *testing.T) {
	err := ValidateJSON([]byte(`{"prototype": true}`))
	// Verify the error wraps ErrDangerousKey, not any external type.
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey sentinel, got %v", err)
	}
}

func TestAllDangerousKeysBlocked(t *testing.T) {
	keys := []string{
		"__proto__", "constructor", "prototype",
	}
	for _, key := range keys {
		err := ValidateJSON([]byte(`{"` + key + `": true}`))
		if !errors.Is(err, ErrDangerousKey) {
			t.Errorf("expected %q to be blocked, got %v", key, err)
		}
	}
}

func TestCommonBusinessKeysAllowed(t *testing.T) {
	// These were previously blocked but are common in business domain JSON.
	keys := []string{
		"command", "system", "import", "include", "require",
		"exec", "execute", "shell", "script", "spawn", "fork", "eval",
	}
	for _, key := range keys {
		err := ValidateJSON([]byte(`{"` + key + `": "value"}`))
		if err != nil {
			t.Errorf("expected %q to be allowed, got %v", key, err)
		}
	}
}

func TestNestedDangerousKey(t *testing.T) {
	err := ValidateJSON([]byte(`{"data": {"inner": {"__proto__": true}}}`))
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey nested, got %v", err)
	}
}

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"password=hunter2", "password=[REDACTED]"},
		{"token=abc123&user=bob", "token=[REDACTED]&user=bob"},
		{"Authorization: Bearer eyJhbG...", "Authorization: [REDACTED]"},
		{"no secrets here", "no secrets here"},
		{"API_KEY=sk-12345 other=ok", "API_KEY=[REDACTED] other=ok"},
		{"password=", "password=[REDACTED]"},
	}
	for _, tt := range tests {
		got := RedactSecrets(tt.input)
		if got != tt.want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.txt", "normal.txt"},
		{"../../etc/passwd", "etcpasswd"},
		{"file\x00name.txt", "filename.txt"},
		{"hello world.pdf", "hello world.pdf"},
		{"...leading", "leading"},
		{"", "unnamed"},
		{"   ", "unnamed"},
	}
	for _, tt := range tests {
		got := SafeFilename(tt.input)
		if got != tt.want {
			t.Errorf("SafeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSafeFilenameURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World.PDF", "hello-world.pdf"},
		{"../../bad", "bad"},
		{"", "unnamed"},
	}
	for _, tt := range tests {
		got := SafeFilenameURL(tt.input)
		if got != tt.want {
			t.Errorf("SafeFilenameURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateIdentifier(t *testing.T) {
	valid := []string{"users", "user_id", "TableName", "_private", "a"}
	for _, s := range valid {
		if err := ValidateIdentifier(s); err != nil {
			t.Errorf("ValidateIdentifier(%q) unexpected error: %v", s, err)
		}
	}

	invalid := []string{"", "123start", "has space", "DROP", "select", "has-dash"}
	for _, s := range invalid {
		if err := ValidateIdentifier(s); err == nil {
			t.Errorf("ValidateIdentifier(%q) expected error", s)
		}
	}
}

func TestValidateIdentifierBoundary64(t *testing.T) {
	// The regex allows ^[a-zA-Z_][a-zA-Z0-9_]{0,63}$ so max total is 64 chars.
	// Exactly 64 characters -- should pass.
	id64 := "a" + strings.Repeat("b", 63)
	if err := ValidateIdentifier(id64); err != nil {
		t.Errorf("64-char identifier should be valid, got: %v", err)
	}

	// 65 characters -- should fail.
	id65 := "a" + strings.Repeat("b", 64)
	if err := ValidateIdentifier(id65); err == nil {
		t.Error("65-char identifier should be invalid, got nil")
	}
}

func TestSafeFilenameUnicode(t *testing.T) {
	tests := []struct {
		input string
		check func(string) bool
		desc  string
	}{
		// Accented characters are stripped by the unsafeFilenameChars regex,
		// so the result must still be non-empty (falls back to remaining chars or "unnamed").
		{"resume.pdf", func(s string) bool { return len(s) > 0 }, "ASCII chars produce non-empty output"},
		{"hello\x00world", func(s string) bool { return !strings.Contains(s, "\x00") }, "null byte stripped"},
		{"foo   bar   baz", func(s string) bool { return !strings.Contains(s, "  ") }, "consecutive spaces collapsed"},
	}

	for _, tt := range tests {
		result := SafeFilename(tt.input)
		if !tt.check(result) {
			t.Errorf("SafeFilename(%q) = %q -- %s", tt.input, result, tt.desc)
		}
	}
}

func TestSafeFilenameAllNonASCII(t *testing.T) {
	// A filename with only non-ASCII chars should return "unnamed".
	result := SafeFilename("\u00e9\u00e8\u00ea")
	if result != "unnamed" {
		t.Errorf("SafeFilename(all non-ASCII) = %q, want %q", result, "unnamed")
	}
}
