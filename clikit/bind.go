package clikit

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/ai8future/chassis-go/v11/config"
)

// Binding records the relationship between a FlagSet and an options struct.
type Binding struct {
	target reflect.Value
	fields []fieldBinding
}

type fieldBinding struct {
	index        int
	name         string
	flagName     string
	envName      string
	defaultValue string
	hasDefault   bool
	required     string
	validate     string
	value        *rawFlagValue
	isSlice      bool
}

type rawFlagValue struct {
	values []string
	isBool bool
	slice  bool
}

func (v *rawFlagValue) Set(s string) error {
	if v.slice {
		v.values = append(v.values, s)
		return nil
	}
	v.values = []string{s}
	return nil
}

func (v *rawFlagValue) String() string {
	if len(v.values) == 0 {
		return ""
	}
	if v.slice {
		return strings.Join(v.values, ",")
	}
	return v.values[len(v.values)-1]
}

func (v *rawFlagValue) IsBoolFlag() bool { return v.isBool }

var reservedFlags = map[string]struct{}{
	"h": {}, "help": {}, "version": {},
	"v": {}, "verbose": {}, "q": {}, "quiet": {},
	"json": {}, "log-level": {}, "color": {}, "no-color": {},
}

// Bind registers flags for structPtr on fs and returns a binding that resolves
// flag/env/default precedence into the same struct pointer. Supported tags are
// flag, env, default, required, validate, and usage.
func Bind(fs *flag.FlagSet, structPtr any) (*Binding, error) {
	if fs == nil {
		return nil, fmt.Errorf("clikit: nil FlagSet")
	}
	if structPtr == nil {
		return nil, fmt.Errorf("clikit: nil options pointer")
	}

	v := reflect.ValueOf(structPtr)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil, fmt.Errorf("clikit: options must be a non-nil pointer to struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("clikit: options must be a pointer to struct")
	}
	t := v.Type()
	b := &Binding{target: v}
	seen := map[string]string{}

	for i := range t.NumField() {
		field := t.Field(i)
		fieldVal := v.Field(i)
		if !field.IsExported() {
			continue
		}

		flagName := cleanFlagName(field.Tag.Get("flag"))
		envName := field.Tag.Get("env")
		def, hasDefault := field.Tag.Lookup("default")
		validate := field.Tag.Get("validate")
		required := field.Tag.Get("required")
		if flagName == "" && envName == "" && !hasDefault && validate == "" {
			continue
		}

		if !supportedType(fieldVal.Type()) {
			return nil, fmt.Errorf("clikit: unsupported option field %s type %s", field.Name, fieldVal.Type())
		}

		fb := fieldBinding{
			index:        i,
			name:         field.Name,
			flagName:     flagName,
			envName:      envName,
			defaultValue: def,
			hasDefault:   hasDefault,
			required:     required,
			validate:     validate,
			isSlice:      fieldVal.Type() == reflect.TypeOf([]string{}),
		}

		if flagName != "" {
			if _, ok := reservedFlags[flagName]; ok {
				return nil, fmt.Errorf("clikit: option field %s uses reserved global flag %q", field.Name, flagName)
			}
			if prev, ok := seen[flagName]; ok {
				return nil, fmt.Errorf("clikit: duplicate flag %q on fields %s and %s", flagName, prev, field.Name)
			}
			if fs.Lookup(flagName) != nil {
				return nil, fmt.Errorf("clikit: duplicate flag %q already registered", flagName)
			}
			seen[flagName] = field.Name
			rv := &rawFlagValue{isBool: fieldVal.Kind() == reflect.Bool, slice: fb.isSlice}
			fb.value = rv
			fs.Var(rv, flagName, field.Tag.Get("usage"))
		}

		b.fields = append(b.fields, fb)
	}

	return b, nil
}

func cleanFlagName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, "-")
	return name
}

func supportedType(t reflect.Type) bool {
	if t == reflect.TypeOf(time.Duration(0)) || t == reflect.TypeOf([]string{}) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
		return true
	default:
		return false
	}
}

// Resolve populates the bound struct from explicit flags, environment
// variables, and defaults in that precedence order.
func (b *Binding) Resolve(fs *flag.FlagSet) error {
	if b == nil {
		return nil
	}
	explicit := map[string]struct{}{}
	if fs != nil {
		fs.Visit(func(f *flag.Flag) {
			explicit[f.Name] = struct{}{}
		})
	}

	for _, fb := range b.fields {
		fieldVal := b.target.Field(fb.index)
		raw, fromFlag, ok := fb.resolveRaw(explicit)
		if !ok || raw == "" {
			if fb.required == "false" {
				continue
			}
			return usageError{err: fmt.Errorf("clikit: required option %s is not set", fb.optionName())}
		}

		if fb.isSlice && fromFlag {
			fieldVal.Set(reflect.ValueOf(splitSliceValues(fb.value.values)))
		} else {
			converted, err := config.Convert(fieldVal.Interface(), raw)
			if err != nil {
				return usageError{err: fmt.Errorf("clikit: invalid option %s: %w", fb.optionName(), err)}
			}
			convertedVal := reflect.ValueOf(converted)
			if !convertedVal.Type().AssignableTo(fieldVal.Type()) {
				return usageError{err: fmt.Errorf("clikit: invalid option %s: unsupported type %s", fb.optionName(), fieldVal.Type())}
			}
			fieldVal.Set(convertedVal)
		}

		if fb.validate != "" {
			if err := config.Check(fb.name, fieldVal.Interface(), fb.validate); err != nil {
				return usageError{err: err}
			}
		}
	}

	return nil
}

func (fb fieldBinding) resolveRaw(explicit map[string]struct{}) (string, bool, bool) {
	if fb.flagName != "" {
		if _, ok := explicit[fb.flagName]; ok {
			return fb.value.String(), true, true
		}
	}
	if fb.envName != "" {
		if raw := os.Getenv(fb.envName); raw != "" {
			return raw, false, true
		}
	}
	if fb.hasDefault {
		return fb.defaultValue, false, true
	}
	return "", false, false
}

func (fb fieldBinding) optionName() string {
	if fb.flagName != "" {
		return "--" + fb.flagName
	}
	if fb.envName != "" {
		return fb.envName
	}
	return fb.name
}

func splitSliceValues(values []string) []string {
	var out []string
	for _, value := range values {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			out = append(out, strings.TrimSpace(part))
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}
