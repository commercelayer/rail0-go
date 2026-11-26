package rail0

import (
	"context"
	"net/url"
	"strconv"
)

// The operator's side of accounts and wallets: find an account, read the whole record,
// see what a deactivation would leave in motion, switch an account off and on, and read
// what still names a wallet's address.
//
// Two things an operator does are NOT here, because the public services already do them
// for an operator session: WalletsService.List reads any account's wallets, and
// WalletsService.Update switches any wallet off and on (Active). The gateway widens both
// for the operator grant; a switch-off made that way is recorded as the operator's, and
// only an operator can lift it.

// ListAdminAccountsParams filters GET /accounts, the list of every account.
type ListAdminAccountsParams struct {
	// Query searches: a part of the name or of the email (case-insensitive), a wallet
	// address or its beginning (0x…), or the account's id.
	Query string
	// Status is "active" or "deactivated".
	Status string
	// Admin keeps only the accounts that hold the operator grant (true) or only the ones
	// that do not (false); nil does not filter.
	Admin   *bool
	Sort    string // name, email or created_at; prefix with - for desc (default: name)
	Page    int
	PerPage int
}

// Accounts lists every account, as the operator reads them: the whole record, with the
// operator's switch (Active, Status) and whether it holds the operator grant (Admin).
func (s *AdminService) Accounts(ctx context.Context, params ...ListAdminAccountsParams) (*PaginatedResponse[AccountFull], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "q", p.Query)
		setNonEmpty(q, "status", p.Status)
		if p.Admin != nil {
			q.Set("admin", strconv.FormatBool(*p.Admin))
		}
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[AccountFull](ctx, s.http, withQuery("/accounts", q))
}

// Account reads one account's whole record (GET /accounts/{account_id} with an operator
// session). AccountsService.Get reads the same route and keeps only the fields an
// account's own holder sees.
func (s *AdminService) Account(ctx context.Context, accountID string) (*AccountFull, error) {
	var out AccountFull
	if err := s.http.get(ctx, pathf("/accounts/%s", accountID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AccountExposure is what an account still has in motion: what a deactivation would
// leave where it is. Nothing in it is lost by a deactivation (escrow always returns to
// the buyer by void or release, and the wallet is the merchant's), but the operator
// decides with these figures in hand.
//
// It reads the account as a PAYEE only. A payment can be in two categories at once (a
// partial capture with escrow left and a dispute on the captured part), so the three
// counts do not add up to a number of payments; ExposureToken.Count does.
type AccountExposure struct {
	Payments ExposurePayments `json:"payments"`
	Payouts  ExposurePayouts  `json:"payouts"`
	// Empty is true when every count is zero: the account can be switched off with
	// nothing left half-way.
	Empty bool `json:"empty"`
}

// ExposurePayments counts an account's open payments by category.
type ExposurePayments struct {
	// InFlight: created or signed, not yet on chain, authorization window still open.
	// After a deactivation the merchant can no longer authorize or charge them.
	InFlight int `json:"in_flight"`
	// InEscrow: buyer funds held by the RAIL0 contract (capturable amount above zero).
	InEscrow int `json:"in_escrow"`
	// Disputed: open disputes.
	Disputed int `json:"disputed"`
	// ByToken totals the three categories per (chain, token).
	ByToken []ExposureToken `json:"by_token"`
}

// ExposureToken is an account's open exposure in one token of one chain.
type ExposureToken struct {
	// ChainID is nil when the gateway no longer knows the payment's contract.
	ChainID *int   `json:"chain_id"`
	Token   string `json:"token"`
	// Symbol is empty when the catalogue no longer knows the token.
	Symbol string `json:"symbol,omitempty"`
	// Count is the number of distinct payments contributing.
	Count int `json:"count"`
	// Amount is in base units: in-flight amounts, escrow and disputed refundable balances.
	Amount string `json:"amount"`
	// AmountFormatted is Amount in token units (the base units when the token is unknown).
	AmountFormatted string `json:"amount_formatted"`
}

// ExposurePayouts counts an account's payouts still moving. They are counted, not
// totalled: a payout moves the merchant's own money to a venue of its own.
type ExposurePayouts struct {
	Pending   int `json:"pending"`
	Submitted int `json:"submitted"`
}

// AccountExposure reads what an account still has in motion
// (GET /admin/accounts/{id}/exposure): the figures to read before DeactivateAccount.
func (s *AdminService) AccountExposure(ctx context.Context, accountID string) (*AccountExposure, error) {
	var out AccountExposure
	if err := s.http.get(ctx, pathf("/admin/accounts/%s/exposure", accountID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// accountSwitch is the body of the operator's switch on PATCH /accounts/{account_id}.
type accountSwitch struct {
	Active                  bool `json:"active"`
	AcknowledgeOpenExposure bool `json:"acknowledge_open_exposure,omitempty"`
}

// DeactivateAccount switches an account off. It is a write: the client must carry a
// write-mode token (see AdminService), and the gateway audits it.
//
// A deactivated account can start no new payment and write no payout; what is already in
// motion stays where it is. So the gateway refuses with 409 `account_has_open_exposure`
// while the account has any (AccountExposure), unless acknowledgeOpenExposure is true:
// the operator read the figures and decided. It also refuses an operator's own account
// (422 `self_deactivate`) and the account holding the last active operator grant (422
// `last_admin`).
func (s *AdminService) DeactivateAccount(ctx context.Context, accountID string, acknowledgeOpenExposure bool) (*AccountFull, error) {
	return s.switchAccount(ctx, accountID, accountSwitch{Active: false, AcknowledgeOpenExposure: acknowledgeOpenExposure})
}

// ActivateAccount switches a deactivated account back on. A write, audited, in write
// mode like DeactivateAccount.
func (s *AdminService) ActivateAccount(ctx context.Context, accountID string) (*AccountFull, error) {
	return s.switchAccount(ctx, accountID, accountSwitch{Active: true})
}

func (s *AdminService) switchAccount(ctx context.Context, accountID string, body accountSwitch) (*AccountFull, error) {
	var out AccountFull
	if err := s.http.patch(ctx, pathf("/accounts/%s", accountID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WalletUsage counts what still names a wallet's address
// (GET /accounts/{account_id}/wallets/{id}/usage): the payments it is the payee or the
// payer of, and the transactions it sent. What an operator reads before switching the
// wallet off. idOrAddress is the wallet's UUID or its 0x address.
func (s *AdminService) WalletUsage(ctx context.Context, accountID, idOrAddress string) (*WalletUsage, error) {
	var out WalletUsage
	if err := s.http.get(ctx, pathf("/accounts/%s/wallets/%s/usage", accountID, idOrAddress), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
