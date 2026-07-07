package rail0

import (
	"context"
	"net/url"
)

// PaymentMethodsService exposes the public GET /payment_methods endpoint: a
// buyer-facing discovery of a merchant's accepted payment methods. It is
// unauthenticated — a payer that only knows the merchant (by account id, or by
// one of the merchant's wallet addresses) can list the active wallet/token
// combinations the merchant accepts, without holding the merchant's session.
//
// This is the public counterpart to the account-scoped WalletsService (which is
// behind SIWE): it never exposes operational/internal wallet fields, only the
// active wallets and their active token holdings.
type PaymentMethodsService struct {
	http *httpClient
}

// PaymentMethodsQuery selects the merchant whose payment methods to list.
// Provide EXACTLY ONE handle: AccountID returns all the merchant's active
// wallets; Address returns just that one wallet. Both empty (or both set) is a
// client error the gateway rejects with 400.
type PaymentMethodsQuery struct {
	AccountID string // merchant account UUID — all its active wallets
	Address   string // a merchant wallet address — just that wallet
}

// List returns the merchant's active wallets, each with its active token
// holdings nested under Tokens (WalletWithTokens) — the same wallet-centric
// shape as WalletsService.List, but public and scoped by the query rather than
// an account path. An unknown account/address yields an empty slice. No JWT.
func (s *PaymentMethodsService) List(ctx context.Context, query PaymentMethodsQuery) ([]WalletWithTokens, error) {
	q := url.Values{}
	if query.AccountID != "" {
		q.Set("account_id", query.AccountID)
	}
	if query.Address != "" {
		q.Set("address", query.Address)
	}
	path := "/payment_methods"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	// The endpoint returns a bare JSON array (no pagination envelope or headers),
	// scoped to a single merchant/wallet, so a plain GET into the slice suffices.
	var out []WalletWithTokens
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}
