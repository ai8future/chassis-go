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
	err := ValidateJSON([]byte(`[{"ok": 1}, {"eval": "evil"}]`))
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
	err := ValidateJSON([]byte(`{"exec": true}`))
	// Verify the error wraps ErrDangerousKey, not any external type.
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey sentinel, got %v", err)
	}
}

func TestAllDangerousKeysBlocked(t *testing.T) {
	keys := []string{
		"__proto__", "constructor", "prototype", "execute", "eval",
		"include", "import", "require", "system", "shell",
		"command", "script", "exec", "spawn", "fork",
	}
	for _, key := range keys {
		err := ValidateJSON([]byte(`{"` + key + `": true}`))
		if !errors.Is(err, ErrDangerousKey) {
			t.Errorf("expected %q to be blocked, got %v", key, err)
		}
	}
}

func TestNestedDangerousKey(t *testing.T) {
	err := ValidateJSON([]byte(`{"data": {"inner": {"exec": true}}}`))
	if !errors.Is(err, ErrDangerousKey) {
		t.Fatalf("expected ErrDangerousKey nested, got %v", err)
	}
}
