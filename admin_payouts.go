package rail0

import (
	"context"
	"net/url"
	"strings"
)

// Every account's payouts and payout destinations, READ-ONLY. The gateway is
// non-custodial and every payout write is the destination wallet's own (PayoutsService,
// PayoutDestinationsService): an operator can look, not act.
//
// With the payouts feature off on the gateway these routes do not exist: every call
// answers 404.

// OperatorPayout is a payout as the operator reads it: every column but the signed
// payload, plus the account.
type OperatorPayout struct {
	Payout
	AccountID string `json:"account_id,omitempty"`
	// ErrorReason is the stored reason behind ErrorCode.
	ErrorReason string `json:"error_reason,omitempty"`
	// BlockHash is the hash of the block recorded with BlockNumber, checked against the
	// canonical chain before the payout is settled.
	BlockHash string `json:"block_hash,omitempty"`
	// Account is the owning account, as {id, name}.
	Account any `json:"account,omitempty"`
}

// OperatorPayoutDestination is a payout destination as the operator reads it: every
// column, plus the account.
type OperatorPayoutDestination struct {
	PayoutDestination
	AccountID string `json:"account_id,omitempty"`
	// CreatedBy is the session address that registered the destination.
	CreatedBy string `json:"created_by,omitempty"`
	// VerifiedBy is the session address that confirmed it; empty while unverified.
	VerifiedBy string `json:"verified_by,omitempty"`
	// Account is the owning account, as {id, name}.
	Account any `json:"account,omitempty"`
}

// OperatorPayoutEvent is a row of a payout's or a destination's audit trail with the
// account it belongs to.
type OperatorPayoutEvent struct {
	PayoutEvent
	AccountID string `json:"account_id,omitempty"`
}

// ListAdminPayoutsParams filters GET /admin/payouts.
type ListAdminPayoutsParams struct {
	AccountID     string   // only this account's payouts
	WalletID      string   // only payouts from this wallet
	DestinationID string   // only payouts to this destination
	ChainID       int      // EVM chain id
	Statuses      []string // any of these statuses
	Purpose       string   // "payout" or "proof"
	CreatedFrom   string   // only payouts created at/after this ISO-8601 time
	CreatedTo     string   // only payouts created at/before this ISO-8601 time
	Sort          string   // sort fields: comma-separated, prefix with - for desc
	Page          int
	PerPage       int
}

// Payouts lists every account's payouts, newest first.
func (s *AdminService) Payouts(ctx context.Context, params ...ListAdminPayoutsParams) (*PaginatedResponse[OperatorPayout], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "account_id", p.AccountID)
		setNonEmpty(q, "wallet_id", p.WalletID)
		setNonEmpty(q, "destination_id", p.DestinationID)
		setPositive(q, "chain_id", p.ChainID)
		setNonEmpty(q, "status", strings.Join(p.Statuses, ","))
		setNonEmpty(q, "purpose", p.Purpose)
		setNonEmpty(q, "created_from", p.CreatedFrom)
		setNonEmpty(q, "created_to", p.CreatedTo)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OperatorPayout](ctx, s.http, withQuery("/admin/payouts", q))
}

// Payout reads any account's payout.
func (s *AdminService) Payout(ctx context.Context, id string) (*OperatorPayout, error) {
	var out OperatorPayout
	if err := s.http.get(ctx, pathf("/admin/payouts/%s", id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PayoutEvents lists a payout's audit trail, newest first.
func (s *AdminService) PayoutEvents(ctx context.Context, id string, params ...ListPayoutEventsParams) (*PaginatedResponse[OperatorPayoutEvent], error) {
	return listPage[OperatorPayoutEvent](ctx, s.http, withQuery(pathf("/admin/payouts/%s/events", id), eventsPaging(params)))
}

// ListAdminPayoutDestinationsParams filters GET /admin/payout_destinations.
type ListAdminPayoutDestinationsParams struct {
	AccountID string // only this account's destinations
	WalletID  string // only this wallet's destinations
	ChainID   int    // EVM chain id
	Status    string // "unverified", "verified" or "disabled"
	Sort      string // created_at, updated_at, label or status; prefix with - for desc
	Page      int
	PerPage   int
}

// PayoutDestinations lists every account's payout destinations.
func (s *AdminService) PayoutDestinations(ctx context.Context, params ...ListAdminPayoutDestinationsParams) (*PaginatedResponse[OperatorPayoutDestination], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "account_id", p.AccountID)
		setNonEmpty(q, "wallet_id", p.WalletID)
		setPositive(q, "chain_id", p.ChainID)
		setNonEmpty(q, "status", p.Status)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OperatorPayoutDestination](ctx, s.http, withQuery("/admin/payout_destinations", q))
}

// PayoutDestination reads any account's payout destination.
func (s *AdminService) PayoutDestination(ctx context.Context, id string) (*OperatorPayoutDestination, error) {
	var out OperatorPayoutDestination
	if err := s.http.get(ctx, pathf("/admin/payout_destinations/%s", id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PayoutDestinationEvents lists a destination's audit trail, newest first: its own
// events and those of every payout made to it.
func (s *AdminService) PayoutDestinationEvents(ctx context.Context, id string, params ...ListPayoutEventsParams) (*PaginatedResponse[OperatorPayoutEvent], error) {
	return listPage[OperatorPayoutEvent](ctx, s.http, withQuery(pathf("/admin/payout_destinations/%s/events", id), eventsPaging(params)))
}

func eventsPaging(params []ListPayoutEventsParams) url.Values {
	q := url.Values{}
	if len(params) > 0 {
		setPaging(q, "", params[0].Page, params[0].PerPage)
	}
	return q
}
