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

	chassis "github.com/ai8future/chassis-go/v11"
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

	loadFields(v, t)

	return cfg
}

// loadFields populates struct fields from environment variables, recursing
// into nested structs so that embedded config types (e.g. kafkakit.Config) are
// populated correctly.
func loadFields(v reflect.Value, t reflect.Type) {
	for i := range t.NumField() {
		field := t.Field(i)
		fieldVal := v.Field(i)

		// Skip unexported fields — they can't be set via reflection.
		if !field.IsExported() {
			continue
		}

		// Recurse into nested structs (e.g. kafkakit.Config, meilikit.Config).
		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeOf(time.Duration(0)) {
			loadFields(fieldVal, field.Type)
			continue
		}

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
			panic(fmt.Sprintf("config: cannot set field %s from env %q: %v", field.Name, envKey, err))
		}

		if vTag := field.Tag.Get("validate"); vTag != "" {
			validateField(field.Name, fieldVal, vTag)
		}
	}
}

// Convert parses raw into proto's dynamic type and returns the converted value.
// It supports the same field types as MustLoad and never panics.
func Convert(proto any, raw string) (any, error) {
	protoType := reflect.TypeOf(proto)
	if protoType == nil {
		return nil, fmt.Errorf("unsupported field type <nil>")
	}

	// Handle time.Duration specially before the kind switch.
	if protoType == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid duration: %w", err)
		}
		return d, nil
	}

	// Handle []string specially.
	if protoType == reflect.TypeOf([]string{}) {
		parts := strings.Split(raw, ",")
		trimmed := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed = append(trimmed, strings.TrimSpace(p))
		}
		return trimmed, nil
	}

	switch protoType.Kind() {
	case reflect.String:
		v := reflect.New(protoType).Elem()
		v.SetString(raw)
		return v.Interface(), nil

	case reflect.Int, reflect.Int64:
		bitSize := 64
		if protoType.Kind() == reflect.Int {
			bitSize = strconv.IntSize
		}
		n, err := strconv.ParseInt(raw, 10, bitSize)
		if err != nil {
			return nil, fmt.Errorf("invalid int: %w", err)
		}
		v := reflect.New(protoType).Elem()
		v.SetInt(n)
		return v.Interface(), nil

	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float64: %w", err)
		}
		v := reflect.New(protoType).Elem()
		v.SetFloat(f)
		return v.Interface(), nil

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid bool: %w", err)
		}
		v := reflect.New(protoType).Elem()
		v.SetBool(b)
		return v.Interface(), nil

	default:
		return nil, fmt.Errorf("unsupported field type %s", protoType)
	}
}

// setField converts a raw string value and sets it on the reflected field.
func setField(fieldVal reflect.Value, raw string) error {
	converted, err := Convert(fieldVal.Interface(), raw)
	if err != nil {
		return err
	}
	convertedVal := reflect.ValueOf(converted)
	if !convertedVal.Type().AssignableTo(fieldVal.Type()) {
		return fmt.Errorf("unsupported field type %s", fieldVal.Type())
	}
	fieldVal.Set(convertedVal)
	return nil
}

// Check validates v against a validate tag and returns an error instead of
// panicking. Supported keys: min, max, oneof, pattern. Multiple constraints are
// comma-separated (e.g. validate:"min=1,max=65535").
func Check(name string, v any, validateTag string) error {
	val := reflect.ValueOf(v)
	parts := splitConstraints(validateTag)
	for _, part := range parts {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "min":
			minVal, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("config: field %s has invalid min value %q in validate tag", name, value)
			}
			actual := fieldAsFloat(val)
			if actual < minVal {
				return fmt.Errorf("config: field %s value %v is below minimum %s", name, val.Interface(), value)
			}
		case "max":
			maxVal, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("config: field %s has invalid max value %q in validate tag", name, value)
			}
			actual := fieldAsFloat(val)
			if actual > maxVal {
				return fmt.Errorf("config: field %s value %v exceeds maximum %s", name, val.Interface(), value)
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
				return fmt.Errorf("config: field %s value %q not in allowed set [%s]", name, actual, value)
			}
		case "pattern":
			re, err := regexp.Compile(value)
			if err != nil {
				return fmt.Errorf("config: field %s has invalid pattern %q in validate tag: %v", name, value, err)
			}
			actual := fmt.Sprintf("%v", val.Interface())
			if !re.MatchString(actual) {
				return fmt.Errorf("config: field %s value %q does not match pattern %s", name, actual, value)
			}
		}
	}
	return nil
}

func splitConstraints(tag string) []string {
	parts := make([]string, 0, 4)
	start := 0
	depth := 0
	escaped := false

	for i, r := range tag {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}

		switch r {
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, tag[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, tag[start:])
	return parts
}

// validateField checks a populated field against constraints in the validate
// struct tag. Supported keys: min, max, oneof, pattern. Multiple constraints
// are comma-separated (e.g. validate:"min=1,max=65535").
func validateField(name string, val reflect.Value, tag string) {
	if err := Check(name, val.Interface(), tag); err != nil {
		panic(err.Error())
	}
}

// fieldAsFloat converts numeric reflect values to float64 for comparison.
func fieldAsFloat(val reflect.Value) float64 {
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(val.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(val.Uint())
	case reflect.Float32, reflect.Float64:
		return val.Float()
	default:
		return 0
	}
}
