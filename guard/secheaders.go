package guard

import (
	"fmt"
	"net/http"

	chassis "github.com/ai8future/chassis-go/v5"
)

// HSTSConfig configures the Strict-Transport-Security header.
type HSTSConfig struct {
	MaxAge            int  // max-age in seconds
	IncludeSubDomains bool // include subdomains directive
	Preload           bool // preload directive
}

// SecurityHeadersConfig configures the security headers middleware.
type SecurityHeadersConfig struct {
	ContentSecurityPolicy   string     // Content-Security-Policy header value
	XContentTypeOptions     string     // X-Content-Type-Options header value
	XFrameOptions           string     // X-Frame-Options header value
	ReferrerPolicy          string     // Referrer-Policy header value
	PermissionsPolicy       string     // Permissions-Policy header value
	HSTS                    HSTSConfig // Strict-Transport-Security config
	CrossOriginOpenerPolicy string     // Cross-Origin-Opener-Policy header value
}

// DefaultSecurityHeaders provides secure defaults for all security headers.
var DefaultSecurityHeaders = SecurityHeadersConfig{
	ContentSecurityPolicy:   "default-src 'self'",
	XContentTypeOptions:     "nosniff",
	XFrameOptions:           "DENY",
	ReferrerPolicy:          "strict-origin-when-cross-origin",
	PermissionsPolicy:       "geolocation=(), camera=(), microphone=()",
	HSTS:                    HSTSConfig{MaxAge: 63072000, IncludeSubDomains: true, Preload: true},
	CrossOriginOpenerPolicy: "same-origin",
}

// SecurityHeaders returns middleware that sets security-related HTTP headers
// before calling the next handler.
func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()

	// Pre-compute HSTS value.
	var hstsValue string
	if cfg.HSTS.MaxAge > 0 {
		hstsValue = fmt.Sprintf("max-age=%d", cfg.HSTS.MaxAge)
		if cfg.HSTS.IncludeSubDomains {
			hstsValue += "; includeSubDomains"
		}
		if cfg.HSTS.Preload {
			hstsValue += "; preload"
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.ContentSecurityPolicy != "" {
				w.Header().Set("Content-Security-Policy", cfg.ContentSecurityPolicy)
			}
			if cfg.XContentTypeOptions != "" {
				w.Header().Set("X-Content-Type-Options", cfg.XContentTypeOptions)
			}
			if cfg.XFrameOptions != "" {
				w.Header().Set("X-Frame-Options", cfg.XFrameOptions)
			}
			if cfg.ReferrerPolicy != "" {
				w.Header().Set("Referrer-Policy", cfg.ReferrerPolicy)
			}
			if cfg.PermissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			}
			if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
				w.Header().Set("Strict-Transport-Security", hstsValue)
			}
			if cfg.CrossOriginOpenerPolicy != "" {
				w.Header().Set("Cross-Origin-Opener-Policy", cfg.CrossOriginOpenerPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}
