package rail0

import (
	"context"
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
//
// A session is bound to the wallet it signed in with. Reads (List, Get,
// Balances) are account-wide, but every write on an existing wallet — Update,
// Delete and the token calls — acts only on the session's own wallet: another
// wallet of the same account is refused 403 not_your_wallet (an operator session
// is exempt). Create is suspended on the gateway for every session (403
// wallet_add_unavailable).
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
	Default     *bool  // restrict nested token holdings to the default one; nil = no filter
	TokenActive *bool  // restrict nested token holdings to this active flag; nil = no filter
	Sort        string // sort fields: comma-separated, prefix with - for desc
	Page        int    // page number (1-based); 0 = use server default
	PerPage     int    // items per page; 0 = use server default
}

// List returns the account's wallets, each with its token holdings nested under
// Tokens (requires JWT).
func (s *WalletsService) List(ctx context.Context, accountID string, params ...ListWalletsParams) (*PaginatedResponse[WalletWithTokens], error) {
	path := pathf("/accounts/%s/wallets", accountID)
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
		if p.Default != nil {
			q.Set("default", strconv.FormatBool(*p.Default))
		}
		if p.TokenActive != nil {
			q.Set("token_active", strconv.FormatBool(*p.TokenActive))
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

// Buyer-facing payment-method discovery lives on the public PaymentMethodsService.

// Get fetches a single wallet by its id or 0x address (requires JWT). The
// address is unique per account, so `idOrAddress` may be either the wallet UUID
// or its address. Returns the wallet record (not a wallet-token row).
func (s *WalletsService) Get(ctx context.Context, accountID, idOrAddress string) (*Wallet, error) {
	var out Wallet
	if err := s.http.get(ctx, pathf("/accounts/%s/wallets/%s", accountID, idOrAddress), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWalletRequest is the body for WalletsService.Create.
//
// Adding a wallet now requires a SIWE proof-of-ownership of Address: Message is
// an EIP-4361 message carrying a fresh nonce (POST /auth/nonces) signed by the
// address's own key, and Signature is that signature. Build the pair with
// AuthService.ProveAddress (the same handshake as login), which the CLI does for
// you. The gateway rejects the request (422) if Signature does not recover to
// Address. Address/Message/Signature are required; Label is optional.
type CreateWalletRequest struct {
	Address   string `json:"address"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
	Label     string `json:"label,omitempty"`
}

// Create adds a new wallet to the account (requires JWT). The request must carry
// a SIWE proof-of-ownership of Address (see CreateWalletRequest / ProveAddress).
// Returns the plain Wallet record (not a wallet-token row).
//
// CURRENTLY UNAVAILABLE: the gateway refuses every session with 403
// wallet_add_unavailable until the merchant wallet-management section lands. The
// method and its request shape are kept because that section reuses them (owner
// only, with the same proof; an operator cannot add a wallet to another account).
func (s *WalletsService) Create(ctx context.Context, accountID string, req CreateWalletRequest) (*Wallet, error) {
	var out Wallet
	if err := s.http.post(ctx, pathf("/accounts/%s/wallets", accountID), req, &out); err != nil {
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
// wallet UUID or its 0x address (unique per account). Only the wallet the
// session signed in with: another wallet of the account is refused 403
// not_your_wallet (an operator may change any). Active=false on the account's
// only active wallet is refused with 422 last_active_wallet: login resolves the
// account from a wallet, so an account with none could neither act nor add one
// back.
//
// Active=true is the one write a deactivated wallet's own session may make: it
// switches the wallet back on when its owner switched it off. A wallet an operator
// switched off (Wallet.DeactivatedBy "operator", or empty on an older switch-off) is
// refused 403 wallet_deactivated_by_operator: only an operator lifts it.
func (s *WalletsService) Update(ctx context.Context, accountID, id string, req UpdateWalletRequest) (*Wallet, error) {
	var out Wallet
	if err := s.http.patch(ctx, pathf("/accounts/%s/wallets/%s", accountID, id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete deactivates a wallet (requires JWT) — a soft delete, the same as Update
// with Active=false; the row is kept because payments name the address. `id` may
// be the wallet UUID or its 0x address (unique per account). Returns 204 No
// Content, or 422 last_active_wallet for the account's only active wallet. Like
// Update, only the session's own wallet (403 not_your_wallet otherwise).
func (s *WalletsService) Delete(ctx context.Context, accountID, id string) error {
	return s.http.delete(ctx, pathf("/accounts/%s/wallets/%s", accountID, id), nil)
}

// AddWalletTokenRequest is the body of AddToken: the (chain, token) pair this wallet
// accepts, optionally as its default.
type AddWalletTokenRequest struct {
	ChainID int    `json:"chain_id"`
	Token   string `json:"token"`
	// Default makes this the wallet's default token. At most one default per wallet —
	// setting it clears the others. Pointer so `false` can be sent explicitly, since
	// omitting the field and clearing the flag are different requests.
	Default *bool `json:"default,omitempty"`
}

// The four calls below manage WHAT A WALLET ACCEPTS, which is what drives the public
// GET /payment_methods and gates payment creation: a wallet with no active holding is
// invisible to buyers and unusable as a payee. rail0-ts and rail0-ruby have had them
// since their wallet resources existed; without them a Go integration could create a
// wallet and then had to leave the SDK to finish configuring it. (#33)
//
// Owner only, and only on the wallet the session signed in with: another wallet of
// the same account is refused 403 not_your_wallet.
//
// `tokenID` on Remove/Enable/Disable is the TOKEN's UUID — WalletTokenHolding.TokenID,
// not an id of the holding row: the gateway looks the holding up by (wallet, token). A
// non-UUID is a clean 404.

// AddToken accepts a token (chain) on this wallet (requires JWT).
//
// An upsert on (wallet, token): a previously-disabled holding is reactivated rather
// than duplicated. The gateway answers 201 when it creates the holding and 200 when it
// reactivates or updates one; both return the holding, so this does not distinguish
// them. `id` may be the wallet UUID or its 0x address.
func (s *WalletsService) AddToken(ctx context.Context, accountID, id string, req AddWalletTokenRequest) (*WalletTokenHolding, error) {
	var out WalletTokenHolding
	if err := s.http.post(ctx, pathf("/accounts/%s/wallets/%s/tokens", accountID, id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveToken stops accepting a token on this wallet (requires JWT).
//
// A soft delete (204): the holding row survives with active:false and loses `default`,
// so its history is kept and EnableToken can bring it back.
func (s *WalletsService) RemoveToken(ctx context.Context, accountID, id, tokenID string) error {
	return s.http.delete(ctx, pathf("/accounts/%s/wallets/%s/tokens/%s", accountID, id, tokenID), nil)
}

// EnableToken re-enables an EXISTING holding (requires JWT). 404 when the wallet has
// none for that token — use AddToken to create one.
func (s *WalletsService) EnableToken(ctx context.Context, accountID, id, tokenID string) (*WalletTokenHolding, error) {
	return s.tokenAction(ctx, accountID, id, tokenID, "enable")
}

// DisableToken disables an EXISTING holding (requires JWT). Same effect as
// RemoveToken, but returns the holding rather than 204. 404 when absent.
func (s *WalletsService) DisableToken(ctx context.Context, accountID, id, tokenID string) (*WalletTokenHolding, error) {
	return s.tokenAction(ctx, accountID, id, tokenID, "disable")
}

// tokenAction is the shared shape of the two PATCH sub-actions — same path, same
// response, different verb in the URL. Mirrors WebhooksService.putAction, which exists
// for the same reason.
func (s *WalletsService) tokenAction(ctx context.Context, accountID, id, tokenID, action string) (*WalletTokenHolding, error) {
	var out WalletTokenHolding
	path := pathf("/accounts/%s/wallets/%s/tokens/%s/%s", accountID, id, tokenID, action)
	if err := s.http.patch(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	path := pathf("/accounts/%s/wallets/%s/balances", accountID, id)
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
