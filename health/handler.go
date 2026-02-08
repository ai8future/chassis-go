package health

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"

	chassis "github.com/ai8future/chassis-go/v5"
)

// response is the JSON envelope returned by the health handler.
type response struct {
	Status string   `json:"status"`
	Checks []Result `json:"checks"`
}

// Handler returns an http.Handler that runs all registered checks via All.
// It responds with 200 when every check passes and 503 when any check fails.
// The response body is JSON: {"status":"healthy"/"unhealthy","checks":[...]}.
func Handler(checks map[string]Check) http.Handler {
	chassis.AssertVersionChecked()
	run := All(checks)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results, err := run(r.Context())

		status := "healthy"
		code := http.StatusOK
		if err != nil {
			status = "unhealthy"
			code = http.StatusServiceUnavailable
		}

		var buf bytes.Buffer
		if encErr := json.NewEncoder(&buf).Encode(response{
			Status: status,
			Checks: results,
		}); encErr != nil {
			slog.ErrorContext(r.Context(), "health: failed to encode response", "error", encErr)
			http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(buf.Bytes())
	})
}
