package rail0

import "context"

// HealthService exposes the gateway liveness/readiness check (public, no auth).
type HealthService struct {
	http *httpClient
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
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
func (s *HealthService) Get(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := s.http.get(ctx, "/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
