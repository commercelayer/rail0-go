package rail0

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// WebhooksService exposes webhook subscription management (requires JWT).
type WebhooksService struct {
	http *httpClient
}

// Webhook topics. A webhook subscribes to exactly one topic.
const (
	TopicPaymentsCreated    = "payments.created"
	TopicPaymentsSigned     = "payments.signed"
	TopicPaymentsAuthorized = "payments.authorized"
	TopicPaymentsCharged    = "payments.charged"
	TopicPaymentsCaptured   = "payments.captured"
	TopicPaymentsVoided     = "payments.voided"
	TopicPaymentsReleased   = "payments.released"
	TopicPaymentsRefunded   = "payments.refunded"
	TopicPaymentsFailed     = "payments.failed"
)

// Webhook is a webhook subscription. SharedSecret is populated only on the
// create and rotate_secret responses; it is omitted on every other endpoint.
type Webhook struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	CallbackURL         string `json:"callback_url"`
	Topic               string `json:"topic"`
	Active              bool   `json:"active"`
	CircuitState        string `json:"circuit_state"`
	CircuitFailureCount int    `json:"circuit_failure_count"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	// SharedSecret is returned only on create and rotate_secret.
	SharedSecret string `json:"shared_secret,omitempty"`
}

// EventCallback is a single webhook delivery attempt.
type EventCallback struct {
	ID              string `json:"id"`
	WebhookID       string `json:"webhook_id"`
	PaymentID       string `json:"payment_id"`
	Topic           string `json:"topic"`
	CallbackURL     string `json:"callback_url"`
	ResponseCode    *int   `json:"response_code,omitempty"`
	ResponseMessage string `json:"response_message,omitempty"`
	ErrorReason     string `json:"error_reason,omitempty"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
}

// ListWebhooksParams holds optional query parameters for WebhooksService.List.
type ListWebhooksParams struct {
	Topic        string // filter by topic
	Active       *bool  // filter by active flag; nil = no filter
	CircuitState string // filter by circuit state ("closed" or "open")
	Sort         string // sort fields: comma-separated, prefix with - for desc
	Page         int    // page number (1-based); 0 = use server default
	PerPage      int    // items per page; 0 = use server default
}

// List returns the account's webhooks (requires JWT).
func (s *WebhooksService) List(ctx context.Context, params ...ListWebhooksParams) (*PaginatedResponse[Webhook], error) {
	var out PaginatedResponse[Webhook]
	path := "/webhooks"
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Topic != "" {
			q.Set("topic", p.Topic)
		}
		if p.Active != nil {
			q.Set("active", strconv.FormatBool(*p.Active))
		}
		if p.CircuitState != "" {
			q.Set("circuit_state", p.CircuitState)
		}
		if p.Sort != "" {
			q.Set("sort", p.Sort)
		}
		if p.Page > 0 {
			q.Set("page", strconv.Itoa(p.Page))
		}
		if p.PerPage > 0 {
			q.Set("per_page", strconv.Itoa(p.PerPage))
		}
		if len(q) > 0 {
			path = path + "?" + q.Encode()
		}
	}
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWebhookRequest is the body for WebhooksService.Create.
type CreateWebhookRequest struct {
	Name        string `json:"name"`
	CallbackURL string `json:"callback_url"`
	Topic       string `json:"topic"`
}

// Create registers a new webhook (requires JWT). The response includes the
// one-time SharedSecret used to verify delivery signatures.
func (s *WebhooksService) Create(ctx context.Context, req CreateWebhookRequest) (*Webhook, error) {
	var out Webhook
	if err := s.http.post(ctx, "/webhooks", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get fetches a single webhook (requires JWT).
func (s *WebhooksService) Get(ctx context.Context, id string) (*Webhook, error) {
	var out Webhook
	if err := s.http.get(ctx, "/webhooks/"+id, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWebhookRequest holds the fields that can be patched on a webhook.
type UpdateWebhookRequest struct {
	Name        string `json:"name,omitempty"`
	CallbackURL string `json:"callback_url,omitempty"`
	Topic       string `json:"topic,omitempty"`
}

// Update patches a webhook (requires JWT).
func (s *WebhooksService) Update(ctx context.Context, id string, req UpdateWebhookRequest) (*Webhook, error) {
	var out Webhook
	if err := s.http.patch(ctx, "/webhooks/"+id, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Enable re-enables a disabled webhook (requires JWT).
func (s *WebhooksService) Enable(ctx context.Context, id string) (*Webhook, error) {
	return s.putAction(ctx, id, "enable")
}

// Disable disables a webhook (requires JWT).
func (s *WebhooksService) Disable(ctx context.Context, id string) (*Webhook, error) {
	return s.putAction(ctx, id, "disable")
}

// RotateSecret generates a new shared secret and returns it once on the response
// (requires JWT).
func (s *WebhooksService) RotateSecret(ctx context.Context, id string) (*Webhook, error) {
	return s.putAction(ctx, id, "rotate_secret")
}

// ResetCircuit resets the delivery circuit breaker and re-enables the webhook
// (requires JWT).
func (s *WebhooksService) ResetCircuit(ctx context.Context, id string) (*Webhook, error) {
	return s.putAction(ctx, id, "reset_circuit")
}

// putAction issues PUT /webhooks/{id}/{action} for the no-body state-transition
// endpoints (enable, disable, rotate_secret, reset_circuit).
func (s *WebhooksService) putAction(ctx context.Context, id, action string) (*Webhook, error) {
	var out Webhook
	if err := s.http.put(ctx, fmt.Sprintf("/webhooks/%s/%s", id, action), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEventCallbacksParams holds optional query parameters for EventCallbacks.
type ListEventCallbacksParams struct {
	Status  string // filter by delivery status ("pending", "delivered", "failed")
	Sort    string // sort fields: comma-separated, prefix with - for desc
	Page    int    // page number (1-based); 0 = use server default
	PerPage int    // items per page; 0 = use server default
}

// EventCallbacks lists delivery attempts for a webhook (requires JWT).
func (s *WebhooksService) EventCallbacks(ctx context.Context, id string, params ...ListEventCallbacksParams) (*PaginatedResponse[EventCallback], error) {
	var out PaginatedResponse[EventCallback]
	path := fmt.Sprintf("/webhooks/%s/event_callbacks", id)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Status != "" {
			q.Set("status", p.Status)
		}
		if p.Sort != "" {
			q.Set("sort", p.Sort)
		}
		if p.Page > 0 {
			q.Set("page", strconv.Itoa(p.Page))
		}
		if p.PerPage > 0 {
			q.Set("per_page", strconv.Itoa(p.PerPage))
		}
		if len(q) > 0 {
			path = path + "?" + q.Encode()
		}
	}
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a webhook (requires JWT). Returns 204 No Content.
func (s *WebhooksService) Delete(ctx context.Context, id string) error {
	return s.http.delete(ctx, "/webhooks/"+id, nil)
}
