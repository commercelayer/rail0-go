package rail0

import "context"

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

// Get reports gateway health, including database connectivity. The gateway
// returns HTTP 503 (surfaced as *APIError) when the database is unreachable.
// Callable without a token; the body is the same with or without one.
func (s *HealthService) Get(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := s.http.get(ctx, "/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
