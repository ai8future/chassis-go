package guard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteAddrKeyFunc(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "strips port", remoteAddr: "203.0.113.10:4321", want: "203.0.113.10"},
		{name: "no port returns raw", remoteAddr: "203.0.113.11", want: "203.0.113.11"},
	}

	keyFunc := RemoteAddr()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if got := keyFunc(req); got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestXForwardedForKeyFunc(t *testing.T) {
	cases := []struct {
		name        string
		trustedCIDR string
		remoteAddr  string
		xff         string
		want        string
	}{
		{
			name:        "trusted uses first xff",
			trustedCIDR: "10.0.0.0/8",
			remoteAddr:  "10.1.2.3:8080",
			xff:         "203.0.113.5, 10.0.0.1",
			want:        "203.0.113.5",
		},
		{
			name:        "untrusted ignores xff",
			trustedCIDR: "10.0.0.0/8",
			remoteAddr:  "192.168.1.10:8080",
			xff:         "203.0.113.6",
			want:        "192.168.1.10",
		},
		{
			name:        "empty xff falls back",
			trustedCIDR: "10.0.0.0/8",
			remoteAddr:  "10.2.3.4:8080",
			xff:         "",
			want:        "10.2.3.4",
		},
		{
			name:        "non-IP xff value falls back to remote",
			trustedCIDR: "10.0.0.0/8",
			remoteAddr:  "10.1.2.3:8080",
			xff:         "not-an-ip, 10.0.0.1",
			want:        "10.1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keyFunc := XForwardedFor(tc.trustedCIDR)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := keyFunc(req); got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestXForwardedForPanicsOnInvalidCIDR(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on invalid CIDR, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "invalid trusted CIDR") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	XForwardedFor("not-a-cidr")
}

func TestHeaderKeyFunc(t *testing.T) {
	keyFunc := HeaderKey("X-API-Key")

	// With header present
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.9:9999"
	req.Header.Set("X-API-Key", "api-123")
	if got := keyFunc(req); got != "api-123" {
		t.Fatalf("key = %q, want %q", got, "api-123")
	}

	// Without header — falls back to remote addr
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.9:9999"
	if got := keyFunc(req); got != "198.51.100.9" {
		t.Fatalf("key = %q, want %q", got, "198.51.100.9")
	}
}
