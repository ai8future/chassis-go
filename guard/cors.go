package guard

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
)

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	AllowOrigins     []string      // REQUIRED: list of allowed origins, or ["*"] for wildcard
	AllowMethods     []string      // defaults to GET, POST, HEAD
	AllowHeaders     []string      // defaults to Origin, Content-Type, Accept
	MaxAge           time.Duration // preflight cache duration
	AllowCredentials bool          // sets Access-Control-Allow-Credentials: true
}

// CORS returns middleware that handles Cross-Origin Resource Sharing.
// It responds to OPTIONS preflight requests with 204 and sets appropriate
// CORS headers on matching-origin requests.
// Panics if AllowOrigins is empty or if AllowCredentials is used with wildcard origin.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	if len(cfg.AllowOrigins) == 0 {
		panic("guard: CORSConfig.AllowOrigins must not be empty")
	}
	if cfg.AllowCredentials {
		for _, o := range cfg.AllowOrigins {
			if o == "*" {
				panic("guard: CORSConfig.AllowCredentials cannot be used with wildcard origin \"*\"")
			}
		}
	}
	if len(cfg.AllowMethods) == 0 {
		cfg.AllowMethods = []string{"GET", "POST", "HEAD"}
	}
	if len(cfg.AllowHeaders) == 0 {
		cfg.AllowHeaders = []string{"Origin", "Content-Type", "Accept"}
	}

	// Pre-compute joined strings.
	methodsStr := strings.Join(cfg.AllowMethods, ", ")
	headersStr := strings.Join(cfg.AllowHeaders, ", ")
	var maxAgeStr string
	if cfg.MaxAge > 0 {
		maxAgeStr = strconv.Itoa(int(cfg.MaxAge.Seconds()))
	}

	// Build origin set for fast lookup.
	wildcard := false
	origins := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			wildcard = true
		}
		origins[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Not a CORS request — pass through.
				next.ServeHTTP(w, r)
				return
			}

			// Check if origin matches.
			allowed := wildcard
			if !allowed {
				_, allowed = origins[origin]
			}
			if !allowed {
				// Origin not allowed — pass through without CORS headers.
				next.ServeHTTP(w, r)
				return
			}

			// Set CORS headers.
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}

			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			// Handle preflight.
			if r.Method == http.MethodOptions {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Methods", methodsStr)
				w.Header().Set("Access-Control-Allow-Headers", headersStr)
				if maxAgeStr != "" {
					w.Header().Set("Access-Control-Max-Age", maxAgeStr)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
