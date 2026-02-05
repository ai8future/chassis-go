package guard

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ai8future/chassis-go/errors"
)

// writeProblem writes an RFC 9457 Problem Details JSON response for the given
// ServiceError. It is the shared helper used by all guard middleware.
func writeProblem(w http.ResponseWriter, r *http.Request, svcErr *errors.ServiceError) {
	pd := svcErr.ProblemDetail(r)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(svcErr.HTTPCode)
	if err := json.NewEncoder(w).Encode(pd); err != nil {
		slog.ErrorContext(r.Context(), "guard: failed to encode problem detail", "error", err)
	}
}
