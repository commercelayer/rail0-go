package rail0

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// HealthService exposes the gateway liveness/readiness check (public, no auth).
type HealthService struct {
	http *httpClient
}

// HealthResponse is returned by GET /health.
//
// The endpoint is public and TOKEN-BLIND: every caller gets the same body, and a
// token on the client changes nothing. It used to be tiered, with the Sidekiq
// worker-fleet figures behind any valid session; the gateway moved that detail to
// the operator-only GET /admin/health (#289), so the Sidekiq field this type
// carried could only ever decode as nil.
type HealthResponse struct {
	// Status is the global signal: "ok", "degraded" (database fine, workers not —
	// the only thing that produces it) or "error" (database down — the only case
	// that also returns HTTP 503).
	Status          string `json:"status"`
	APIVersion      string `json:"api_version"`
	ContractVersion string `json:"contract_version"`
	DB              string `json:"db"`
	ActiveChains    int    `json:"active_chains"`
	ActiveContracts int    `json:"active_contracts"`
	Timestamp       string `json:"timestamp"`
}

// Get reports gateway health, including database connectivity. Callable without a
// token; the body is the same with or without one.
//
// A 503 is still an answer: the gateway sends it, with the full health report, exactly
// when its database is down — the one reading a caller most needs to see. So on a 503
// whose body is a health report, Get returns BOTH that report and the *APIError: the
// error keeps "unhealthy" an error (a caller checking err alone behaves as before), and
// the report says what is wrong. It used to return only the error, whose detail was the
// bare "HTTP 503", and the report was thrown away.
func (s *HealthService) Get(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	err := s.http.get(ctx, "/health", &out)
	if err == nil {
		return &out, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusServiceUnavailable {
		var report HealthResponse
		if json.Unmarshal(apiErr.Body, &report) == nil && report.Status != "" {
			return &report, err
		}
	}
	return nil, err
}
