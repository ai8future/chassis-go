package clikit

import (
	"bytes"
	"io"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() {
	chassis.RequireMajor(11)
}

func TestColorModeTruthTable(t *testing.T) {
	old := streamIsTerminal
	streamIsTerminal = func(io.Writer) bool { return false }
	t.Cleanup(func() { streamIsTerminal = old })
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	if ColorAuto.Enabled(&bytes.Buffer{}) {
		t.Fatal("auto color should be disabled for non-TTY with no env overrides")
	}
	streamIsTerminal = func(io.Writer) bool { return true }
	if !ColorAuto.Enabled(&bytes.Buffer{}) {
		t.Fatal("auto color should be enabled for TTY")
	}
	t.Setenv("NO_COLOR", "1")
	if ColorAuto.Enabled(&bytes.Buffer{}) {
		t.Fatal("NO_COLOR should disable auto color")
	}
	t.Setenv("NO_COLOR", "")
	streamIsTerminal = func(io.Writer) bool { return false }
	t.Setenv("FORCE_COLOR", "1")
	if !ColorAuto.Enabled(&bytes.Buffer{}) {
		t.Fatal("FORCE_COLOR should enable auto color")
	}
	if !ColorAlways.Enabled(&bytes.Buffer{}) {
		t.Fatal("ColorAlways should always enable color")
	}
	if ColorNever.Enabled(&bytes.Buffer{}) {
		t.Fatal("ColorNever should never enable color")
	}
}

func TestColorHelpers(t *testing.T) {
	var buf bytes.Buffer
	if got := ColorNever.Bold(&buf, "hi"); got != "hi" {
		t.Fatalf("disabled Bold = %q", got)
	}
	if got := ColorAlways.Red(&buf, "hi"); got != "\x1b[31mhi\x1b[0m" {
		t.Fatalf("enabled Red = %q", got)
	}
}
