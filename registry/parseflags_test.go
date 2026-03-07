package registry

import (
	"testing"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want map[string]string
	}{
		{
			name: "empty args",
			args: []string{"./mybinary"},
			want: map[string]string{},
		},
		{
			name: "equals form",
			args: []string{"./mybinary", "--input=/data/file.csv"},
			want: map[string]string{"input": "/data/file.csv"},
		},
		{
			name: "boolean flag no value next starts with dash",
			args: []string{"./mybinary", "--dry-run", "--verbose"},
			want: map[string]string{"dry-run": "true", "verbose": "true"},
		},
		{
			name: "space-separated value",
			args: []string{"./mybinary", "--batch-size", "500"},
			want: map[string]string{"batch-size": "500"},
		},
		{
			name: "short flag with value",
			args: []string{"./mybinary", "-n", "500"},
			want: map[string]string{"n": "500"},
		},
		{
			name: "short boolean flag",
			args: []string{"./mybinary", "-v"},
			want: map[string]string{"v": "true"},
		},
		{
			name: "mixed flags",
			args: []string{"./mybinary", "--input=/data/file.csv", "--dry-run", "-n", "500", "-v", "--batch-size", "100"},
			want: map[string]string{
				"input":      "/data/file.csv",
				"dry-run":    "true",
				"n":          "500",
				"v":          "true",
				"batch-size": "100",
			},
		},
		{
			name: "sensitive flag equals form redacted",
			args: []string{"./mybinary", "--api-key=supersecret"},
			want: map[string]string{"api-key": "REDACTED"},
		},
		{
			name: "sensitive flag space form redacted",
			args: []string{"./mybinary", "--password", "hunter2"},
			want: map[string]string{"password": "REDACTED"},
		},
		{
			name: "non-flag positional args skipped",
			args: []string{"./mybinary", "positional", "--flag", "value"},
			want: map[string]string{"flag": "value"},
		},
		{
			name: "no args at all",
			args: []string{},
			want: map[string]string{},
		},
		{
			name: "only binary name",
			args: []string{"./mybinary"},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFlags(tt.args)
			if len(got) != len(tt.want) {
				t.Errorf("parseFlags(%v) returned %d flags, want %d\n  got:  %v\n  want: %v",
					tt.args, len(got), len(tt.want), got, tt.want)
				return
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("parseFlags(%v)[%q] = %q, want %q", tt.args, k, got[k], want)
				}
			}
		})
	}
}

func TestIsSensitiveFlag(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"password", true},
		{"api-key", true},
		{"api_key", true},
		{"token", true},
		{"secret", true},
		{"auth", true},
		{"credential", true},
		{"input", false},
		{"batch-size", false},
		{"v", false},
		{"dry-run", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSensitiveFlag(tt.name)
			if got != tt.want {
				t.Errorf("isSensitiveFlag(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
