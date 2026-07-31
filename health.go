package rail0

import "context"

// HealthService exposes the gateway liveness/readiness check (public, no auth).
type HealthService struct {
	http *httpClient
}

// HealthResponse is returned by GET /health.
//
// Only Status, DB and Timestamp are always present: the endpoint is public (so a
// platform liveness probe can call it token-less) but the DETAIL is session-gated.
// APIVersion, ContractVersion, ActiveChains, ActiveContracts and Sidekiq are
// returned ONLY to an authenticated caller — set a token on the client first, or
// they come back zero-valued, which is indistinguishable from "not reported".
type HealthResponse struct {
	// Status is the global signal: "ok", "degraded" (database fine, workers not)
	// or "error" (database down — the only case that also returns HTTP 503).
	Status          string `json:"status"`
	APIVersion      string `json:"api_version"`
	ContractVersion string `json:"contract_version"`
	DB              string `json:"db"`
	ActiveChains    int    `json:"active_chains"`
	ActiveContracts int    `json:"active_contracts"`
	// Sidekiq reports the worker fleet; nil for an unauthenticated caller. It is
	// the only way to learn WHY Status came back "degraded".
	Sidekiq   *SidekiqStatus `json:"sidekiq"`
	Timestamp string         `json:"timestamp"`
}

// SidekiqStatus is the worker-fleet section of HealthResponse (authenticated
// callers only). A degraded Sidekiq is reported but never gates liveness: the API
// still serves synchronous requests with the workers down, jobs simply queue.
type SidekiqStatus struct {
	// Status is "ok" (Redis up, at least one live worker), "degraded" (Redis up but
	// no live worker, or the stats read failed) or "down" (Redis unreachable).
	Status string `json:"status"`
	// Redis is "ok" or "error".
	Redis string `json:"redis"`
	// Processes is the live Sidekiq process count. Absent (zero) when Redis is down.
	Processes int `json:"processes"`
	// Enqueued is the total backlog across queues. Absent (zero) when Redis is down.
	Enqueued int `json:"enqueued"`
}

// Get reports gateway health, including database connectivity. The gateway
// returns HTTP 503 (surfaced as *APIError) when the database is unreachable.
// Callable without a token, but the detail fields listed on HealthResponse are
// filled in only when the client carries one.
func (s *HealthService) Get(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := s.http.get(ctx, "/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
