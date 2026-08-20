package rail0

import (
	"context"
	"fmt"
)

// AccountsService reads the merchant account itself (requires JWT).
//
// One method, and that is the whole surface by design: the gateway's
// /accounts/{account_id} is behind an ownership guard — a JWT whose account
// matches the path — so a caller can only ever read its OWN account. There is
// no endpoint for reading another merchant's, and an id that is not an account
// answers 404 exactly as another account's id does, so the pair cannot be used
// to tell whether an account exists.
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
