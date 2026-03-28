package chassis

import (
	"strconv"
	"strings"
)

// semverNewer returns true if version a is strictly newer than version b.
// Both must be dot-separated numeric strings (e.g., "10.0.11").
// Returns false on parse errors or equal versions.
func semverNewer(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := range maxLen {
		var na, nb int
		var errA, errB error
		if i < len(partsA) {
			na, errA = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			nb, errB = strconv.Atoi(partsB[i])
		}
		if errA != nil || errB != nil {
			return false
		}
		if na > nb {
			return true
		}
		if na < nb {
			return false
		}
	}
	return false
}
