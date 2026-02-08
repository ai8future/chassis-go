package flagz

import (
	"encoding/json"
	"os"
	"strings"
)

// envSource reads flag values from environment variables captured at
// construction time. Variable names are converted from PREFIX_FLAG_NAME=value
// to flag name "flag-name" (lowercased, underscores become hyphens).
type envSource struct {
	flags map[string]string
}

// FromEnv creates a Source that reads environment variables with the given
// prefix. For example, with prefix "FLAG", the env var FLAG_NEW_THING=true
// maps to flag name "new-thing" with value "true".
func FromEnv(prefix string) Source {
	flags := make(map[string]string)
	pfx := strings.ToUpper(prefix) + "_"
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		if !strings.HasPrefix(key, pfx) {
			continue
		}
		// Strip prefix and convert to flag name.
		name := strings.ToLower(strings.TrimPrefix(key, pfx))
		name = strings.ReplaceAll(name, "_", "-")
		flags[name] = val
	}
	return &envSource{flags: flags}
}

func (s *envSource) Lookup(name string) (string, bool) {
	v, ok := s.flags[name]
	return v, ok
}

// mapSource is an in-memory source backed by a static map.
type mapSource struct {
	m map[string]string
}

// FromMap creates a Source from an in-memory map. Useful for testing.
func FromMap(m map[string]string) Source {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &mapSource{m: cp}
}

func (s *mapSource) Lookup(name string) (string, bool) {
	v, ok := s.m[name]
	return v, ok
}

// jsonSource reads flag values from a JSON file at construction time.
type jsonSource struct {
	flags map[string]string
}

// FromJSON creates a Source that reads flag key-value pairs from a JSON file.
// The file must contain a flat JSON object: {"flag-name": "value", ...}.
// Panics if the file cannot be read or parsed.
func FromJSON(path string) Source {
	data, err := os.ReadFile(path)
	if err != nil {
		panic("flagz: failed to read JSON file: " + err.Error())
	}
	var flags map[string]string
	if err := json.Unmarshal(data, &flags); err != nil {
		panic("flagz: failed to parse JSON file: " + err.Error())
	}
	return &jsonSource{flags: flags}
}

func (s *jsonSource) Lookup(name string) (string, bool) {
	v, ok := s.flags[name]
	return v, ok
}

// multiSource layers multiple sources. Later sources override earlier ones.
type multiSource struct {
	sources []Source
}

// Multi creates a Source that layers multiple sources. Later sources in the
// list take precedence over earlier ones.
func Multi(sources ...Source) Source {
	return &multiSource{sources: sources}
}

func (s *multiSource) Lookup(name string) (string, bool) {
	// Iterate in reverse so later sources win.
	for i := len(s.sources) - 1; i >= 0; i-- {
		if v, ok := s.sources[i].Lookup(name); ok {
			return v, true
		}
	}
	return "", false
}
