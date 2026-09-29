package rail0

import (
	"context"
	"fmt"
)

// AccountsService reads and edits the merchant account itself (requires JWT).
//
// Get and Update are the whole surface by design: the gateway's
// /accounts/{account_id} is behind an ownership guard — a JWT whose account
// matches the path — so a caller can only ever read or change its OWN account.
// There is no endpoint for another merchant's, and an id that is not an account
// answers 404 exactly as another account's id does, so the pair cannot be used
// to tell whether an account exists. Activating or deactivating an account is
// an operator action and is deliberately not exposed here.
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
type Account struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Get returns the account's own profile: id, name, email and timestamps.
func (s *AccountsService) Get(ctx context.Context, accountID string) (*Account, error) {
	var out Account
	if err := s.http.get(ctx, fmt.Sprintf("/accounts/%s", accountID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAccountRequest is the body for AccountsService.Update. Both fields are
// optional, but at least one must be set: a PATCH with nothing to change is
// refused with 400 rather than treated as a silent no-op.
//
// Name and Email are each unique across accounts, so a value another account
// already holds answers 409. The account's active flag is operator-only and is
// intentionally absent.
type UpdateAccountRequest struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Update changes the account's own profile (PATCH /accounts/{account_id}) and
// returns it as Get would.
//
// The gateway answers 400 validation_failed when neither field is set (or the
// email is malformed), 409 conflict when the name or email is already taken, and 403
// account_deactivated / wallet_deactivated when the account or the signed-in
// wallet has been deactivated — a revoked key must not be able to redirect the
// account's contact email.
func (s *AccountsService) Update(ctx context.Context, accountID string, req UpdateAccountRequest) (*Account, error) {
	var out Account
	if err := s.http.patch(ctx, fmt.Sprintf("/accounts/%s", accountID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
