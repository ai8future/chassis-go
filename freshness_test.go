package chassis

import "testing"

func TestSemverNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"10.0.11", "10.0.8", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0.0", "2.0.0", false},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"abc", "1.0.0", false},
		{"1.0", "1.0.0", false},
		{"1.0.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0.1", false},
	}
	for _, tt := range tests {
		got := semverNewer(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
