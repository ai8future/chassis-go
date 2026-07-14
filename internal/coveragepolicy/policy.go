// Package coveragepolicy validates repository coverage against an expiring,
// machine-readable exception policy.
package coveragepolicy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Policy is the strict JSON schema consumed by scripts/check-coverage.sh.
type Policy struct {
	SchemaVersion    int         `json:"schema_version"`
	Module           string      `json:"module"`
	AggregateMinimum float64     `json:"aggregate_minimum"`
	LibraryMinimum   float64     `json:"library_minimum"`
	Exceptions       []Exception `json:"exceptions"`
}

// Exception temporarily lowers the floor for one exact library package.
type Exception struct {
	Kind      string  `json:"kind"`
	Target    string  `json:"target"`
	Minimum   float64 `json:"minimum"`
	Owner     string  `json:"owner"`
	Rationale string  `json:"rationale"`
	Expires   string  `json:"expires"`
}

// Package describes a package returned by go list.
type Package struct {
	ImportPath string
	Name       string
}

// Coverage contains statement counts from one Go cover profile.
type Coverage struct {
	Packages map[string]Counts
	Total    Counts
}

// Counts are covered and total statements.
type Counts struct {
	Covered int
	Total   int
}

// Percent returns statement coverage as a percentage.
func (c Counts) Percent() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Covered) * 100 / float64(c.Total)
}

// LoadPolicy decodes policy with unknown-field rejection.
func LoadPolicy(filename string) (Policy, error) {
	f, err := os.Open(filename)
	if err != nil {
		return Policy{}, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, err
	}
	if err := ensureEOF(decoder); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// ParseProfile reads a Go coverprofile and aggregates statements by package.
func ParseProfile(r io.Reader) (Coverage, error) {
	coverage := Coverage{Packages: make(map[string]Counts)}
	scanner := bufio.NewScanner(r)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 {
			if !strings.HasPrefix(line, "mode: ") {
				return Coverage{}, fmt.Errorf("invalid coverage mode line %q", line)
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return Coverage{}, fmt.Errorf("coverage line %d: expected 3 fields", lineNumber)
		}
		location := fields[0]
		colon := strings.LastIndex(location, ":")
		if colon < 1 {
			return Coverage{}, fmt.Errorf("coverage line %d: invalid location", lineNumber)
		}
		packagePath := path.Dir(location[:colon])
		statements, err := strconv.Atoi(fields[1])
		if err != nil || statements < 0 {
			return Coverage{}, fmt.Errorf("coverage line %d: invalid statement count %q", lineNumber, fields[1])
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count < 0 {
			return Coverage{}, fmt.Errorf("coverage line %d: invalid execution count %q", lineNumber, fields[2])
		}
		counts := coverage.Packages[packagePath]
		counts.Total += statements
		coverage.Total.Total += statements
		if count > 0 {
			counts.Covered += statements
			coverage.Total.Covered += statements
		}
		coverage.Packages[packagePath] = counts
	}
	if err := scanner.Err(); err != nil {
		return Coverage{}, err
	}
	if lineNumber == 0 || coverage.Total.Total == 0 {
		return Coverage{}, errors.New("coverage profile contains no statements")
	}
	return coverage, nil
}

// Validate enforces aggregate and non-main library floors and validates that
// every exception is necessary, matched, unexpired, and above its temporary
// minimum.
func Validate(policy Policy, coverage Coverage, packages []Package, today time.Time) error {
	var problems []string
	if policy.SchemaVersion != 1 {
		problems = append(problems, fmt.Sprintf("unsupported schema_version %d", policy.SchemaVersion))
	}
	if policy.Module == "" {
		problems = append(problems, "module must not be empty")
	}
	if err := validatePercent("aggregate_minimum", policy.AggregateMinimum); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validatePercent("library_minimum", policy.LibraryMinimum); err != nil {
		problems = append(problems, err.Error())
	}

	packageNames := make(map[string]string, len(packages))
	for _, pkg := range packages {
		packageNames[pkg.ImportPath] = pkg.Name
	}
	exceptions := make(map[string]Exception, len(policy.Exceptions))
	for i, exception := range policy.Exceptions {
		prefix := fmt.Sprintf("exception[%d]", i)
		if exception.Kind != "package" {
			problems = append(problems, prefix+": kind must be package")
		}
		if exception.Target == "" || !strings.HasPrefix(exception.Target, policy.Module) {
			problems = append(problems, prefix+": target must be an exact package in module")
		}
		if _, duplicate := exceptions[exception.Target]; duplicate {
			problems = append(problems, prefix+": duplicate target "+exception.Target)
		}
		exceptions[exception.Target] = exception
		if err := validatePercent(prefix+" minimum", exception.Minimum); err != nil {
			problems = append(problems, err.Error())
		}
		if strings.TrimSpace(exception.Owner) == "" || strings.TrimSpace(exception.Rationale) == "" {
			problems = append(problems, prefix+": owner and rationale are required")
		}
		expiry, err := time.Parse("2006-01-02", exception.Expires)
		if err != nil {
			problems = append(problems, prefix+": expires must use YYYY-MM-DD")
		} else if expiry.Before(day(today)) {
			problems = append(problems, prefix+": expired on "+exception.Expires)
		}
		if name, exists := packageNames[exception.Target]; !exists {
			problems = append(problems, prefix+": target is not returned by go list")
		} else if name == "main" {
			problems = append(problems, prefix+": main packages are entrypoints, not library exceptions")
		}
	}

	if aggregate := coverage.Total.Percent(); aggregate+1e-9 < policy.AggregateMinimum {
		problems = append(problems, fmt.Sprintf("aggregate coverage %.1f%% is below %.1f%%", aggregate, policy.AggregateMinimum))
	}

	matched := make(map[string]bool, len(exceptions))
	paths := make([]string, 0, len(coverage.Packages))
	for packagePath := range coverage.Packages {
		paths = append(paths, packagePath)
	}
	sort.Strings(paths)
	for _, packagePath := range paths {
		counts := coverage.Packages[packagePath]
		if counts.Total == 0 || packageNames[packagePath] == "main" {
			continue
		}
		actual := counts.Percent()
		exception, excepted := exceptions[packagePath]
		if actual+1e-9 >= policy.LibraryMinimum {
			if excepted {
				problems = append(problems, fmt.Sprintf("stale exception %s: coverage %.1f%% now meets %.1f%%", packagePath, actual, policy.LibraryMinimum))
				matched[packagePath] = true
			}
			continue
		}
		if !excepted {
			problems = append(problems, fmt.Sprintf("library package %s coverage %.1f%% is below %.1f%% without exception", packagePath, actual, policy.LibraryMinimum))
			continue
		}
		matched[packagePath] = true
		if actual+1e-9 < exception.Minimum {
			problems = append(problems, fmt.Sprintf("excepted package %s coverage %.1f%% is below temporary minimum %.1f%%", packagePath, actual, exception.Minimum))
		}
	}
	for target := range exceptions {
		if !matched[target] {
			problems = append(problems, "unmatched exception "+target)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func validatePercent(name string, value float64) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("%s must be between 0 and 100", name)
	}
	return nil
}

func day(t time.Time) time.Time {
	year, month, date := t.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("policy contains multiple JSON values")
}
