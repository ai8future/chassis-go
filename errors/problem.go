package errors

import (
	"encoding/json"
	"net/http"
)

const typeBaseURI = "https://chassis.ai8future.com/errors/"

var typeURIs = map[int]string{
	http.StatusBadRequest:            typeBaseURI + "validation",
	http.StatusNotFound:              typeBaseURI + "not-found",
	http.StatusUnauthorized:          typeBaseURI + "unauthorized",
	http.StatusGatewayTimeout:        typeBaseURI + "timeout",
	http.StatusRequestEntityTooLarge: typeBaseURI + "payload-too-large",
	http.StatusTooManyRequests:       typeBaseURI + "rate-limit",
	http.StatusServiceUnavailable:    typeBaseURI + "dependency",
	http.StatusInternalServerError:   typeBaseURI + "internal",
}

var titleMap = map[int]string{
	http.StatusBadRequest:            "Validation Error",
	http.StatusNotFound:              "Not Found",
	http.StatusUnauthorized:          "Unauthorized",
	http.StatusGatewayTimeout:        "Timeout",
	http.StatusRequestEntityTooLarge: "Payload Too Large",
	http.StatusTooManyRequests:       "Rate Limit Exceeded",
	http.StatusServiceUnavailable:    "Dependency Error",
	http.StatusInternalServerError:   "Internal Error",
}

// ProblemDetail represents an RFC 9457 Problem Details object.
// Extension members are serialized as top-level fields per the RFC spec.
type ProblemDetail struct {
	Type       string            `json:"type"`
	Title      string            `json:"title"`
	Status     int               `json:"status"`
	Detail     string            `json:"detail"`
	Instance   string            `json:"instance,omitempty"`
	Extensions map[string]string `json:"-"` // serialized as top-level members
}

// MarshalJSON implements custom serialization to place extension members at
// the top level of the JSON object, as required by RFC 9457.
func (pd ProblemDetail) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"type":   pd.Type,
		"title":  pd.Title,
		"status": pd.Status,
		"detail": pd.Detail,
	}
	if pd.Instance != "" {
		m["instance"] = pd.Instance
	}
	for k, v := range pd.Extensions {
		switch k {
		case "type", "title", "status", "detail", "instance":
			continue // skip reserved RFC 9457 fields
		}
		m[k] = v
	}
	return json.Marshal(m)
}

// ProblemDetail converts this ServiceError into an RFC 9457 ProblemDetail,
// using the request to populate the Instance field.
func (e *ServiceError) ProblemDetail(r *http.Request) ProblemDetail {
	typeURI, ok := typeURIs[e.HTTPCode]
	if !ok {
		typeURI = typeBaseURI + "unknown"
	}
	if e.typeURI != "" {
		typeURI = e.typeURI
	}
	title, ok := titleMap[e.HTTPCode]
	if !ok {
		title = http.StatusText(e.HTTPCode)
	}
	var instance string
	if r != nil && r.URL != nil {
		instance = r.URL.Path
	}
	pd := ProblemDetail{
		Type:     typeURI,
		Title:    title,
		Status:   e.HTTPCode,
		Detail:   e.Message,
		Instance: instance,
	}
	if len(e.Details) > 0 {
		pd.Extensions = make(map[string]string, len(e.Details))
		for k, v := range e.Details {
			pd.Extensions[k] = v
		}
	}
	return pd
}
