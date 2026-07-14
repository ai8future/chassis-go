package coveragepolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/coveragepolicy"
)

func TestParseProfile(t *testing.T) {
	chassis.RequireMajor(11)
	profile := "mode: atomic\nexample.test/module/a.go:1.1,2.1 2 1\nexample.test/module/a.go:3.1,4.1 1 0\nexample.test/module/cmd/x/main.go:1.1,2.1 3 0\n"
	coverage, err := coveragepolicy.ParseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if got := coverage.Total.Percent(); got != 2.0/6.0*100 {
		t.Fatalf("aggregate = %.1f, want %.1f", got, 2.0/6.0*100)
	}
	if got := coverage.Packages["example.test/module"].Percent(); got != 2.0/3.0*100 {
		t.Fatalf("library = %.1f, want %.1f", got, 2.0/3.0*100)
	}
}

func TestParseProfileRejectsMalformedInput(t *testing.T) {
	chassis.RequireMajor(11)
	inputs := []string{
		"",
		"not-a-mode\n",
		"mode: atomic\nbad\n",
		"mode: atomic\nmissing-colon 1 1\n",
		"mode: atomic\nexample.test/module/a.go:1.1,2.1 nope 1\n",
		"mode: atomic\nexample.test/module/a.go:1.1,2.1 1 nope\n",
	}
	for _, input := range inputs {
		if _, err := coveragepolicy.ParseProfile(strings.NewReader(input)); err == nil {
			t.Fatalf("ParseProfile(%q) unexpectedly succeeded", input)
		}
	}
}

func TestLoadPolicyStrictJSON(t *testing.T) {
	chassis.RequireMajor(11)
	tests := []struct {
		name    string
		content string
		wantOK  bool
	}{
		{name: "valid", content: `{"schema_version":1,"module":"example.test/module","aggregate_minimum":75,"library_minimum":75,"exceptions":[]}`, wantOK: true},
		{name: "unknown", content: `{"schema_version":1,"unknown":true}`},
		{name: "multiple", content: "{}\n{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(filename, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := coveragepolicy.LoadPolicy(filename)
			if (err == nil) != tt.wantOK {
				t.Fatalf("LoadPolicy success = %v, want %v: %v", err == nil, tt.wantOK, err)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	chassis.RequireMajor(11)
	today := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	packages := []coveragepolicy.Package{
		{ImportPath: "example.test/module", Name: "module"},
		{ImportPath: "example.test/module/low", Name: "low"},
		{ImportPath: "example.test/module/cmd/x", Name: "main"},
	}
	coverage := coveragepolicy.Coverage{
		Total: coveragepolicy.Counts{Covered: 80, Total: 100},
		Packages: map[string]coveragepolicy.Counts{
			"example.test/module":       {Covered: 80, Total: 100},
			"example.test/module/low":   {Covered: 7, Total: 10},
			"example.test/module/cmd/x": {Covered: 0, Total: 10},
		},
	}
	base := coveragepolicy.Policy{
		SchemaVersion:    1,
		Module:           "example.test/module",
		AggregateMinimum: 75,
		LibraryMinimum:   75,
		Exceptions: []coveragepolicy.Exception{{
			Kind: "package", Target: "example.test/module/low", Minimum: 70,
			Owner: "maintainers", Rationale: "covered next", Expires: "2026-08-01",
		}},
	}

	tests := []struct {
		name   string
		mutate func(*coveragepolicy.Policy, *coveragepolicy.Coverage)
		want   string
	}{
		{name: "valid"},
		{name: "expired", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) { p.Exceptions[0].Expires = "2026-07-14" }, want: "expired"},
		{name: "unmatched", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) {
			p.Exceptions[0].Target = "example.test/module/missing"
		}, want: "unmatched exception"},
		{name: "stale", mutate: func(_ *coveragepolicy.Policy, c *coveragepolicy.Coverage) {
			c.Packages["example.test/module/low"] = coveragepolicy.Counts{Covered: 8, Total: 10}
		}, want: "stale exception"},
		{name: "below exception floor", mutate: func(_ *coveragepolicy.Policy, c *coveragepolicy.Coverage) {
			c.Packages["example.test/module/low"] = coveragepolicy.Counts{Covered: 6, Total: 10}
		}, want: "temporary minimum"},
		{name: "missing exception", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) { p.Exceptions = nil }, want: "without exception"},
		{name: "aggregate", mutate: func(_ *coveragepolicy.Policy, c *coveragepolicy.Coverage) {
			c.Total = coveragepolicy.Counts{Covered: 70, Total: 100}
		}, want: "aggregate coverage"},
		{name: "bad schema", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) { p.SchemaVersion = 2 }, want: "schema_version"},
		{name: "bad percentages", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) { p.LibraryMinimum = 101 }, want: "between 0 and 100"},
		{name: "invalid exception metadata", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) {
			p.Exceptions[0].Kind = "function"
			p.Exceptions[0].Owner = ""
			p.Exceptions[0].Expires = "tomorrow"
		}, want: "owner and rationale"},
		{name: "entrypoint exception", mutate: func(p *coveragepolicy.Policy, _ *coveragepolicy.Coverage) {
			p.Exceptions[0].Target = "example.test/module/cmd/x"
		}, want: "main packages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := base
			policy.Exceptions = append([]coveragepolicy.Exception(nil), base.Exceptions...)
			candidate := coveragepolicy.Coverage{Total: coverage.Total, Packages: make(map[string]coveragepolicy.Counts)}
			for key, value := range coverage.Packages {
				candidate.Packages[key] = value
			}
			if tt.mutate != nil {
				tt.mutate(&policy, &candidate)
			}
			err := coveragepolicy.Validate(policy, candidate, packages, today)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
