package errors

import "net/http"

const typeBaseURI = "https://chassis.ai8future.com/errors/"

var typeURIs = map[int]string{
	http.StatusBadRequest:          typeBaseURI + "validation",
	http.StatusNotFound:            typeBaseURI + "not-found",
	http.StatusUnauthorized:        typeBaseURI + "unauthorized",
	http.StatusGatewayTimeout:      typeBaseURI + "timeout",
	http.StatusTooManyRequests:     typeBaseURI + "rate-limit",
	http.StatusServiceUnavailable:  typeBaseURI + "dependency",
	http.StatusInternalServerError: typeBaseURI + "internal",
}

var titleMap = map[int]string{
	http.StatusBadRequest:          "Validation Error",
	http.StatusNotFound:            "Not Found",
	http.StatusUnauthorized:        "Unauthorized",
	http.StatusGatewayTimeout:      "Timeout",
	http.StatusTooManyRequests:     "Rate Limit Exceeded",
	http.StatusServiceUnavailable:  "Dependency Error",
	http.StatusInternalServerError: "Internal Error",
}

// ProblemDetail represents an RFC 9457 Problem Details object.
type ProblemDetail struct {
	Type       string            `json:"type"`
	Title      string            `json:"title"`
	Status     int               `json:"status"`
	Detail     string            `json:"detail"`
	Instance   string            `json:"instance"`
	Extensions map[string]string `json:"extensions,omitempty"`
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
	pd := ProblemDetail{
		Type:     typeURI,
		Title:    title,
		Status:   e.HTTPCode,
		Detail:   e.Message,
		Instance: r.URL.Path,
	}
	if len(e.Details) > 0 {
		pd.Extensions = make(map[string]string, len(e.Details))
		for k, v := range e.Details {
			pd.Extensions[k] = v
		}
	}
	return pd
}
