package guard

import (
	"net"
	"net/http"
	"strings"
)

// KeyFunc extracts a rate limit key from an HTTP request.
type KeyFunc func(r *http.Request) string

// RemoteAddr returns a KeyFunc that uses the request's RemoteAddr (without port).
func RemoteAddr() KeyFunc {
	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}

// XForwardedFor returns a KeyFunc that reads the client IP from X-Forwarded-For,
// but only if RemoteAddr is within a trusted CIDR range. Falls back to RemoteAddr
// if untrusted.
func XForwardedFor(trustedCIDRs ...string) KeyFunc {
	var nets []*net.IPNet
	for _, cidr := range trustedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return func(r *http.Request) string {
		remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
		remoteIP := net.ParseIP(remoteHost)
		trusted := false
		if remoteIP != nil {
			for _, n := range nets {
				if n.Contains(remoteIP) {
					trusted = true
					break
				}
			}
		}
		if trusted {
			xff := r.Header.Get("X-Forwarded-For")
			if xff != "" {
				parts := strings.SplitN(xff, ",", 2)
				clientIP := strings.TrimSpace(parts[0])
				if clientIP != "" {
					return clientIP
				}
			}
		}
		return remoteHost
	}
}

// HeaderKey returns a KeyFunc using the value of a request header as the key.
func HeaderKey(header string) KeyFunc {
	return func(r *http.Request) string {
		v := r.Header.Get(header)
		if v == "" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			return host
		}
		return v
	}
}
