// Package config provides a generic, reflection-based configuration loader
// that populates structs from environment variables using struct tags.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// MustLoad loads environment variables into a struct of type T based on struct
// tags. It panics if any required variable is missing and has no default.
//
// Supported struct tags:
//
//	env:"VAR_NAME"       — the environment variable to read
//	default:"value"      — fallback value when the env var is empty
//	required:"true"      — panic if missing and no default (this is the default behavior)
//	required:"false"     — leave the zero value if missing and no default
//
// Supported field types: string, int, int64, bool, time.Duration, []string.
func MustLoad[T any]() T {
	var cfg T
	v := reflect.ValueOf(&cfg).Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		fieldVal := v.Field(i)

		envKey := field.Tag.Get("env")
		if envKey == "" {
			continue
		}

		raw := os.Getenv(envKey)

		// Apply default if env var is empty.
		if raw == "" {
			if def, ok := field.Tag.Lookup("default"); ok {
				raw = def
			}
		}

		// Handle missing value.
		if raw == "" {
			req := field.Tag.Get("required")
			if req == "false" {
				continue
			}
			// Default behaviour: required.
			panic(fmt.Sprintf("config: required environment variable %q is not set (field %s)", envKey, field.Name))
		}

		if err := setField(fieldVal, raw); err != nil {
			panic(fmt.Sprintf("config: cannot set field %s from env %q=%q: %v", field.Name, envKey, raw, err))
		}
	}

	return cfg
}

// setField converts a raw string value and sets it on the reflected field.
func setField(fieldVal reflect.Value, raw string) error {
	// Handle time.Duration specially before the kind switch.
	if fieldVal.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}
		fieldVal.Set(reflect.ValueOf(d))
		return nil
	}

	// Handle []string specially.
	if fieldVal.Type() == reflect.TypeOf([]string{}) {
		parts := strings.Split(raw, ",")
		trimmed := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed = append(trimmed, strings.TrimSpace(p))
		}
		fieldVal.Set(reflect.ValueOf(trimmed))
		return nil
	}

	switch fieldVal.Kind() {
	case reflect.String:
		fieldVal.SetString(raw)

	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid int: %w", err)
		}
		fieldVal.SetInt(n)

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid bool: %w", err)
		}
		fieldVal.SetBool(b)

	default:
		return fmt.Errorf("unsupported field type %s", fieldVal.Type())
	}

	return nil
}
