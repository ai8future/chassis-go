package meilikit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MeiliError represents an error returned by the Meilisearch API.
type MeiliError struct {
	Message    string `json:"message"`
	Code       string `json:"code"`
	Type       string `json:"type"`
	Link       string `json:"link"`
	StatusCode int    `json:"-"`
}

func (e *MeiliError) Error() string {
	return fmt.Sprintf("meilikit: %s (code=%s, status=%d)", e.Message, e.Code, e.StatusCode)
}

// maxErrorBody caps how much of an error response we read (64 KB).
const maxErrorBody = 64 << 10

// decodeMeiliError reads and decodes a Meilisearch error response.
func decodeMeiliError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var me MeiliError
	if err := json.Unmarshal(body, &me); err != nil {
		return fmt.Errorf("meilikit: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	me.StatusCode = resp.StatusCode
	return &me
}
