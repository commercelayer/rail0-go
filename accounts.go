package rail0

import (
	"context"
)

// AccountsService reads and edits the merchant account itself (requires JWT):
// name, contact email and the priority_tip fee setting.
//
// Get and Update are the whole surface by design. The gateway's
// /accounts/{account_id} is behind an owner-or-operator guard: a merchant
// session reaches only its OWN account, and an operator (an admin session) may
// reach any. For anyone else, another account's id answers the same 403 as a
// plain ownership miss, so the pair cannot be used to tell whether an account
// exists. Activating or deactivating an account is an operator action and is
// deliberately not exposed here.
//
// The account's wallets are a collection under the same path and live on
// WalletsService; buyer-facing discovery of what a merchant accepts lives on
// PaymentMethodsService.
type AccountsService struct {
	http *httpClient
}

// Account is the merchant account as its own holder reads it.
//
// Email is part of the response because the holder is the endpoint's only
// possible caller — this is a merchant reading its own contact address, never
// another's.
//
// PriorityTip is the fee setting of the account's OWN transactions (authorize,
// capture, void, release, refund): true (the default) tips the network's suggested
// priority fee, false uses the chain's minimum tip — 0 on most chains, 25 gwei on
// Polygon PoS/Amoy. The buyer's dispute/close_dispute never read it.
type Account struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	PriorityTip bool   `json:"priority_tip"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// Get returns the account's own profile: id, name, email and timestamps.
func (s *AccountsService) Get(ctx context.Context, accountID string) (*Account, error) {
	var out Account
	if err := s.http.get(ctx, pathf("/accounts/%s", accountID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAccountRequest is the body for AccountsService.Update. Every field is
// optional, but at least one must be set: a PATCH with nothing to change is
// refused with 400 rather than treated as a silent no-op.
//
// Name and Email are each unique across accounts, so a value another account
// already holds answers 409. PriorityTip is a pointer because false is a value
// here, not an absence: Bool(false) switches the account's own transactions to
// the chain's minimum tip, nil leaves the setting untouched. The account's active
// flag is operator-only and is intentionally absent.
type UpdateAccountRequest struct {
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	PriorityTip *bool  `json:"priority_tip,omitempty"`
}

// Update changes the account's own profile (PATCH /accounts/{account_id}) and
// returns it as Get would.
//
// The gateway answers 400 validation_failed when no field is set (or the
// email is malformed), 409 conflict when the name or email is already taken, and 403
// account_deactivated / wallet_deactivated when the account or the signed-in
// wallet has been deactivated — a revoked key must not be able to redirect the
// account's contact email.
func (s *AccountsService) Update(ctx context.Context, accountID string, req UpdateAccountRequest) (*Account, error) {
	var out Account
	if err := s.http.patch(ctx, pathf("/accounts/%s", accountID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
