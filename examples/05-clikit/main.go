package main

import (
	"context"
	"os"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/clikit"
)

func main() {
	chassis.SetAppVersion(AppVersion)
	chassis.RequireMajor(11)
	app := clikit.New(clikit.Config{Name: "clikit-demo"}).Command(clikit.Command{Name: "greet", Short: "prints a greeting", Run: func(_ context.Context, c *clikit.Context) error {
		return c.Out.Emit(map[string]string{"message": "hello"})
	}})
	os.Exit(app.Run(os.Args))
}
