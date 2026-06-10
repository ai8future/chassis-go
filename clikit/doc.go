// Package clikit provides a small, stdlib-first toolkit for chassis command
// line programs and batch tools.
//
// clikit does not own main and never calls os.Exit. Applications keep the
// chassis prologue explicit:
//
//	chassis.SetAppVersion(myapp.AppVersion)
//	chassis.RequireMajor(11)
//	app := clikit.New(clikit.Config{Name: "mytool"})
//	os.Exit(app.Run(os.Args))
//
// Command handlers should write user-facing results through Context.Out. Logs,
// errors, help on usage failure, and diagnostics are written to stderr by the
// app runtime so --json output remains pipeable.
package clikit
