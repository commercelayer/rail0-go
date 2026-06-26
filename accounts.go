package rail0

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// AccountsService exposes an account's wallet listing (requires JWT).
//
// Wallets live under /accounts/{account_id}/wallets. The list and single-get
// endpoints return "wallet token" join rows (WalletToken) — one row per
// (wallet, token) pairing on a chain — while create/update return the plain
// Wallet record.
type AccountsService struct {
	http *httpClient
}

// ListWalletsParams holds optional query parameters for AccountsService.Wallets.
type ListWalletsParams struct {
	ChainID     int    // filter by chain id; 0 = no filter
	TokenSymbol string // filter by token symbol (e.g. "USDC")
	Active      *bool  // filter by active flag; nil = no filter
	Sort        string // sort fields: comma-separated, prefix with - for desc
	Page        int    // page number (1-based); 0 = use server default
	PerPage     int    // items per page; 0 = use server default
}

// Wallets lists the account's wallet-token rows (requires JWT).
func (s *AccountsService) Wallets(ctx context.Context, accountID string, params ...ListWalletsParams) (*PaginatedResponse[WalletToken], error) {
	var out PaginatedResponse[WalletToken]
	path := fmt.Sprintf("/accounts/%s/wallets", accountID)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.ChainID > 0 {
			q.Set("chain_id", strconv.Itoa(p.ChainID))
		}
		if p.TokenSymbol != "" {
			q.Set("token_symbol", p.TokenSymbol)
		}
		if p.Active != nil {
			q.Set("active", strconv.FormatBool(*p.Active))
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

// GetWallet fetches a single wallet by its id or 0x address (requires JWT).
// The address is unique per account, so `idOrAddress` may be either the wallet
// UUID or its address. Returns the wallet record (not a wallet-token row).
func (s *AccountsService) GetWallet(ctx context.Context, accountID, idOrAddress string) (*Wallet, error) {
	var out Wallet
	if err := s.http.get(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, idOrAddress), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWalletRequest is the body for AccountsService.CreateWallet.
type CreateWalletRequest struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
}

// CreateWallet adds a new wallet to the account (requires JWT). Returns the
// plain Wallet record (not a wallet-token row).
func (s *AccountsService) CreateWallet(ctx context.Context, accountID string, req CreateWalletRequest) (*Wallet, error) {
	var out Wallet
	if err := s.http.post(ctx, fmt.Sprintf("/accounts/%s/wallets", accountID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWalletRequest holds the fields that can be patched on a wallet.
type UpdateWalletRequest struct {
	Label  string `json:"label,omitempty"`
	Active *bool  `json:"active,omitempty"`
}

// UpdateWallet patches label or active on a wallet (requires JWT). `id` may be
// the wallet UUID or its 0x address (unique per account).
func (s *AccountsService) UpdateWallet(ctx context.Context, accountID, id string, req UpdateWalletRequest) (*Wallet, error) {
	var out Wallet
	if err := s.http.patch(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWallet removes a wallet (requires JWT). `id` may be the wallet UUID or
// its 0x address (unique per account). Returns 204 No Content.
func (s *AccountsService) DeleteWallet(ctx context.Context, accountID, id string) error {
	return s.http.delete(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, id), nil)
}

// WalletBalancesParams holds optional query parameters for AccountsService.Balances.
type WalletBalancesParams struct {
	ChainID     int    // restrict to one chain id; 0 = all configured chains
	TokenSymbol string // restrict tokens to this symbol; "" = all active tokens
}

// Balances reads a wallet's live on-chain balances across the configured chains
// (requires JWT). Each ChainBalance carries the native gas-token balance and the
// active ERC-20 token balances, or an Error when that chain's RPC was
// unreachable — the per-chain error is reported in-band rather than failing the
// whole call, so one dead RPC never hides the other chains' balances.
// `id` may be the wallet UUID or its 0x address (unique per account).
func (s *AccountsService) Balances(ctx context.Context, accountID, id string, params ...WalletBalancesParams) (*WalletBalances, error) {
	var out WalletBalances
	path := fmt.Sprintf("/accounts/%s/wallets/%s/balances", accountID, id)
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.ChainID > 0 {
			q.Set("chain_id", strconv.Itoa(p.ChainID))
		}
		if p.TokenSymbol != "" {
			q.Set("token_symbol", p.TokenSymbol)
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
