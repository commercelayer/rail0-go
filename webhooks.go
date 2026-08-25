package rail0

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	// The escrow came back because a never-captured authorization's window lapsed — no
	// attempt involved, which is why it is its own topic and not a flavour of failed.
	TopicPaymentsExpired       = "payments.expired"
	TopicPaymentsFailed        = "payments.failed"
	TopicPaymentsDisputed      = "payments.disputed"
	TopicPaymentsDisputeClosed = "payments.dispute_closed"
)

// Webhook (the webhook subscription shape, including the SharedSecret returned
// only on create and rotate_secret) and EventCallback (a single delivery
// attempt) are generated from the gateway OpenAPI schema in types_gen.go.

// Headers carried by every webhook delivery.
const (
	HeaderWebhookTopic     = "X-Rail0-Topic"
	HeaderWebhookTimestamp = "X-Rail0-Timestamp"
	HeaderWebhookSignature = "X-Rail0-Signature"
)

// WebhookToleranceSecs is the accepted clock skew, in seconds, between the
// delivery's X-Rail0-Timestamp and now — applied symmetrically (a timestamp too
// far in the FUTURE is rejected too, since a clock an attacker controls is not
// evidence of freshness). Matches the gateway's own ±5 minutes.
const WebhookToleranceSecs = 300

// VerifyWebhookSignature reports whether a webhook delivery is authentic: an
// HMAC-SHA256 over "{timestamp}.{body}" keyed with the webhook's shared secret,
// compared in constant time, plus a timestamp inside WebhookToleranceSecs.
//
// secret is the one-time SharedSecret from Create/RotateSecret; body is the RAW
// request body (verify before any JSON re-parse — re-serialising changes the bytes
// and invalidates the digest); timestamp and signature are the X-Rail0-Timestamp
// and X-Rail0-Signature header values verbatim.
//
//	body, _ := io.ReadAll(r.Body)
//	if !rail0.VerifyWebhookSignature(secret, string(body),
//	    r.Header.Get(rail0.HeaderWebhookTimestamp), r.Header.Get(rail0.HeaderWebhookSignature)) {
//	    http.Error(w, "invalid signature", http.StatusUnauthorized)
//	    return
//	}
//
// The timestamp is what bounds a replay: it is inside the signed preimage, so it
// cannot be edited, and refusing a stale one is what stops a captured delivery
// from being replayed forever. This requires a gateway that sends the timestamped
// signature (rail0-gateway#176, from 2026-07-31); against an older gateway, which
// signed the body alone with no X-Rail0-Timestamp, every delivery fails here.
func VerifyWebhookSignature(secret, body, timestamp, signature string) bool {
	// A blank secret would make the digest trivially forgeable (anyone can compute
	// HMAC("", …)), so treat it as a failure rather than fail open.
	if secret == "" || timestamp == "" || signature == "" {
		return false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	if drift := time.Now().Unix() - ts; drift > WebhookToleranceSecs || drift < -WebhookToleranceSecs {
		return false
	}
	// Decoding the candidate (rather than hex-encoding ours) keeps the comparison on
	// raw bytes: hmac.Equal is constant time, and hex case never matters.
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	// The timestamp is signed exactly as it arrived on the wire — the gateway keys
	// the digest with that same string, so a normalised copy would not match.
	mac.Write([]byte(timestamp + "." + body))
	return hmac.Equal(mac.Sum(nil), got)
}

// ListWebhooksParams holds optional query parameters for WebhooksService.List.
type ListWebhooksParams struct {
	Topic        string // filter to subscriptions that include this topic
	Active       *bool  // filter by active flag; nil = no filter
	CircuitState string // filter by circuit state ("closed" or "open")
	Sort         string // sort fields: comma-separated, prefix with - for desc
	Page         int    // page number (1-based); 0 = use server default
	PerPage      int    // items per page; 0 = use server default
}

// List returns the account's webhooks (requires JWT).
func (s *WebhooksService) List(ctx context.Context, params ...ListWebhooksParams) (*PaginatedResponse[Webhook], error) {
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
	// Bare JSON array + pagination headers (not a {data,meta} envelope).
	var data []Webhook
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[Webhook]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

// CreateWebhookRequest is the body for WebhooksService.Create.
//
// One subscription covers a SET of topics, and that is the point: four events on one
// endpoint used to mean four subscriptions, so four shared secrets to verify against and
// four rotations to coordinate. Now it is one secret and one circuit breaker.
//
// Two subscriptions for the same CallbackURL must not overlap — one holding
// TopicPaymentsAuthorized and another holding it alongside two more would deliver every
// authorization twice, signed with two different keys — so the gateway answers 409 and
// names the topic that collided.
type CreateWebhookRequest struct {
	Name        string   `json:"name"`
	CallbackURL string   `json:"callback_url"`
	Topics      []string `json:"topics"`
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
//
// Topics REPLACES the whole set rather than adding to it — which is the only version that
// can also remove one — so send the union to add and the remainder to drop. The shared
// secret is untouched: changing what an endpoint receives is not a reason to re-key it.
type UpdateWebhookRequest struct {
	Name        string   `json:"name,omitempty"`
	CallbackURL string   `json:"callback_url,omitempty"`
	Topics      []string `json:"topics,omitempty"`
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
	Status    string // filter by delivery status ("pending", "delivered", "failed")
	Topic     string // filter by event topic
	PaymentID string // filter by the payment the delivery is for
	Since     string // only deliveries at/after this ISO-8601 time
	Until     string // only deliveries at/before this ISO-8601 time
	Sort      string // sort fields: comma-separated, prefix with - for desc
	Page      int    // page number (1-based); 0 = use server default
	PerPage   int    // items per page; 0 = use server default
}

// EventCallbacks lists delivery attempts for a webhook (requires JWT).
func (s *WebhooksService) EventCallbacks(ctx context.Context, id string, params ...ListEventCallbacksParams) (*PaginatedResponse[EventCallback], error) {
	path := fmt.Sprintf("/webhooks/%s/event_callbacks", id)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Status != "" {
			q.Set("status", p.Status)
		}
		if p.Topic != "" {
			q.Set("topic", p.Topic)
		}
		if p.PaymentID != "" {
			q.Set("payment_id", p.PaymentID)
		}
		if p.Since != "" {
			q.Set("since", p.Since)
		}
		if p.Until != "" {
			q.Set("until", p.Until)
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
	// Bare JSON array + pagination headers (not a {data,meta} envelope).
	var data []EventCallback
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[EventCallback]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

// Delete removes a webhook (requires JWT). Returns 204 No Content.
func (s *WebhooksService) Delete(ctx context.Context, id string) error {
	return s.http.delete(ctx, "/webhooks/"+id, nil)
}
