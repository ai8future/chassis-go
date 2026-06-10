package clikit

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ColorMode controls ANSI color output.
type ColorMode int

const (
	ColorAuto ColorMode = iota
	ColorAlways
	ColorNever
)

var streamIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func parseColorMode(raw string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return ColorAuto, nil
	case "always":
		return ColorAlways, nil
	case "never":
		return ColorNever, nil
	default:
		return ColorAuto, fmt.Errorf("invalid color mode %q (want auto, always, or never)", raw)
	}
}

// Enabled reports whether color should be emitted for w.
func (m ColorMode) Enabled(w io.Writer) bool {
	switch m {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		if os.Getenv("FORCE_COLOR") != "" {
			return true
		}
		return streamIsTerminal(w)
	}
}

func (m ColorMode) sgr(w io.Writer, code, s string) string {
	if !m.Enabled(w) || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Bold wraps s in bold ANSI styling when color is enabled.
func (m ColorMode) Bold(w io.Writer, s string) string { return m.sgr(w, "1", s) }

// Red wraps s in red ANSI styling when color is enabled.
func (m ColorMode) Red(w io.Writer, s string) string { return m.sgr(w, "31", s) }

// Dim wraps s in dim ANSI styling when color is enabled.
func (m ColorMode) Dim(w io.Writer, s string) string { return m.sgr(w, "2", s) }
