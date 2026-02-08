package guard

import (
	"net"
	"net/http"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/errors"
)

// IPFilterConfig configures the IP filter middleware.
type IPFilterConfig struct {
	Allow   []string // CIDR notation whitelist
	Deny    []string // CIDR notation blacklist (evaluated first)
	KeyFunc KeyFunc  // optional: custom IP extraction (e.g., XForwardedFor); defaults to RemoteAddr
}

// IPFilter returns middleware that filters requests by client IP address.
// Deny rules are evaluated first and take precedence over Allow rules.
// When only Allow is set, all non-matching IPs are rejected.
// When only Deny is set, all non-matching IPs are allowed.
// Panics if both Allow and Deny are empty, or if any CIDR is invalid.
func IPFilter(cfg IPFilterConfig) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	if len(cfg.Allow) == 0 && len(cfg.Deny) == 0 {
		panic("guard: IPFilterConfig must have at least one Allow or Deny entry")
	}

	allowNets := parseCIDRs(cfg.Allow)
	denyNets := parseCIDRs(cfg.Deny)

	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = RemoteAddr()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := keyFunc(r)
			ip := net.ParseIP(host)
			if ip == nil {
				writeProblem(w, r, errors.ForbiddenError("access denied"))
				return
			}

			// Deny takes precedence.
			for _, n := range denyNets {
				if n.Contains(ip) {
					writeProblem(w, r, errors.ForbiddenError("access denied"))
					return
				}
			}

			// If Allow rules exist, IP must match at least one.
			if len(allowNets) > 0 {
				allowed := false
				for _, n := range allowNets {
					if n.Contains(ip) {
						allowed = true
						break
					}
				}
				if !allowed {
					writeProblem(w, r, errors.ForbiddenError("access denied"))
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// parseCIDRs parses CIDR strings and panics on invalid entries.
func parseCIDRs(cidrs []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("guard: invalid CIDR: " + cidr + ": " + err.Error())
		}
		nets = append(nets, n)
	}
	return nets
}
