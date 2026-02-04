// Package chassis provides the chassis-go toolkit version and version compatibility check.
package chassis

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Version is the current release of chassis-go.
const Version = "3.0.0"

var majorVersionAsserted bool

// RequireMajor crashes the process if the chassis major version does not match
// the required version. Services must call this at the top of main() before
// using any other chassis module.
func RequireMajor(required int) {
	majorVersionAsserted = true
	parts := strings.SplitN(Version, ".", 2)
	actual, _ := strconv.Atoi(parts[0])
	if actual != required {
		fmt.Fprintf(os.Stderr,
			"FATAL: Service requires chassis v%d but v%s is installed.\n"+
				"Review the v%d migration guide and update your RequireMajor(%d) call.\n",
			required, Version, actual, actual)
		os.Exit(1)
	}
}

// AssertVersionChecked crashes if RequireMajor has not been called yet.
// Other chassis modules call this at their entry points.
func AssertVersionChecked() {
	if !majorVersionAsserted {
		fmt.Fprintf(os.Stderr,
			"FATAL: chassis.RequireMajor() must be called before using any chassis module.\n"+
				"Add chassis.RequireMajor(3) to main() before any other chassis calls.\n")
		os.Exit(1)
	}
}

// ResetVersionCheck is for testing only — resets the version assertion state.
func ResetVersionCheck() {
	majorVersionAsserted = false
}
