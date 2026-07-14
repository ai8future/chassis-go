package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/ai8future/chassis-go/v11/internal/coveragepolicy"
)

func main() {
	profilePath := flag.String("profile", "", "Go coverprofile to validate")
	policyPath := flag.String("policy", "testing/coverage-policy.json", "coverage policy JSON")
	flag.Parse()
	if *profilePath == "" {
		fatalf("-profile is required")
	}

	policy, err := coveragepolicy.LoadPolicy(*policyPath)
	if err != nil {
		fatalf("load policy: %v", err)
	}
	profile, err := os.Open(*profilePath)
	if err != nil {
		fatalf("open profile: %v", err)
	}
	coverage, err := coveragepolicy.ParseProfile(profile)
	profile.Close()
	if err != nil {
		fatalf("parse profile: %v", err)
	}
	packages, err := listPackages()
	if err != nil {
		fatalf("list packages: %v", err)
	}
	if err := coveragepolicy.Validate(policy, coverage, packages, time.Now()); err != nil {
		fatalf("coverage policy failed:\n%v", err)
	}
	fmt.Printf("coverage policy passed: aggregate %.1f%%, library floor %.1f%% with %d active exceptions\n", coverage.Total.Percent(), policy.LibraryMinimum, len(policy.Exceptions))
}

func listPackages() ([]coveragepolicy.Package, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(stdout)
	var packages []coveragepolicy.Package
	for {
		var pkg struct {
			ImportPath string
			Name       string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		packages = append(packages, coveragepolicy.Package{ImportPath: pkg.ImportPath, Name: pkg.Name})
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return packages, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
