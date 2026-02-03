// Package httpkit provides standard HTTP middleware and response utilities.
package httpkit

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the standard JSON error body returned by JSONError.
type ErrorResponse struct {
	Error      string `json:"error"`
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id,omitempty"`
}

// JSONError writes a JSON error response with the given status code and message.
// If a request ID is present in the request context, it is included in the response body.
func JSONError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	resp := ErrorResponse{
		Error:      message,
		StatusCode: statusCode,
		RequestID:  RequestIDFrom(r.Context()),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	_ = json.NewEncoder(w).Encode(resp)
}
