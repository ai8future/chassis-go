package clikit

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/logz"
	"github.com/ai8future/chassis-go/v11/registry"
)

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// App is a flat subcommand router. It never calls os.Exit.
type App struct {
	cfg      Config
	commands map[string]Command
	order    []string
	errs     []error
}

// New creates an App. If Config.AppVersion is set, it forwards the value to
// chassis.SetAppVersion for callers that construct the app before RequireMajor.
func New(cfg Config) *App {
	if cfg.AppVersion != "" {
		chassis.SetAppVersion(cfg.AppVersion)
	}
	return &App{cfg: cfg, commands: map[string]Command{}}
}

// Command registers a subcommand and returns a for chaining.
func (a *App) Command(c Command) *App {
	if a == nil {
		return a
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		a.errs = append(a.errs, fmt.Errorf("clikit: command name is required"))
		return a
	}
	if strings.ContainsAny(name, " \t\n\r") {
		a.errs = append(a.errs, fmt.Errorf("clikit: command name %q must not contain whitespace", name))
		return a
	}
	c.Name = name
	if _, exists := a.commands[name]; exists {
		a.errs = append(a.errs, fmt.Errorf("clikit: duplicate command %q", name))
		return a
	}
	a.commands[name] = c
	a.order = append(a.order, name)
	return a
}

// Run executes the app for args and returns an exit code. It never calls os.Exit.
func (a *App) Run(args []string) (code int) {
	registryStarted := false
	defer func() {
		if registryStarted {
			registry.ShutdownCLI(code)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "panic: %v\n", r)
			code = ExitError
		}
	}()

	err := a.run(args, &registryStarted)
	if err != nil && !isUsage(err) {
		fmt.Fprintln(stderr, err)
	}
	return exitCode(err)
}

func (a *App) run(args []string, registryStarted *bool) error {
	if a == nil {
		fmt.Fprintln(stderr, "clikit: nil App")
		return usageError{err: fmt.Errorf("clikit: nil App")}
	}
	if len(args) == 0 {
		args = []string{a.appName("")}
	}
	appName := a.appName(args[0])

	if len(a.errs) > 0 {
		for _, err := range a.errs {
			fmt.Fprintln(stderr, err)
		}
		a.writeTopUsage(stderr, appName)
		return usageError{err: a.errs[0]}
	}

	std, rest, err := a.parseGlobalFlags(appName, args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		a.writeTopUsage(stderr, appName)
		return usageError{err: err}
	}
	if std.help || len(rest) == 0 {
		a.writeTopUsage(stdout, appName)
		return nil
	}

	cmdName := rest[0]
	cmd, ok := a.commands[cmdName]
	if !ok {
		err := fmt.Errorf("unknown command %q", cmdName)
		fmt.Fprintln(stderr, err)
		a.writeTopUsage(stderr, appName)
		return usageError{err: err}
	}
	if cmd.Run == nil {
		err := fmt.Errorf("clikit: command %q has nil Run", cmdName)
		fmt.Fprintln(stderr, err)
		a.writeCommandUsage(stderr, appName, cmd, nil)
		return usageError{err: err}
	}

	cmdFS := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmdFS.SetOutput(io.Discard)
	cmdHelp := false
	cmdFS.BoolVar(&cmdHelp, "help", false, "show command help")
	cmdFS.BoolVar(&cmdHelp, "h", false, "show command help")

	var binding *Binding
	if cmd.Flags != nil {
		binding, err = Bind(cmdFS, cmd.Flags)
		if err != nil {
			fmt.Fprintln(stderr, err)
			a.writeCommandUsage(stderr, appName, cmd, cmdFS)
			return usageError{err: err}
		}
	}

	if err := cmdFS.Parse(rest[1:]); err != nil {
		fmt.Fprintln(stderr, err)
		a.writeCommandUsage(stderr, appName, cmd, cmdFS)
		return usageError{err: err}
	}
	if cmdHelp {
		a.writeCommandUsage(stdout, appName, cmd, cmdFS)
		return nil
	}
	if binding != nil {
		if err := binding.Resolve(cmdFS); err != nil {
			fmt.Fprintln(stderr, err)
			a.writeCommandUsage(stderr, appName, cmd, cmdFS)
			return err
		}
	}

	if a.cfg.UseRegistry {
		if err := registry.InitCLI(chassis.Version); err != nil {
			return fmt.Errorf("clikit: registry init: %w", err)
		}
		*registryStarted = true
	}

	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	level := std.logLevelName()
	logger := newLogger(std.json, level, stderr)
	color := std.colorMode
	cctx := &Context{
		Args:    cmdFS.Args(),
		Options: cmd.Flags,
		Log:     logger,
		Out:     NewEmitter(stdout, std.json, color),
		Color:   color,
	}

	if err := cmd.Run(runCtx, cctx); err != nil {
		return err
	}
	return nil
}

func (a *App) appName(arg0 string) string {
	if a != nil && strings.TrimSpace(a.cfg.Name) != "" {
		return strings.TrimSpace(a.cfg.Name)
	}
	if strings.TrimSpace(arg0) != "" {
		return filepath.Base(arg0)
	}
	if len(os.Args) > 0 {
		return filepath.Base(os.Args[0])
	}
	return "app"
}

type stdState struct {
	help      bool
	json      bool
	verbose   bool
	quiet     bool
	logLevel  string
	colorRaw  string
	noColor   bool
	colorMode ColorMode
}

func (s stdState) logLevelName() string {
	level := "info"
	if s.verbose {
		level = "debug"
	}
	if s.quiet {
		level = "error"
	}
	if strings.TrimSpace(s.logLevel) != "" {
		level = strings.TrimSpace(s.logLevel)
	}
	return level
}

func (a *App) parseGlobalFlags(appName string, raw []string) (stdState, []string, error) {
	var state stdState
	state.colorRaw = "auto"
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&state.help, "help", false, "show help")
	fs.BoolVar(&state.help, "h", false, "show help")

	std := a.cfg.StdFlags.normalized()
	if std.JSON {
		fs.BoolVar(&state.json, "json", false, "emit command output as JSON")
	}
	if std.LogLevel {
		fs.StringVar(&state.logLevel, "log-level", "", "log level: debug, info, warn, error")
	}
	if std.Verbose {
		fs.BoolVar(&state.verbose, "verbose", false, "enable debug logs")
		fs.BoolVar(&state.verbose, "v", false, "enable debug logs")
	}
	if std.Quiet {
		fs.BoolVar(&state.quiet, "quiet", false, "only log errors")
		fs.BoolVar(&state.quiet, "q", false, "only log errors")
	}
	if std.Color {
		fs.StringVar(&state.colorRaw, "color", "auto", "color mode: auto, always, never")
		fs.BoolVar(&state.noColor, "no-color", false, "disable color")
	}

	if err := fs.Parse(raw); err != nil {
		return state, nil, err
	}
	colorMode, err := parseColorMode(state.colorRaw)
	if err != nil {
		return state, nil, err
	}
	if state.noColor {
		colorMode = ColorNever
	}
	state.colorMode = colorMode
	return state, fs.Args(), nil
}

func newLogger(jsonMode bool, level string, w io.Writer) *slog.Logger {
	if jsonMode {
		return logz.NewWithWriter(level, w)
	}
	return logz.NewTextWithWriter(level, w)
}

func (a *App) writeTopUsage(w io.Writer, appName string) {
	fmt.Fprintf(w, "Usage:\n  %s [global flags] <command> [flags] [args]\n\n", appName)
	if len(a.order) > 0 {
		fmt.Fprintln(w, "Commands:")
		for _, name := range a.sortedCommandNames() {
			cmd := a.commands[name]
			if cmd.Short != "" {
				fmt.Fprintf(w, "  %-12s %s\n", name, cmd.Short)
			} else {
				fmt.Fprintf(w, "  %s\n", name)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  -h, --help           show help")
	std := a.cfg.StdFlags.normalized()
	if std.JSON {
		fmt.Fprintln(w, "      --json           emit command output as JSON")
	}
	if std.LogLevel {
		fmt.Fprintln(w, "      --log-level      log level: debug, info, warn, error")
	}
	if std.Verbose {
		fmt.Fprintln(w, "  -v, --verbose        enable debug logs")
	}
	if std.Quiet {
		fmt.Fprintln(w, "  -q, --quiet          only log errors")
	}
	if std.Color {
		fmt.Fprintln(w, "      --color          color mode: auto, always, never")
		fmt.Fprintln(w, "      --no-color       disable color")
	}
}

func (a *App) writeCommandUsage(w io.Writer, appName string, cmd Command, fs *flag.FlagSet) {
	fmt.Fprintf(w, "Usage:\n  %s [global flags] %s [flags] [args]\n\n", appName, cmd.Name)
	if cmd.Long != "" {
		fmt.Fprintln(w, cmd.Long)
		fmt.Fprintln(w)
	} else if cmd.Short != "" {
		fmt.Fprintln(w, cmd.Short)
		fmt.Fprintln(w)
	}
	if fs == nil {
		return
	}
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
	if strings.TrimSpace(buf.String()) != "" {
		fmt.Fprintln(w, "Flags:")
		fmt.Fprint(w, buf.String())
	}
}

func (a *App) sortedCommandNames() []string {
	names := append([]string(nil), a.order...)
	sort.Strings(names)
	return names
}
