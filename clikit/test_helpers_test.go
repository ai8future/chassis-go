package clikit

import (
	"bytes"
	"os"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func captureRun(t *testing.T, app *App, args []string) (int, string, string) {
	t.Helper()
	oldOut, oldErr := stdout, stderr
	var out, err bytes.Buffer
	stdout, stderr = &out, &err
	t.Cleanup(func() { stdout, stderr = oldOut, oldErr })
	code := app.Run(args)
	return code, out.String(), err.String()
}
