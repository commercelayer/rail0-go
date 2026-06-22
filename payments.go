package rail0

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

// rail0IDPattern matches a logical rail0_id: a 0x-prefixed 32-byte hex hash.
// Anything else (e.g. a UUID) is treated as the technical primary key.
var rail0IDPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

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
	Status  string // filter by payment status
	Mode    string // filter by mode ("authorize" or "charge")
	Payer   string // filter by payer address
	Payee   string // filter by payee address
	Token   string // filter by token address
	Rail0ID string // filter by the logical on-chain payment id (0x…)
	Sort    string // sort fields: comma-separated, prefix with - for desc
	Page    int    // page number (1-based); 0 = use server default
	PerPage int    // items per page; 0 = use server default
}

// ResolvePaymentID returns the technical UUID for a payment given either its
// UUID or its logical rail0_id (0x-prefixed 32-byte hash). Path operations
// address payments by UUID; a rail0_id — what users know — is resolved through
// the list filter (requires JWT). A value that is not a rail0_id is returned
// unchanged, so callers can pass whichever identifier they hold.
func (s *PaymentsService) ResolvePaymentID(ctx context.Context, idOrRail0 string) (Bytes32, error) {
	if !rail0IDPattern.MatchString(idOrRail0) {
		return idOrRail0, nil
	}
	res, err := s.List(ctx, ListPaymentsParams{Rail0ID: idOrRail0, PerPage: 1})
	if err != nil {
		return "", err
	}
	if len(res.Data) == 0 {
		return "", fmt.Errorf("rail0: no payment found for rail0_id %s", idOrRail0)
	}
	return res.Data[0].ID, nil
}

// List returns payments for the authenticated account (requires JWT).
func (s *PaymentsService) List(ctx context.Context, params ...ListPaymentsParams) (*PaginatedResponse[Payment], error) {
	var out PaginatedResponse[Payment]
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

// Transactions lists on-chain transactions for a payment.
func (s *PaymentsService) Transactions(ctx context.Context, paymentID Bytes32, params ...ListTransactionsParams) (*PaginatedResponse[Transaction], error) {
	var out PaginatedResponse[Transaction]
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
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// VoidPrepare builds the unsigned void() transaction.
func (s *PaymentsService) VoidPrepare(ctx context.Context, paymentID Bytes32) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpVoid)
}

// Void submits the signed void transaction.
func (s *PaymentsService) Void(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpVoid, params)
}

// ReleasePrepare builds the unsigned release() transaction. from is optional and
// overrides the submitter address (defaults to the payer when empty).
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
func (s *PaymentsService) RefundPrepare(ctx context.Context, paymentID Bytes32, req PrepareRequest) (*Transaction, error) {
	return s.Prepare(ctx, paymentID, OpRefund, req)
}

// Refund submits the signed refund transaction.
func (s *PaymentsService) Refund(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	return s.Submit(ctx, paymentID, OpRefund, params)
}

// ── Disputes (buyer-driven, signal-only) ────────────────────────────────────

// DisputePrepareResult is returned by the dispute prepare endpoints: the
// unsigned transaction for the payer to sign and broadcast, plus the bytes32
// reason code that was encoded.
type DisputePrepareResult struct {
	UnsignedTransaction map[string]any `json:"unsigned_transaction"`
	Reason              string         `json:"reason"`
}

// DisputePrepare builds the unsigned dispute() transaction (payer only).
// reason is an optional bytes32 code (0x…); defaults to zero server-side.
func (s *PaymentsService) DisputePrepare(ctx context.Context, paymentID Bytes32, reason ...string) (*DisputePrepareResult, error) {
	body := map[string]any{}
	if len(reason) > 0 && reason[0] != "" {
		body["reason"] = reason[0]
	}
	var out DisputePrepareResult
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute/prepare", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseDisputePrepare builds the unsigned closeDispute() transaction (payer only).
func (s *PaymentsService) CloseDisputePrepare(ctx context.Context, paymentID Bytes32, reason ...string) (*DisputePrepareResult, error) {
	body := map[string]any{}
	if len(reason) > 0 && reason[0] != "" {
		body["reason"] = reason[0]
	}
	var out DisputePrepareResult
	if err := s.http.post(ctx, "/payments/"+paymentID+"/dispute/close/prepare", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Disputes lists a payment's dispute open/close history.
func (s *PaymentsService) Disputes(ctx context.Context, paymentID Bytes32) ([]Dispute, error) {
	var out []Dispute
	if err := s.http.get(ctx, "/payments/"+paymentID+"/disputes", &out); err != nil {
		return nil, err
	}
	return out, nil
}
