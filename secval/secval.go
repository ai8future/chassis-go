// Package secval provides JSON security validation: dangerous key detection
// and nesting depth limits. It has NO cross-module dependencies — errors
// are module-local sentinel types.
//
// Do not use secval on file uploads or streaming endpoints. It parses the
// entire input into memory. Enforce body size limits (e.g., MaxBytesReader
// at 1-2MB) BEFORE passing data to secval.
package secval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Sentinel errors — module-local, NOT from the chassis errors package.
var (
	ErrDangerousKey = errors.New("secval: dangerous key detected")
	ErrNestingDepth = errors.New("secval: nesting depth exceeded")
	ErrInvalidJSON  = errors.New("secval: invalid JSON")
)

// dangerousKeys is the set of normalised keys blocked in user input.
var dangerousKeys = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
	"execute":     true,
	"eval":        true,
	"include":     true,
	"import":      true,
	"require":     true,
	"system":      true,
	"shell":       true,
	"command":     true,
	"script":      true,
	"exec":        true,
	"spawn":       true,
	"fork":        true,
}

// MaxNestingDepth is the maximum allowed depth for nested structures.
const MaxNestingDepth = 20

// ValidateJSON parses data as JSON and scans it for dangerous keys and
// excessive nesting. Returns nil on success, or an error wrapping one of
// the sentinel errors (ErrDangerousKey, ErrNestingDepth, ErrInvalidJSON).
func ValidateJSON(data []byte) error {
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return validateValue(parsed, 0)
}

func validateValue(v any, depth int) error {
	switch val := v.(type) {
	case map[string]any:
		if depth >= MaxNestingDepth {
			return fmt.Errorf("%w: depth %d exceeds maximum %d", ErrNestingDepth, depth, MaxNestingDepth)
		}
		for key, value := range val {
			// Strip non-ASCII and non-printable characters, then normalise.
			cleaned := strings.Map(func(r rune) rune {
				if r > unicode.MaxASCII || !unicode.IsPrint(r) {
					return -1
				}
				return r
			}, key)
			normalised := strings.ToLower(strings.ReplaceAll(cleaned, "-", "_"))
			if dangerousKeys[normalised] {
				return fmt.Errorf("%w: %q", ErrDangerousKey, key)
			}
			if err := validateValue(value, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if depth >= MaxNestingDepth {
			return fmt.Errorf("%w: depth %d exceeds maximum %d", ErrNestingDepth, depth, MaxNestingDepth)
		}
		for _, item := range val {
			if err := validateValue(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
