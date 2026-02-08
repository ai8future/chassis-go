package guard

import (
	"net/http"

	"github.com/ai8future/chassis-go/v5/errors"
)

// writeProblem writes an RFC 9457 Problem Details JSON response for the given
// ServiceError. It is the shared helper used by all guard middleware.
func writeProblem(w http.ResponseWriter, r *http.Request, svcErr *errors.ServiceError) {
	errors.WriteProblem(w, r, svcErr, "")
}
