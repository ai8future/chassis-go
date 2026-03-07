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
	"regexp"
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
// Only keys that indicate prototype pollution or direct code execution vectors
// are included. Common business-domain words (command, system, import, etc.)
// are intentionally excluded to avoid false positives.
var dangerousKeys = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
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

var secretReplacements = []struct {
	pattern *regexp.Regexp
	repl    string
}{
	{regexp.MustCompile(`(?i)(authorization)\s*:\s*\S.*`), "${1}: [REDACTED]"},
	{regexp.MustCompile(`(?i)(bearer)\s+\S+`), "${1} [REDACTED]"},
	{regexp.MustCompile(`(?i)(password|passwd|secret|token|api_key|api-key|apikey|auth)\s*([=:])\s*[^\s&]*`), "${1}${2}[REDACTED]"},
}

// RedactSecrets replaces secret values in a string with [REDACTED].
func RedactSecrets(s string) string {
	for _, r := range secretReplacements {
		s = r.pattern.ReplaceAllString(s, r.repl)
	}
	return s
}

var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9\-_. ]`)

// SafeFilename removes path separators, null bytes, and control characters.
func SafeFilename(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	cleaned = strings.NewReplacer("/", "", "\\", "").Replace(cleaned)
	cleaned = unsafeFilenameChars.ReplaceAllString(cleaned, "")
	cleaned = strings.Trim(cleaned, ". ")
	cleaned = collapseWhitespace.ReplaceAllString(cleaned, " ")
	if cleaned == "" {
		return "unnamed"
	}
	return cleaned
}

var collapseWhitespace = regexp.MustCompile(`\s+`)
var unsafeURLChars = regexp.MustCompile(`[^a-z0-9\-.]`)

// SafeFilenameURL returns a URL-safe filename.
func SafeFilenameURL(s string) string {
	cleaned := SafeFilename(s)
	cleaned = strings.ToLower(cleaned)
	cleaned = strings.ReplaceAll(cleaned, " ", "-")
	cleaned = unsafeURLChars.ReplaceAllString(cleaned, "")
	cleaned = strings.Trim(cleaned, "-.")
	if cleaned == "" {
		return "unnamed"
	}
	return cleaned
}

var sqlReserved = map[string]bool{
	"SELECT": true, "DROP": true, "DELETE": true, "INSERT": true,
	"UPDATE": true, "ALTER": true, "EXEC": true, "UNION": true,
	"CREATE": true, "GRANT": true, "REVOKE": true, "TRUNCATE": true,
	"MERGE": true, "REPLACE": true, "CALL": true, "EXECUTE": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true,
}

var identifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

var ErrInvalidIdentifier = errors.New("secval: invalid SQL identifier")

// ValidateIdentifier checks that s is a safe SQL identifier.
func ValidateIdentifier(s string) error {
	if !identifierPattern.MatchString(s) {
		return fmt.Errorf("%w: %q does not match identifier pattern", ErrInvalidIdentifier, s)
	}
	if sqlReserved[strings.ToUpper(s)] {
		return fmt.Errorf("%w: %q is a SQL reserved word", ErrInvalidIdentifier, s)
	}
	return nil
}
