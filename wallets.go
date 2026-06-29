package rail0

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// WalletsService exposes an account's wallets (requires JWT).
//
// Wallets live under /accounts/{account_id}/wallets, so every method takes the
// account id as its first argument. List returns the account's wallets, each
// with its token holdings nested under Tokens (WalletWithTokens) — a wallet with
// no tokens is still returned (Tokens is empty), never omitted. Get/Create/
// Update return the plain Wallet record. The verb set mirrors PaymentsService
// and WebhooksService (List/Get/Create/Update/Delete) for a consistent SDK.
type WalletsService struct {
	http *httpClient
}

// ListWalletsParams holds optional query parameters for WalletsService.List.
// ChainID/TokenSymbol restrict the nested tokens only (a wallet is still
// returned with an empty Tokens when none match); Active filters the wallets.
type ListWalletsParams struct {
	ChainID     int    // restrict nested tokens to this chain id; 0 = no filter
	TokenSymbol string // restrict nested tokens to this symbol (e.g. "USDC")
	Active      *bool  // filter wallets by active flag; nil = no filter
	Sort        string // sort fields: comma-separated, prefix with - for desc
	Page        int    // page number (1-based); 0 = use server default
	PerPage     int    // items per page; 0 = use server default
}

// List returns the account's wallets, each with its token holdings nested under
// Tokens (requires JWT).
func (s *WalletsService) List(ctx context.Context, accountID string, params ...ListWalletsParams) (*PaginatedResponse[WalletWithTokens], error) {
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
	// List endpoints return a bare JSON array with pagination in headers, not a
	// {data,meta} envelope — decode into the slice and read the meta back.
	var data []WalletWithTokens
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[WalletWithTokens]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

// PaymentMethodsParams filters the payment methods returned by
// WalletsService.PaymentMethods.
type PaymentMethodsParams struct {
	ChainID     int    // restrict to this chain id; 0 = all chains
	TokenSymbol string // restrict to this token symbol (e.g. "USDC"); "" = all
}

// PaymentMethod is one concrete way to pay a merchant: send Token, on chain
// ChainID, to the wallet Address. It is the flattened (address × token × chain)
// projection of the account's active wallets and their active token holdings.
type PaymentMethod struct {
	Address string // active wallet address — the payee to pay
	ChainID int    // EVM chain id the token lives on
	Token   Token  // token to pay with (symbol, address, decimals, chain_id)
}

// PaymentMethods returns the account's available payment methods: the active
// wallet addresses with their active token holdings, flattened to one entry per
// (address, token, chain) and optionally filtered by chain and/or token symbol.
//
// It is a convenience over List for a payer choosing where/how to pay a
// merchant: it requests only active wallets, pages through all of them, drops
// inactive token holdings, and naturally omits wallets left with no matching
// token. Requires JWT.
func (s *WalletsService) PaymentMethods(ctx context.Context, accountID string, params ...PaymentMethodsParams) ([]PaymentMethod, error) {
	var p PaymentMethodsParams
	if len(params) > 0 {
		p = params[0]
	}
	active := true
	const perPage = 100
	methods := []PaymentMethod{}
	for page := 1; ; page++ {
		resp, err := s.List(ctx, accountID, ListWalletsParams{
			Active:      &active,
			ChainID:     p.ChainID,
			TokenSymbol: p.TokenSymbol,
			Page:        page,
			PerPage:     perPage,
		})
		if err != nil {
			return nil, err
		}
		for _, w := range resp.Data {
			for _, h := range w.Tokens {
				if !h.Active {
					continue
				}
				methods = append(methods, PaymentMethod{
					Address: w.Address,
					ChainID: h.Token.ChainID,
					Token:   h.Token,
				})
			}
		}
		// Last page reached: a short (or empty) page, or the total is covered.
		if len(resp.Data) < perPage || page*perPage >= resp.Meta.Total {
			break
		}
	}
	return methods, nil
}

// Get fetches a single wallet by its id or 0x address (requires JWT). The
// address is unique per account, so `idOrAddress` may be either the wallet UUID
// or its address. Returns the wallet record (not a wallet-token row).
func (s *WalletsService) Get(ctx context.Context, accountID, idOrAddress string) (*Wallet, error) {
	var out Wallet
	if err := s.http.get(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, idOrAddress), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWalletRequest is the body for WalletsService.Create.
type CreateWalletRequest struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
}

// Create adds a new wallet to the account (requires JWT). Returns the plain
// Wallet record (not a wallet-token row).
func (s *WalletsService) Create(ctx context.Context, accountID string, req CreateWalletRequest) (*Wallet, error) {
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

// Update patches label or active on a wallet (requires JWT). `id` may be the
// wallet UUID or its 0x address (unique per account).
func (s *WalletsService) Update(ctx context.Context, accountID, id string, req UpdateWalletRequest) (*Wallet, error) {
	var out Wallet
	if err := s.http.patch(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a wallet (requires JWT). `id` may be the wallet UUID or its 0x
// address (unique per account). Returns 204 No Content.
func (s *WalletsService) Delete(ctx context.Context, accountID, id string) error {
	return s.http.delete(ctx, fmt.Sprintf("/accounts/%s/wallets/%s", accountID, id), nil)
}

// WalletBalancesParams holds optional query parameters for WalletsService.Balances.
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
func (s *WalletsService) Balances(ctx context.Context, accountID, id string, params ...WalletBalancesParams) (*WalletBalances, error) {
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
