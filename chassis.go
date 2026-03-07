// Package chassis provides the chassis-go toolkit version and version compatibility check.
package chassis

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

//go:embed VERSION
var rawVersion string

// Version returns the current release of chassis-go, read from the VERSION file.
// This is the single source of truth for the version number.
var Version = strings.TrimSpace(rawVersion)

var majorVersionAsserted atomic.Bool

// RequireMajor crashes the process if the chassis major version does not match
// the required version. Services must call this at the top of main() before
// using any other chassis module.
func RequireMajor(required int) {
	majorVersionAsserted.Store(true)
	parts := strings.SplitN(Version, ".", 2)
	actual, err := strconv.Atoi(parts[0])
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"FATAL: Invalid VERSION format %q — expected semver like \"5.0.0\".\n", Version)
		os.Exit(1)
	}
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
	if !majorVersionAsserted.Load() {
		parts := strings.SplitN(Version, ".", 2)
		fmt.Fprintf(os.Stderr,
			"FATAL: chassis.RequireMajor() must be called before using any chassis module.\n"+
				"Add chassis.RequireMajor(%s) to main() before any other chassis calls.\n", parts[0])
		os.Exit(1)
	}
}

// ResetVersionCheck is for testing only — resets the version assertion state.
func ResetVersionCheck() {
	majorVersionAsserted.Store(false)
}

// Standard port role offsets for chassis transport roles.
const (
	PortHTTP    = 0 // Primary HTTP/REST API
	PortGRPC    = 1 // gRPC transport
	PortMetrics = 2 // Admin, Prometheus metrics, health
)

// Port returns a deterministic port number derived from a service name using
// the djb2 hash algorithm. The result is in the range 5000–48000, well below
// the OS ephemeral port range (49152+).
//
// The optional offset parameter (default 0) allows multiple ports per service:
//
//	chassis.Port("my_svc")                    // base port (HTTP)
//	chassis.Port("my_svc", chassis.PortGRPC)  // base + 1 (gRPC)
//	chassis.Port("my_svc", chassis.PortMetrics) // base + 2 (metrics)
func Port(name string, offset ...int) int {
	var h uint32 = 5381
	for i := 0; i < len(name); i++ {
		h = h*33 + uint32(name[i])
	}
	port := 5000 + int(h%43001)
	if len(offset) > 0 {
		port += offset[0]
	}
	return port
}
