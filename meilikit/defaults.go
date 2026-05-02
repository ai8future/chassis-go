package meilikit

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const defaultLimit int64 = 20

const (
	defaultBatchSize = 1000
	maxBatchSize     = 10000
)

// resolveSearchDefaults fills in zero-valued defaults for SearchOptions.
func resolveSearchDefaults(opts SearchOptions) SearchOptions {
	if opts.Limit == 0 {
		opts.Limit = defaultLimit
	}
	return opts
}

// resolveBulkDefaults fills in zero-valued defaults for BulkOptions.
func resolveBulkDefaults(opts BulkOptions) BulkOptions {
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.BatchSize > maxBatchSize {
		opts.BatchSize = maxBatchSize
	}
	return opts
}

// validateIndexName checks that an index name matches Meilisearch's UID rules:
// non-empty, alphanumeric plus hyphen and underscore only.
func validateIndexName(name string) error {
	if name == "" {
		return fmt.Errorf("meilikit: index name must not be empty")
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("meilikit: index name %q contains invalid character %q (allowed: a-z A-Z 0-9 - _)", name, c)
		}
	}
	return nil
}

// validateDocID checks that a document ID is non-empty and contains only safe characters.
func validateDocID(id string) error {
	if id == "" {
		return fmt.Errorf("meilikit: document ID must not be empty")
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("meilikit: document ID %q contains invalid character", id)
		}
	}
	return nil
}

// SanitizeUTF8 replaces invalid UTF-8 bytes with the Unicode replacement character.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}
