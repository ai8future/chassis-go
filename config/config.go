// Package config provides a generic, reflection-based configuration loader
// that populates structs from environment variables using struct tags.
package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v8"
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
// Supported field types: string, int, int64, float64, bool, time.Duration, []string.
func MustLoad[T any]() T {
	chassis.AssertVersionChecked()
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

		// Skip unexported fields — they can't be set via reflection.
		if !field.IsExported() {
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
			panic(fmt.Sprintf("config: cannot set field %s from env %q: %v", field.Name, envKey, err))
		}

		if vTag := field.Tag.Get("validate"); vTag != "" {
			validateField(field.Name, fieldVal, vTag)
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
		bitSize := 64
		if fieldVal.Kind() == reflect.Int {
			bitSize = strconv.IntSize
		}
		n, err := strconv.ParseInt(raw, 10, bitSize)
		if err != nil {
			return fmt.Errorf("invalid int: %w", err)
		}
		fieldVal.SetInt(n)

	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid float64: %w", err)
		}
		fieldVal.SetFloat(f)

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

// validateField checks a populated field against constraints in the validate
// struct tag. Supported keys: min, max, oneof, pattern. Multiple constraints
// are comma-separated (e.g. validate:"min=1,max=65535").
func validateField(name string, val reflect.Value, tag string) {
	parts := strings.Split(tag, ",")
	for _, part := range parts {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "min":
			minVal, _ := strconv.ParseFloat(value, 64)
			actual := fieldAsFloat(val)
			if actual < minVal {
				panic(fmt.Sprintf("config: field %s value %v is below minimum %s", name, val.Interface(), value))
			}
		case "max":
			maxVal, _ := strconv.ParseFloat(value, 64)
			actual := fieldAsFloat(val)
			if actual > maxVal {
				panic(fmt.Sprintf("config: field %s value %v exceeds maximum %s", name, val.Interface(), value))
			}
		case "oneof":
			allowed := strings.Fields(value)
			actual := fmt.Sprintf("%v", val.Interface())
			found := false
			for _, a := range allowed {
				if a == actual {
					found = true
					break
				}
			}
			if !found {
				panic(fmt.Sprintf("config: field %s value %q not in allowed set [%s]", name, actual, value))
			}
		case "pattern":
			re := regexp.MustCompile(value)
			actual := fmt.Sprintf("%v", val.Interface())
			if !re.MatchString(actual) {
				panic(fmt.Sprintf("config: field %s value %q does not match pattern %s", name, actual, value))
			}
		}
	}
}

// fieldAsFloat converts numeric reflect values to float64 for comparison.
func fieldAsFloat(val reflect.Value) float64 {
	switch val.Kind() {
	case reflect.Int, reflect.Int64:
		return float64(val.Int())
	case reflect.Float64:
		return val.Float()
	default:
		return 0
	}
}
