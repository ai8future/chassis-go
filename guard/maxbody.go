package guard

import (
	"net/http"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/errors"
)

// MaxBody returns middleware that rejects requests with a body exceeding
// maxBytes with 413 Payload Too Large.
func MaxBody(maxBytes int64) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeProblem(w, r, errors.PayloadTooLargeError("request body too large"))
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
