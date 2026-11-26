package rail0

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// AdminService is the operator's side of the gateway: the cross-account routes under
// /admin. Every call needs a session whose account holds the operator grant; any other
// session is answered 403 `forbidden`, with no hint of why.
//
// The SDK otherwise tracks the public surface, and this service is the deliberate
// exception: it carries what an operator does from a terminal while something is wrong
// — read the diagnostics, find the stuck transactions, read and clear the sync errors,
// follow one payment through deliveries, disputes and the audit log. Granting the
// operator role, onboarding and the catalogue stay with the console.
//
// READS AND WRITES. An operator session reads. A request that writes (here, only
// ClearSyncErrors) is refused 403 `write_mode_required` unless the client carries a
// write-mode token: a second JWT, issued by EnableWriteMode for the same session, valid
// for an hour by default and never longer than the session. The caller puts it on the
// client with SetToken, and puts the session token back when it is done (DisableWriteMode
// revokes the write-mode token). The SDK does not swap them by itself: when a client may
// write is the caller's decision, and both switches are audited on the gateway.
type AdminService struct {
	http *httpClient
}

// WriteModeStatus is GET /admin/write_mode: whether the token the client carries is a
// write-mode one, and until when.
type WriteModeStatus struct {
	Active bool `json:"active"`
	// ExpiresAt is when the write-mode token stops working (RFC 3339), empty while
	// Active is false.
	ExpiresAt string `json:"expires_at,omitempty"`
}

// WriteModeToken is POST /admin/write_mode: the write-mode token and its expiry. The
// token is a credential, like the session's: do not log it.
type WriteModeToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// WriteMode reports whether the client's token is in write mode.
func (s *AdminService) WriteMode(ctx context.Context) (*WriteModeStatus, error) {
	var out WriteModeStatus
	if err := s.http.get(ctx, "/admin/write_mode", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnableWriteMode asks for a write-mode token for the client's session. It asks for no
// signature: the session already proved the wallet. The returned token replaces the
// session token on the client (SetToken) for as long as the caller wants to write; the
// session token itself stays valid and is what to go back to.
func (s *AdminService) EnableWriteMode(ctx context.Context) (*WriteModeToken, error) {
	var out WriteModeToken
	if err := s.http.post(ctx, "/admin/write_mode", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisableWriteMode revokes the write-mode token the client carries, and reports whether
// there was one to revoke: false when the client carries the session token, or when the
// gateway could not record the revocation (the token then stays valid until it expires).
// The caller puts the session token back on the client afterwards.
func (s *AdminService) DisableWriteMode(ctx context.Context) (bool, error) {
	var out struct {
		Revoked bool `json:"revoked"`
	}
	if err := s.http.delete(ctx, "/admin/write_mode", &out); err != nil {
		return false, err
	}
	return out.Revoked, nil
}

// Health is the operator diagnostics (GET /admin/health): one verdict, then a named
// check per thing that can be wrong, each with its figures and a sentence saying what
// they mean. Always 200: a degraded system is the content, not an error, so the answer
// is in Status ("ok", "degraded", "error"), never in err.
func (s *AdminService) Health(ctx context.Context) (*AdminHealth, error) {
	var out AdminHealth
	if err := s.http.get(ctx, "/admin/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RPCHealth probes every active chain's RPC endpoints from the gateway's process
// (GET /admin/rpc_health) and reports each one's reachability, head and latency. The
// answer is cached for a few seconds on the gateway; CheckedAt says when the probe ran.
func (s *AdminService) RPCHealth(ctx context.Context) (*AdminRpcHealth, error) {
	var out AdminRpcHealth
	if err := s.http.get(ctx, "/admin/rpc_health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OperatorPaymentSummary is the payment a cross-account row belongs to, as the operator
// listings embed it: enough to read the row without a second request.
type OperatorPaymentSummary struct {
	ID       string `json:"id,omitempty"`
	Rail0ID  string `json:"rail0_id,omitempty"`
	ChainID  int    `json:"chain_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Decimals *int   `json:"decimals,omitempty"`
	Payer    string `json:"payer,omitempty"`
	Payee    string `json:"payee,omitempty"`
	Token    string `json:"token,omitempty"`
}

// OperatorTransaction is a transaction as the operator lists it: the transaction, why it
// failed when it did, and the payment it belongs to.
type OperatorTransaction struct {
	Transaction
	ErrorReason string                  `json:"error_reason,omitempty"`
	Payment     *OperatorPaymentSummary `json:"payment,omitempty"`
}

// ListAdminTransactionsParams filters GET /admin/transactions.
type ListAdminTransactionsParams struct {
	// Stuck keeps only stuck rows: "unbroadcast" (signed, never sent), "unconfirmed"
	// (sent, never confirmed) or "any".
	Stuck     string
	Statuses  []string // any of these statuses
	Operation string   // authorize, capture, charge, void, release, refund, …
	ChainID   int      // EVM chain id
	AccountID string   // payments whose payee is one of this account's wallets
	Hash      string   // the transaction with this hash (0x + 64 hex)
	Sort      string   // sort fields: comma-separated, prefix with - for desc
	Page      int      // page number (1-based); 0 = use server default
	PerPage   int      // items per page; 0 = use server default
}

// Transactions lists transactions across every account, or only the stuck ones.
func (s *AdminService) Transactions(ctx context.Context, params ...ListAdminTransactionsParams) (*PaginatedResponse[OperatorTransaction], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "stuck", p.Stuck)
		setNonEmpty(q, "status", strings.Join(p.Statuses, ","))
		setNonEmpty(q, "operation", p.Operation)
		setPositive(q, "chain_id", p.ChainID)
		setNonEmpty(q, "account_id", p.AccountID)
		setNonEmpty(q, "hash", p.Hash)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OperatorTransaction](ctx, s.http, withQuery("/admin/transactions", q))
}

// SyncError is one row of the gateway's sync-error ledger: an indexer callback the
// gateway could not apply, and why.
type SyncError struct {
	ID string `json:"id,omitempty"`
	// Reason is why the callback was refused: not_found, external_operation,
	// chain_mismatch, payment_mismatch, operation_mismatch, unapplicable,
	// balance_divergence or impossible_balances.
	Reason string `json:"reason,omitempty"`
	// Outcome is the kind of callback: "confirmed" or "failed".
	Outcome         string `json:"outcome,omitempty"`
	TransactionHash string `json:"transaction_hash,omitempty"`
	// PaymentID is the payment's ON-CHAIN id (0x…), as the callback named it; empty
	// when the callback named none.
	PaymentID   string `json:"payment_id,omitempty"`
	ChainID     *int   `json:"chain_id,omitempty"`
	BlockNumber *int   `json:"block_number,omitempty"`
	EventType   string `json:"event_type,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// SyncErrorFilter selects rows of the sync-error ledger. The listing and the clear read
// it the same way, so a clear removes exactly what the same filter lists.
type SyncErrorFilter struct {
	Reason  string // only this rejection reason (see SyncError.Reason)
	Outcome string // "confirmed" or "failed"
	TxHash  string // only this transaction hash
	// PaymentID is a payment's id (UUID) or its rail0_id (0x…).
	PaymentID string
}

func (f SyncErrorFilter) values() url.Values {
	q := url.Values{}
	setNonEmpty(q, "reason", f.Reason)
	setNonEmpty(q, "outcome", f.Outcome)
	setNonEmpty(q, "tx_hash", f.TxHash)
	setNonEmpty(q, "payment_id", f.PaymentID)
	return q
}

// ListSyncErrorsParams filters and pages GET /admin/sync_errors.
type ListSyncErrorsParams struct {
	SyncErrorFilter
	Sort    string // created_at or reason; prefix with - for desc (default: newest first)
	Page    int
	PerPage int
}

// SyncErrors lists the sync-error ledger, newest first.
func (s *AdminService) SyncErrors(ctx context.Context, params ...ListSyncErrorsParams) (*PaginatedResponse[SyncError], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		q = p.SyncErrorFilter.values()
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[SyncError](ctx, s.http, withQuery("/admin/sync_errors", q))
}

// ClearSyncErrors deletes the rows the filter selects and returns how many went. It is a
// write: the client must carry a write-mode token (see AdminService), and the gateway
// audits the call with its filters.
//
// AN EMPTY FILTER EMPTIES THE LEDGER. That includes the `balance_divergence` rows, whose
// standing count Health reports: clearing unfiltered resets that detector's history. To
// drop a backlog and keep the divergences, filter by Reason.
func (s *AdminService) ClearSyncErrors(ctx context.Context, filter SyncErrorFilter) (int, error) {
	var out struct {
		Deleted int `json:"deleted"`
	}
	if err := s.http.delete(ctx, withQuery("/admin/sync_errors", filter.values()), &out); err != nil {
		return 0, err
	}
	return out.Deleted, nil
}

// OperatorWebhook is a webhook subscription as the operator sees it: every column but
// the signing keys, plus the account that owns it and its newest delivery attempt.
type OperatorWebhook struct {
	Webhook
	AccountID         string `json:"account_id,omitempty"`
	DisabledAt        string `json:"disabled_at,omitempty"`
	RotatedAt         string `json:"rotated_at,omitempty"`
	LastAttemptAt     string `json:"last_attempt_at,omitempty"`
	LastAttemptStatus string `json:"last_attempt_status,omitempty"`
	// Account is the owning account's public shape (id, name, email, …).
	Account any `json:"account,omitempty"`
}

// OperatorEventCallback is a webhook delivery as the operator lists it: the record, plus
// the subscription it went to and, through it, the account.
type OperatorEventCallback struct {
	EventCallback
	Webhook *OperatorWebhook `json:"webhook,omitempty"`
}

// ListAdminEventCallbacksParams filters GET /admin/event_callbacks.
type ListAdminEventCallbacksParams struct {
	PaymentID    string // a payment's id (UUID) or its rail0_id (0x…)
	WebhookID    string // only this subscription's deliveries
	Status       string // "delivered" or "failed"
	Topic        string // event topic
	ResponseCode string // the subscriber's exact HTTP response code
	Since        string // only deliveries at/after this ISO-8601 time
	Until        string // only deliveries at/before this ISO-8601 time
	Sort         string
	Page         int
	PerPage      int
}

// EventCallbacks lists webhook deliveries across accounts, by payment or subscription.
func (s *AdminService) EventCallbacks(ctx context.Context, params ...ListAdminEventCallbacksParams) (*PaginatedResponse[OperatorEventCallback], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "payment_id", p.PaymentID)
		setNonEmpty(q, "webhook_id", p.WebhookID)
		setNonEmpty(q, "status", p.Status)
		setNonEmpty(q, "topic", p.Topic)
		setNonEmpty(q, "response_code", p.ResponseCode)
		setNonEmpty(q, "since", p.Since)
		setNonEmpty(q, "until", p.Until)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OperatorEventCallback](ctx, s.http, withQuery("/admin/event_callbacks", q))
}

// OperatorDispute is a dispute as the operator lists it: the record, with the payment it
// is about.
type OperatorDispute struct {
	Dispute
	Payment *OperatorPaymentSummary `json:"payment,omitempty"`
}

// ListAdminDisputesParams filters GET /admin/disputes.
type ListAdminDisputesParams struct {
	Status     string // "open" or "closed"
	PaymentID  string // a payment's id (UUID) or its rail0_id (0x…)
	ChainID    int    // EVM chain id
	ReasonCode string // the dictionary code the dispute was opened with
	From       string // only disputes opened at/after this ISO-8601 time
	To         string // only disputes opened at/before this ISO-8601 time
	Sort       string
	Page       int
	PerPage    int
}

// Disputes lists every dispute across accounts, open and closed.
func (s *AdminService) Disputes(ctx context.Context, params ...ListAdminDisputesParams) (*PaginatedResponse[OperatorDispute], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "status", p.Status)
		setNonEmpty(q, "payment_id", p.PaymentID)
		setPositive(q, "chain_id", p.ChainID)
		setNonEmpty(q, "reason_code", p.ReasonCode)
		setNonEmpty(q, "from", p.From)
		setNonEmpty(q, "to", p.To)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OperatorDispute](ctx, s.http, withQuery("/admin/disputes", q))
}

// ListAdminActionsParams filters GET /admin/actions.
type ListAdminActionsParams struct {
	Action          string // only this kind of action (e.g. "sync_errors.clear")
	ActorAccountID  string // only actions by this operator's account
	TargetAccountID string // only actions that landed on this account
	ResourceType    string // only actions on this kind of resource (e.g. "wallet")
	// PaymentID is the payment an action's detail names (a redrive): its UUID. Unlike the
	// other operator filters this one does not take a rail0_id.
	PaymentID string
	From      string // only actions at/after this ISO-8601 time
	To        string // only actions at/before this ISO-8601 time
	Sort      string
	Page      int
	PerPage   int
}

// Actions lists the operator audit log: every operator write, who did it and to whom.
func (s *AdminService) Actions(ctx context.Context, params ...ListAdminActionsParams) (*PaginatedResponse[AdminAction], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "action", p.Action)
		setNonEmpty(q, "actor_account_id", p.ActorAccountID)
		setNonEmpty(q, "target_account_id", p.TargetAccountID)
		setNonEmpty(q, "resource_type", p.ResourceType)
		setNonEmpty(q, "payment_id", p.PaymentID)
		setNonEmpty(q, "from", p.From)
		setNonEmpty(q, "to", p.To)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[AdminAction](ctx, s.http, withQuery("/admin/actions", q))
}

func setPositive(q url.Values, key string, value int) {
	if value > 0 {
		q.Set(key, strconv.Itoa(value))
	}
}
