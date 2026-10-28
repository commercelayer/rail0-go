package rail0

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
//
// Status and Statuses both filter by payment status and may be combined: the
// gateway matches ANY of the listed states (gateway #367), so the SDK sends the
// union — Status first, then Statuses, blanks and duplicates dropped — as one
// comma-separated `status=` value. Status alone behaves exactly as before.
//
// Scope is the operator's lever: "mine" (the default, and what an empty Scope
// means) lists the authenticated wallet's own payments, "all" lists every payment
// on the gateway and needs an admin session (403 otherwise). AccountID, HasError
// and ExpiringWithin only mean something under Scope "all" — they narrow the
// cross-account list by payee account, by the presence of a last_error_code, and
// to authorizations whose expiry falls within that many seconds from now.
type ListPaymentsParams struct {
	Status          string   // filter by one payment status
	Statuses        []string // filter by several payment statuses (e.g. {"authorized", "expired"}); unioned with Status
	Mode            string   // filter by mode ("authorize" or "charge")
	Payer           string   // filter by payer address
	Payee           string   // filter by payee address
	Token           string   // filter by token address
	Rail0ID         string   // filter by the logical on-chain payment id (0x…)
	Reference       string   // filter by the payment's reference in the merchant's system (exact)
	ReferenceOrigin string   // filter by the system the reference comes from
	ChainID         int      // filter by the payment's chain (resolved via its contract); 0 = no filter
	Disputed        *bool    // filter by whether an open dispute exists; nil = no filter
	MinAmount       string   // minimum amount in token base units (inclusive)
	MaxAmount       string   // maximum amount in token base units (inclusive)
	CreatedFrom     string   // only payments created at/after this ISO-8601 time
	CreatedTo       string   // only payments created at/before this ISO-8601 time
	Operation       string   // only payments carrying at least one transaction with this operation (e.g. "dispute")
	Scope           string   // "mine" (default) or "all" (admin only: every payment on the gateway)
	AccountID       string   // Scope "all" only: payments whose payee is one of this account's wallets
	HasError        *bool    // Scope "all" only: true = payments carrying a last_error_code, false = none; nil = no filter
	ExpiringWithin  int      // Scope "all" only: authorized payments expiring within this many seconds (1..90 days); 0 = no filter
	Sort            string   // sort fields: comma-separated, prefix with - for desc
	Page            int      // page number (1-based); 0 = use server default
	PerPage         int      // items per page; 0 = use server default
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
		if st := statusFilter(p.Status, p.Statuses); st != "" {
			q.Set("status", st)
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
		if p.Reference != "" {
			q.Set("reference", p.Reference)
		}
		if p.ReferenceOrigin != "" {
			q.Set("reference_origin", p.ReferenceOrigin)
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
		if p.Operation != "" {
			q.Set("operation", p.Operation)
		}
		if p.Scope != "" {
			q.Set("scope", p.Scope)
		}
		if p.AccountID != "" {
			q.Set("account_id", p.AccountID)
		}
		if p.HasError != nil {
			q.Set("has_error", strconv.FormatBool(*p.HasError))
		}
		if p.ExpiringWithin > 0 {
			q.Set("expiring_within", strconv.Itoa(p.ExpiringWithin))
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
		Page:       atoi(h.Get("X-Page")),
		PerPage:    atoi(h.Get("X-Per-Page")),
		Total:      atoi(h.Get("X-Total-Count")),
		TotalPages: atoi(h.Get("X-Total-Pages")),
		Links:      parseLinkHeader(h.Get("Link")),
	}
}

// linkRelRe matches one RFC 8288 link-value: <uri>; rel="name".
var linkRelRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="([^"]+)"`)

// parseLinkHeader turns the Link header into PageLinks. An absent header yields the
// zero value, which is what an empty collection means: the gateway omits the header
// rather than sending one that points nowhere.
func parseLinkHeader(raw string) PageLinks {
	var out PageLinks
	if raw == "" {
		return out
	}
	for _, m := range linkRelRe.FindAllStringSubmatch(raw, -1) {
		switch m[2] {
		case "first":
			out.First = m[1]
		case "prev":
			out.Prev = m[1]
		case "next":
			out.Next = m[1]
		case "last":
			out.Last = m[1]
		}
	}
	return out
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
	if err := s.http.get(ctx, pathf("/payments/%s", paymentID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update edits a payment's description (PATCH /payments/{id}) and returns the
// updated payment, in the same shape as Get.
//
// Only a participant may call it — the payer or the payee (JWT required): the
// gateway answers 401 without a session, 404 to a non-participant (the payment's
// existence is not disclosed) and 403 wallet_deactivated when the caller's wallet
// is deactivated. It is allowed in any status, since the description is
// off-chain metadata and never touches the escrow.
//
// req.Description == nil (sent as JSON null) or a pointer to "" clears the
// description; anything else replaces it, up to 255 characters (422 above that).
func (s *PaymentsService) Update(ctx context.Context, paymentID Bytes32, req UpdatePaymentRequest) (*Payment, error) {
	var out Payment
	if err := s.http.patch(ctx, pathf("/payments/%s", paymentID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// statusFilter builds the single `status` query value for the list endpoints
// that accept several statuses (payments, payment transactions). The gateway
// takes a comma-separated list and matches any of its values, so the one-value
// Status field and the Statuses slice are unioned rather than one overriding the
// other: Status first, then Statuses in order, with blanks and repeats dropped.
// An empty result means "no status filter" and the caller omits the parameter —
// the gateway answers 400 on an empty `status=`.
func statusFilter(one string, many []string) string {
	seen := make(map[string]bool, len(many)+1)
	var out []string
	for _, v := range append([]string{one}, many...) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return strings.Join(out, ",")
}

// ListTransactionsParams holds optional query parameters for Payments.Transactions.
//
// Status and Statuses combine exactly as on ListPaymentsParams: the union is sent
// as one comma-separated `status=` value, matching any of them — e.g.
// Statuses: {"submitting", "submitted"} asks for what is on its way to the chain.
type ListTransactionsParams struct {
	Operation string   // filter by operation type
	Status    string   // filter by one transaction status
	Statuses  []string // filter by several transaction statuses; unioned with Status
	Sort      string   // sort fields: comma-separated, prefix with - for desc
	Page      int      // page number (1-based); 0 = use server default
	PerPage   int      // items per page; 0 = use server default
}

// Transactions lists on-chain transactions for a payment. Like Payments.List,
// the gateway returns a bare JSON array with pagination in response headers.
func (s *PaymentsService) Transactions(ctx context.Context, paymentID Bytes32, params ...ListTransactionsParams) (*PaginatedResponse[Transaction], error) {
	path := pathf("/payments/%s/transactions", paymentID)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Operation != "" {
			q.Set("operation", p.Operation)
		}
		if st := statusFilter(p.Status, p.Statuses); st != "" {
			q.Set("status", st)
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

// Redrive re-enqueues a stuck broadcast
// (POST /payments/{id}/transactions/{transaction_id}/redrive).
//
// For the one shape a retry can fix: a transaction that is `pending` and whose SIGNED
// bytes the gateway already holds — prepared and signed, never landed on the chain (a
// worker that died between the two, a queue drained by hand). Nothing about the payment
// changes; the same bytes go back to the broadcaster.
//
// Transaction.Redrivable is the same predicate the gateway guards the route with, so
// offer the action on that flag rather than discovering a 422. A `pending` row holding no
// signed transaction is NOT redrivable: there the next step is submitting the signature,
// not retrying a send that never happened.
//
// The transaction id is resolved THROUGH the payment, so one belonging to another payment
// answers 404 rather than redriving someone else's row. Payee-gated, like the other
// operation writes — or an operator session on any payment, which the gateway records
// in its audit log as transaction.redrive.
func (s *PaymentsService) Redrive(ctx context.Context, paymentID Bytes32, transactionID string) (*Transaction, error) {
	var out Transaction
	path := pathf("/payments/%s/transactions/%s/redrive", paymentID, transactionID)
	if err := s.http.post(ctx, path, struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Sign deposits the payer's EIP-712 signature (PUT /payments/{id}/sign).
func (s *PaymentsService) Sign(ctx context.Context, paymentID Bytes32, params PayerSignatureRequest) (*Payment, error) {
	var out Payment
	if err := s.http.put(ctx, pathf("/payments/%s/sign", paymentID), params, &out); err != nil {
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
	return s.PrepareWithKey(ctx, paymentID, op, "", req...)
}

// PrepareWithKey is Prepare with an Idempotency-Key. Pass "" to omit the header and
// get Prepare's behaviour exactly.
//
// What the key buys: without it, a retry that arrives AFTER the first transaction was
// signed and broadcast opens a SECOND one. That is correct for a genuine sequential
// partial capture and wrong for a retry, and nothing but the caller can tell those
// apart - the gateway's own state cannot, because by then the first transaction has
// left `pending` (commercelayer/rail0-gateway#331). Replaying a key returns the
// transaction the first call created, with HTTP 200 instead of 201; the same key with
// different terms is refused 422 idempotency_key_reused. The key is scoped to this
// payment, so reusing it on another one is free.
func (s *PaymentsService) PrepareWithKey(ctx context.Context, paymentID Bytes32, op, idempotencyKey string, req ...PrepareRequest) (*Transaction, error) {
	var out Transaction
	var body any
	if len(req) > 0 {
		body = req[0]
	}
	path := pathf("/payments/%s/%s/prepare", paymentID, op)
	if idempotencyKey != "" {
		headers := map[string]string{"Idempotency-Key": idempotencyKey}
		if err := s.http.postWithHeaders(ctx, path, body, &out, headers); err != nil {
			return nil, err
		}
		return &out, nil
	}
	if err := s.http.post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTransaction reads ONE of a payment's transactions
// (GET /payments/{id}/transactions/{transaction_id}).
//
// This is the lookup for an action id: anything handed a transaction id when an
// operation was accepted resolves it directly, instead of fetching the payment and
// scanning its transactions for an id it already holds
// (commercelayer/rail0-gateway#330). Readable by either participant; an unknown,
// malformed or foreign transaction id all answer 404 alike.
func (s *PaymentsService) GetTransaction(ctx context.Context, paymentID Bytes32, transactionID string) (*Transaction, error) {
	var out Transaction
	if err := s.http.get(ctx, pathf("/payments/%s/transactions/%s", paymentID, transactionID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Submit broadcasts a signed transaction for an operation
// (POST /payments/{id}/{op}); the gateway acknowledges with HTTP 202.
func (s *PaymentsService) Submit(ctx context.Context, paymentID Bytes32, op string, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/%s", paymentID, op), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitByHash records a transaction the caller broadcast themselves
// (MetaMask eth_sendTransaction) for an operation (POST /payments/{id}/{op}/submitted); HTTP 202.
func (s *PaymentsService) SubmitByHash(ctx context.Context, paymentID Bytes32, op string, params SubmitByHashRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/%s/submitted", paymentID, op), params, &out); err != nil {
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

// ReleasePrepare builds the unsigned release() transaction for from, whose nonce it
// carries. from must be the payment's payer or payee (the gateway answers 422
// release_submitter_not_a_party otherwise); empty builds it for the signed-in caller.
// Returns the uncaptured escrow to the payer. The resulting status depends on what
// is left:
//   - a total release, from "authorized" or "expired" (nothing captured), closes the
//     payment as "released";
//   - a release that leaves both balances at zero — the captured part was already
//     fully refunded (e.g. capture 40, refund 40, release 60) — closes it as
//     "refunded", the same outcome as running the refund last;
//   - otherwise the status is left unchanged, so any refundable residual stays
//     refundable and disputable.
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

// DisputePrepare builds the unsigned dispute() transaction (payer only).
//
// reason is optional. Omit it (or pass "") to open the dispute with no reason —
// the field is left out of the request and the calldata carries the zero bytes32,
// which the Dispute entity reads back as "No reason given". When given it must be
// a DisputeOpenReason code (DisputeOpenReasonNotReceived, …) or exactly that code's
// bytes32; anything else is refused by the gateway with HTTP 422
// unknown_dispute_reason. LookupDisputeOpenReason applies the same rule client-side.
// Only the first reason is used.
func (s *PaymentsService) DisputePrepare(ctx context.Context, paymentID Bytes32, reason ...DisputeReason) (*Transaction, error) {
	return s.disputePrepareWithKey(ctx, pathf("/payments/%s/dispute/prepare", paymentID), "", reason...)
}

// DisputePrepareWithKey is DisputePrepare with an Idempotency-Key. Pass "" to omit
// the header. See PrepareWithKey for what the key buys; the reason is part of the
// terms, so reusing a key with another reason is refused 422 idempotency_key_reused.
func (s *PaymentsService) DisputePrepareWithKey(ctx context.Context, paymentID Bytes32, idempotencyKey string, reason ...DisputeReason) (*Transaction, error) {
	return s.disputePrepareWithKey(ctx, pathf("/payments/%s/dispute/prepare", paymentID), idempotencyKey, reason...)
}

// disputeBody builds the optional {reason} payload for the dispute prepares. A
// missing or blank reason leaves the field out, so the gateway records no reason
// (the zero bytes32) instead of being asked to resolve an empty string.
func disputeBody(reason ...DisputeReason) map[string]any {
	body := map[string]any{}
	if len(reason) > 0 {
		if v := strings.TrimSpace(string(reason[0])); v != "" {
			body["reason"] = v
		}
	}
	return body
}

// disputePrepareWithKey is the one body both dispute prepares share, so the pair
// cannot drift into answering the same header differently.
func (s *PaymentsService) disputePrepareWithKey(ctx context.Context, path, idempotencyKey string, reason ...DisputeReason) (*Transaction, error) {
	var out Transaction
	body := disputeBody(reason...)
	if idempotencyKey != "" {
		headers := map[string]string{"Idempotency-Key": idempotencyKey}
		if err := s.http.postWithHeaders(ctx, path, body, &out, headers); err != nil {
			return nil, err
		}
		return &out, nil
	}
	if err := s.http.post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Dispute submits the signed dispute transaction (payer only).
func (s *PaymentsService) Dispute(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/dispute", paymentID), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseDisputePrepare builds the unsigned closeDispute() transaction (payer only).
//
// reason is optional, as for DisputePrepare (omitted = no reason, the zero
// bytes32), but when given it comes from the close list: a DisputeCloseReason code
// (DisputeCloseReasonWithdrawn, …) or its exact bytes32. The system reason
// full_refund is not accepted (the protocol records it itself); it and any other
// value outside the list are refused 422 unknown_dispute_reason.
func (s *PaymentsService) CloseDisputePrepare(ctx context.Context, paymentID Bytes32, reason ...DisputeReason) (*Transaction, error) {
	return s.disputePrepareWithKey(ctx, pathf("/payments/%s/dispute/close/prepare", paymentID), "", reason...)
}

// CloseDisputePrepareWithKey is CloseDisputePrepare with an Idempotency-Key.
func (s *PaymentsService) CloseDisputePrepareWithKey(ctx context.Context, paymentID Bytes32, idempotencyKey string, reason ...DisputeReason) (*Transaction, error) {
	return s.disputePrepareWithKey(ctx, pathf("/payments/%s/dispute/close/prepare", paymentID), idempotencyKey, reason...)
}

// CloseDispute submits the signed close-dispute transaction (payer only).
func (s *PaymentsService) CloseDispute(ctx context.Context, paymentID Bytes32, params SubmitTransactionRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/dispute/close", paymentID), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisputeSubmitByHash records a dispute() transaction the caller broadcast
// themselves (MetaMask eth_sendTransaction) — the payer's counterpart to
// SubmitByHash (POST /payments/{id}/dispute/submitted); HTTP 202. Payer-only:
// the caller must be authenticated as the payment's payer (an account-less SIWE
// token is fine), since the bare hash carries no signature to authorize it.
func (s *PaymentsService) DisputeSubmitByHash(ctx context.Context, paymentID Bytes32, params SubmitByHashRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/dispute/submitted", paymentID), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseDisputeSubmitByHash records a closeDispute() transaction the caller
// broadcast themselves (POST /payments/{id}/dispute/close/submitted); HTTP 202.
// Payer-only, same contract as DisputeSubmitByHash.
func (s *PaymentsService) CloseDisputeSubmitByHash(ctx context.Context, paymentID Bytes32, params SubmitByHashRequest) (*Transaction, error) {
	var out Transaction
	if err := s.http.post(ctx, pathf("/payments/%s/dispute/close/submitted", paymentID), params, &out); err != nil {
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
	path := pathf("/payments/%s/disputes", paymentID)
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
