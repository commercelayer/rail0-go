package rail0

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// PaymentsService implements the payment lifecycle operations.
type PaymentsService struct {
	http *httpClient
}

// Payment operations accepted by the prepare/submit endpoints.
const (
	OpAuthorize = "authorize"
	OpCapture   = "capture"
	OpCharge    = "charge"
	OpVoid      = "void"
	OpRelease   = "release"
	OpRefund    = "refund"
)

// ListPaymentsParams holds optional query parameters for Payments.List.
type ListPaymentsParams struct {
	Status      string // filter by payment status
	Mode        string // filter by mode ("authorize" or "charge")
	Payer       string // filter by payer address
	Payee       string // filter by payee address
	Token       string // filter by token address
	Rail0ID     string // filter by the logical on-chain payment id (0x…)
	ChainID     int    // filter by the payment's chain (resolved via its contract); 0 = no filter
	Disputed    *bool  // filter by whether an open dispute exists; nil = no filter
	MinAmount   string // minimum amount in token base units (inclusive)
	MaxAmount   string // maximum amount in token base units (inclusive)
	CreatedFrom string // only payments created at/after this ISO-8601 time
	CreatedTo   string // only payments created at/before this ISO-8601 time
	Sort        string // sort fields: comma-separated, prefix with - for desc
	Page        int    // page number (1-based); 0 = use server default
	PerPage     int    // items per page; 0 = use server default
}

// List returns payments for the authenticated account (requires JWT). The
// gateway returns a bare JSON array with pagination in response headers, so the
// data is decoded from the body and Meta is read from X-Total-Count / X-Page /
// X-Per-Page.
func (s *PaymentsService) List(ctx context.Context, params ...ListPaymentsParams) (*PaginatedResponse[Payment], error) {
	path := "/payments"
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Status != "" {
			q.Set("status", p.Status)
		}
		if p.Mode != "" {
			q.Set("mode", p.Mode)
		}
		if p.Payer != "" {
			q.Set("payer", p.Payer)
		}
		if p.Payee != "" {
			q.Set("payee", p.Payee)
		}
		if p.Token != "" {
			q.Set("token", p.Token)
		}
		if p.Rail0ID != "" {
			q.Set("rail0_id", p.Rail0ID)
		}
		if p.ChainID > 0 {
			q.Set("chain_id", strconv.Itoa(p.ChainID))
		}
		if p.Disputed != nil {
			q.Set("disputed", strconv.FormatBool(*p.Disputed))
		}
		if p.MinAmount != "" {
			q.Set("min_amount", p.MinAmount)
		}
		if p.MaxAmount != "" {
			q.Set("max_amount", p.MaxAmount)
		}
		if p.CreatedFrom != "" {
			q.Set("created_from", p.CreatedFrom)
		}
		if p.CreatedTo != "" {
			q.Set("created_to", p.CreatedTo)
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
	var data []Payment
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[Payment]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

// pageMetaFromHeaders reads the pagination meta the gateway returns as response
// headers on collection endpoints (bare-array body).
func pageMetaFromHeaders(h http.Header) PageMeta {
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	return PageMeta{
		Page:    atoi(h.Get("X-Page")),
		PerPage: atoi(h.Get("X-Per-Page")),
		Total:   atoi(h.Get("X-Total-Count")),
	}
}

// Create creates a payment (POST /payments). Pass an idempotencyKey to make the
// request replay-safe: a repeated call with the same key returns the existing
// payment (HTTP 200) instead of creating a new one. Pass "" to omit the header.
func (s *PaymentsService) Create(ctx context.Context, params CreatePaymentRequest, idempotencyKey ...string) (*Payment, error) {
	var out Payment
	if len(idempotencyKey) > 0 && idempotencyKey[0] != "" {
		headers := map[string]string{"Idempotency-Key": idempotencyKey[0]}
		if err := s.http.postWithHeaders(ctx, "/payments", params, &out, headers); err != nil {
			return nil, err
		}
		return &out, nil
	}
	if err := s.http.post(ctx, "/payments", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePayment is an alias for Create for backward compatibility.
func (s *PaymentsService) CreatePayment(ctx context.Context, params CreatePaymentRequest, idempotencyKey ...string) (*Payment, error) {
	return s.Create(ctx, params, idempotencyKey...)
}

// Get fetches the current payment state, including its embedded transactions.
func (s *PaymentsService) Get(ctx context.Context, paymentID Bytes32) (*Payment, error) {
	var out Payment
	if err := s.http.get(ctx, "/payments/"+paymentID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTransactionsParams holds optional query parameters for Payments.Transactions.
type ListTransactionsParams struct {
	Operation string // filter by operation type
	Status    string // filter by transaction status
	Sort      string // sort fields: comma-separated, prefix with - for desc
	Page      int    // page number (1-based); 0 = use server default
	PerPage   int    // items per page; 0 = use server default
}

// Transactions lists on-chain transactions for a payment. Like Payments.List,
// the gateway returns a bare JSON array with pagination in response headers.
func (s *PaymentsService) Transactions(ctx context.Context, paymentID Bytes32, params ...ListTransactionsParams) (*PaginatedResponse[Transaction], error) {
	path := fmt.Sprintf("/payments/%s/transactions", paymentID)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Operation != "" {
			q.Set("operation", p.Operation)
		}
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
	var data []Transaction
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[Transaction]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

// Sign deposits the payer's EIP-712 signature (PUT /payments/{id}/sign).
func (s *PaymentsService) Sign(ctx context.Context, paymentID Bytes32, params PayerSignatureRequest) (*Payment, error) {
	var out Payment
	if err := s.http.put(ctx, "/payments/"+paymentID+"/sign", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Prepare builds the unsigned transaction for an operation
// (POST /payments/{id}/{op}/prepare) and returns the pending Transaction.
//
// op must be one of the Op* constants. The optional req carries operation-
// specific fields: amount (capture, refund), signature (refund phase-2), and
// from (release). On the refund prepare call, omitting the signature returns a
// Transaction whose SigningPayload is populated (refund phase-1).
func (s *PaymentsService) Prepare(ctx context.Context, paymentID Bytes32, op string, req ...PrepareRequest) (*Transaction, error) {
	var out Transaction
	var body any
	if len(req) > 0 {
		body = req[0]
	}
	if err := s.http.post(ctx, fmt.Sprintf("/payments/%s/%s/prepare", paymentID, op), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Submit broadcasts a signed transaction for an operation
// (POST /payments/{id}/{op}); the gateway acknowledges with HTTP 202.
func (s *PaymentsService) Submit(ctx context.Context, paymentID Bytes32, op string, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, fmt.Sprintf("/payments/%s/%s", paymentID, op), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitByHash records a transaction the caller broadcast themselves
// (MetaMask eth_sendTransaction) for an operation (POST /payments/{id}/{op}/submitted); HTTP 202.
func (s *PaymentsService) SubmitByHash(ctx context.Context, paymentID Bytes32, op string, params SubmitByHashRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, fmt.Sprintf("/payments/%s/%s/submitted", paymentID, op), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Per-operation convenience wrappers ─────────────────────────────────────────

// AuthorizePrepare builds the unsigned authorize() transaction.
func (s *PaymentsService) AuthorizePrepare(ctx context.Context, paymentID Bytes32) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpAuthorize)
}

// Authorize submits the signed authorize transaction.
func (s *PaymentsService) Authorize(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpAuthorize, params)
}

// ChargePrepare builds the unsigned charge() transaction (one-shot authorize+capture).
func (s *PaymentsService) ChargePrepare(ctx context.Context, paymentID Bytes32) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpCharge)
}

// Charge submits the signed charge transaction.
func (s *PaymentsService) Charge(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpCharge, params)
}

// CapturePrepare builds the unsigned capture() transaction. amount is required.
func (s *PaymentsService) CapturePrepare(ctx context.Context, paymentID Bytes32, amount string) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpCapture, PrepareRequest{Amount: amount})
}

// Capture submits the signed capture transaction.
func (s *PaymentsService) Capture(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpCapture, params)
}

// VoidPrepare builds the unsigned void() transaction. Only valid while the
// authorization is fully intact (nothing captured yet); after any — even partial
// — capture the gateway rejects it (the contract reverts AlreadyCaptured). Use
// Release to return the uncaptured remainder instead.
func (s *PaymentsService) VoidPrepare(ctx context.Context, paymentID Bytes32) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpVoid)
}

// Void submits the signed void transaction.
func (s *PaymentsService) Void(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpVoid, params)
}

// ReleasePrepare builds the unsigned release() transaction. from is optional and
// overrides the submitter address (defaults to the payer when empty). Returns the
// uncaptured escrow to the payer; the payment closes as "released" only on a total
// release (an untouched authorization — nothing captured, nothing refundable),
// otherwise its status is left unchanged so any refundable residual stays actionable.
func (s *PaymentsService) ReleasePrepare(ctx context.Context, paymentID Bytes32, from string) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpRelease, PrepareRequest{From: from})
}

// Release submits the signed release transaction.
func (s *PaymentsService) Release(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpRelease, params)
}

// RefundPrepare builds the refund() transaction (POST /payments/{id}/refund/prepare).
//
// Phase 1: pass req with only Amount set → the returned Transaction's
// SigningPayload holds the EIP-3009 typed data for the payee to sign.
// Phase 2: pass req with Amount + Signature set → the returned Transaction has
// UnsignedTransaction populated, ready to sign and Submit.
//
// The payment closes as "refunded" only once fully settled (escrow and refundable
// both drained); a partial refund leaves its status unchanged.
func (s *PaymentsService) RefundPrepare(ctx context.Context, paymentID Bytes32, req PrepareRequest) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpRefund, req)
}

// Refund submits the signed refund transaction.
func (s *PaymentsService) Refund(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpRefund, params)
}

// ── Disputes (payer-driven, signal-only) ────────────────────────────────────
//
// Disputes follow the same prepare → submit lifecycle as the other operations:
// prepare returns a pending Transaction with the unsigned tx; the payer signs it
// and submits the signed raw tx, which the gateway broadcasts. The on-chain
// event then flips the payment's `disputed` flag.

// disputeBody builds the optional {reason} payload for the dispute prepares.
func disputeBody(reason ...string) map[string]any {
	body := map[string]any{}
	if len(reason) > 0 && reason[0] != "" {
		body["reason"] = reason[0]
	}
	return body
}

// DisputePrepare builds the unsigned dispute() transaction (payer only).
// reason is an optional bytes32 code (0x…); defaults to zero server-side.
func (s *PaymentsService) DisputePrepare(ctx context.Context, paymentID Bytes32, reason ...string) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute/prepare", disputeBody(reason...), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Dispute submits the signed dispute transaction (payer only).
func (s *PaymentsService) Dispute(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseDisputePrepare builds the unsigned closeDispute() transaction (payer only).
func (s *PaymentsService) CloseDisputePrepare(ctx context.Context, paymentID Bytes32, reason ...string) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute/close/prepare", disputeBody(reason...), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseDispute submits the signed close-dispute transaction (payer only).
func (s *PaymentsService) CloseDispute(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute/close", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDisputesParams holds optional query parameters for Payments.Disputes.
type ListDisputesParams struct {
	Status  string // filter by dispute status ("open" or "closed")
	Sort    string // sort fields: comma-separated, prefix with - for desc
	Page    int    // page number (1-based); 0 = use server default
	PerPage int    // items per page; 0 = use server default
}

// Disputes lists a payment's dispute open/close history. The gateway paginates
// this endpoint (bare-array body with pagination in response headers).
func (s *PaymentsService) Disputes(ctx context.Context, paymentID Bytes32, params ...ListDisputesParams) (*PaginatedResponse[Dispute], error) {
	path := "/payments/" + paymentID + "/disputes"
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
	var data []Dispute
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[Dispute]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}
