// Package clikit provides a small, stdlib-first toolkit for chassis CLIs.
package clikit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

const (
	// ExitOK is returned when a command succeeds.
	ExitOK = 0
	// ExitError is returned for ordinary runtime failures.
	ExitError = 1
	// ExitUsage is returned for usage, parse, validation, or registration errors.
	ExitUsage = 2
)

// ExitErr lets command handlers choose a specific process exit code while still
// returning a useful diagnostic error.
type ExitErr struct {
	Code int
	Err  error
}

func (e ExitErr) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e ExitErr) Unwrap() error { return e.Err }

// Config controls the app composition root. clikit is intentionally additive:
// main() still owns chassis.SetAppVersion, chassis.RequireMajor, and os.Exit.
type Config struct {
	Name        string
	AppVersion  string
	UseRegistry bool
	StdFlags    StdFlagSet
}

// StdFlagSet selects optional global flags. Help flags are always registered;
// the zero value enables the default standard flag set.
type StdFlagSet struct {
	Disable  bool
	JSON     bool
	LogLevel bool
	Verbose  bool
	Quiet    bool
	Color    bool
}

// DefaultStdFlags returns clikit's conventional global flag set.
func DefaultStdFlags() StdFlagSet {
	return StdFlagSet{JSON: true, LogLevel: true, Verbose: true, Quiet: true, Color: true}
}

func (s StdFlagSet) normalized() StdFlagSet {
	if s.Disable {
		return StdFlagSet{}
	}
	if !s.JSON && !s.LogLevel && !s.Verbose && !s.Quiet && !s.Color {
		return DefaultStdFlags()
	}
	return s
}

// Command defines one flat subcommand.
type Command struct {
	Name  string
	Short string
	Long  string
	Flags any
	Run   func(ctx context.Context, cctx *Context) error
}

// Context is passed to command handlers.
type Context struct {
	Args    []string
	Options any
	Log     *slog.Logger
	Out     *Emitter
	Color   ColorMode
}

type usageError struct{ err error }

func (e usageError) Error() string {
	if e.err == nil {
		return "usage error"
	}
	return e.err.Error()
}

func (e usageError) Unwrap() error { return e.err }

func isUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee ExitErr
	if errors.As(err, &ee) {
		if ee.Code == 0 {
			return ExitOK
		}
		return ee.Code
	}
	if isUsage(err) {
		return ExitUsage
	}
	return ExitError
}

// SuppressAutoRebuild disables chassis freshness auto-rebuild for the current
// process. Call it before chassis.RequireMajor when using it.
func SuppressAutoRebuild() {
	_ = os.Setenv("CHASSIS_NO_REBUILD", "1")
}
