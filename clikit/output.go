package clikit

import (
	"encoding/json"
	"fmt"
	"io"
)

// Emitter writes command product output to stdout. In JSON mode, human text
// helpers are suppressed so scripts receive only explicit JSON documents.
type Emitter struct {
	w     io.Writer
	json  bool
	color ColorMode
}

// NewEmitter creates an output emitter over w.
func NewEmitter(w io.Writer, jsonMode bool, color ColorMode) *Emitter {
	return &Emitter{w: w, json: jsonMode, color: color}
}

// Printf writes formatted human output unless JSON mode is active.
func (e *Emitter) Printf(format string, args ...any) {
	if e == nil || e.json {
		return
	}
	_, _ = fmt.Fprintf(e.w, format, args...)
}

// Println writes human output unless JSON mode is active.
func (e *Emitter) Println(args ...any) {
	if e == nil || e.json {
		return
	}
	_, _ = fmt.Fprintln(e.w, args...)
}

// Emit writes v as a single JSON document in JSON mode, or a readable line in
// human mode.
func (e *Emitter) Emit(v any) error {
	if e == nil {
		return nil
	}
	if e.json {
		enc := json.NewEncoder(e.w)
		return enc.Encode(v)
	}
	switch x := v.(type) {
	case string:
		_, err := fmt.Fprintln(e.w, x)
		return err
	case []byte:
		_, err := fmt.Fprintln(e.w, string(x))
		return err
	case fmt.Stringer:
		_, err := fmt.Fprintln(e.w, x.String())
		return err
	default:
		_, err := fmt.Fprintln(e.w, v)
		return err
	}
}

// JSON writes v as JSON regardless of the emitter's current human/json mode.
func (e *Emitter) JSON(v any) error {
	if e == nil {
		return nil
	}
	enc := json.NewEncoder(e.w)
	return enc.Encode(v)
}

// Color reports the emitter's color mode.
func (e *Emitter) Color() ColorMode {
	if e == nil {
		return ColorNever
	}
	return e.color
}
