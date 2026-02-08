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
// but only if RemoteAddr is within a trusted CIDR range. It walks the
// X-Forwarded-For chain from right to left, returning the rightmost IP that is
// NOT in the trusted CIDRs — this is the last hop before entering the trusted
// proxy chain and is resistant to client-side header spoofing.
//
// Falls back to RemoteAddr if untrusted, if X-Forwarded-For is absent, or if
// no valid non-trusted IP is found.
func XForwardedFor(trustedCIDRs ...string) KeyFunc {
	var nets []*net.IPNet
	for _, cidr := range trustedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("guard: XForwardedFor: invalid trusted CIDR: " + cidr + ": " + err.Error())
		}
		nets = append(nets, n)
	}

	isTrusted := func(ip net.IP) bool {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}

	return func(r *http.Request) string {
		host := remoteHost(r)
		remoteIP := net.ParseIP(host)
		if remoteIP == nil || !isTrusted(remoteIP) {
			return host
		}

		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			return host
		}

		// Walk right-to-left: the rightmost non-trusted IP is the real client.
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			ip := net.ParseIP(candidate)
			if ip == nil {
				continue
			}
			if !isTrusted(ip) {
				return candidate
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
