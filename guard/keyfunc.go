package guard

import (
	"net"
	"net/http"
	"strings"
)

// KeyFunc extracts a rate limit key from an HTTP request.
type KeyFunc func(r *http.Request) string

// remoteHost extracts the host portion of r.RemoteAddr, falling back to the
// full RemoteAddr if SplitHostPort fails.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RemoteAddr returns a KeyFunc that uses the request's RemoteAddr (without port).
func RemoteAddr() KeyFunc {
	return func(r *http.Request) string {
		return remoteHost(r)
	}
}

// XForwardedFor returns a KeyFunc that reads the client IP from X-Forwarded-For,
// but only if RemoteAddr is within a trusted CIDR range. Falls back to RemoteAddr
// if untrusted or if the X-Forwarded-For value is not a valid IP address.
func XForwardedFor(trustedCIDRs ...string) KeyFunc {
	var nets []*net.IPNet
	for _, cidr := range trustedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return func(r *http.Request) string {
		host := remoteHost(r)
		remoteIP := net.ParseIP(host)
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
				if clientIP != "" && net.ParseIP(clientIP) != nil {
					return clientIP
				}
			}
		}
		return host
	}
}

// HeaderKey returns a KeyFunc using the value of a request header as the key.
// Falls back to RemoteAddr if the header is absent.
func HeaderKey(header string) KeyFunc {
	return func(r *http.Request) string {
		v := r.Header.Get(header)
		if v == "" {
			return remoteHost(r)
		}
		return v
	}
}
