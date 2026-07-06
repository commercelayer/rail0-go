package rail0

import "fmt"

// APIError is returned when the RAIL0 API responds with a non-2xx status code.
type APIError struct {
	// Status is the HTTP status code (e.g. 404, 409, 422).
	Status int
	// Code is the machine-readable error identifier, e.g. "PaymentNotFound".
	Code string
	// Message is the human-readable description.
	Message string
	// Reason is an optional sub-cause the gateway attaches to some errors
	// (e.g. "rpc_unreachable" on a 502 signing_payload_unavailable); "" if absent.
	Reason string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("rail0 error (HTTP %d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("rail0 %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// firstNonEmpty returns the first argument that isn't the empty string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
