package rail0

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ================================================================
//  Payouts (merchant cash-out, level 1)
// ================================================================
//
// A payout moves a merchant wallet's tokens to a deposit address at a venue of the
// merchant's own (an exchange account, Circle Mint, another wallet they control). The
// gateway does the on-chain leg and nothing else: it builds an unsigned ERC-20
// transfer(address,uint256) from the wallet to a registered destination, and the
// merchant signs it. It never holds funds or keys, and never talks to the venue.
//
// The flow:
//
//  1. Register a destination (PayoutDestinations.Create) — bound to ONE chain and ONE
//     token, with ConfirmDetails set to true after the merchant checked the details at
//     the venue. It is born "unverified".
//  2. Send the proof: Payouts.Prepare with Purpose PayoutPurposeProof, sign, submit.
//     Once it is mined and the venue credited it, PayoutDestinations.Confirm marks the
//     destination "verified".
//  3. Pay out: Payouts.Prepare with an Amount (a human decimal, or PayoutAmountAll for
//     the balance minus the refund reserve), verify, sign, submit. PrepareAndSubmit and
//     SignAndSubmit run the verify → sign → submit steps for a private key.
//
// Every route here is the session wallet's (JWT), scoped to its account.

// Payout purposes (PreparePayoutRequest.Purpose, Payout.Purpose).
const (
	// PayoutPurposePayout moves the amount asked for (the default).
	PayoutPurposePayout = "payout"
	// PayoutPurposeProof moves the gateway's proof amount (1 token unit by default),
	// raised to the destination's MinAmount when that is higher.
	PayoutPurposeProof = "proof"
)

// PayoutAmountAll asks Prepare for the wallet's whole balance minus the refund reserve
// (the destination's ReserveAmount, or SuggestedReserve when none is set).
const PayoutAmountAll = "all"

// Payout statuses (Payout.Status).
const (
	PayoutStatusPending   = "pending"
	PayoutStatusSubmitted = "submitted"
	PayoutStatusConfirmed = "confirmed"
	PayoutStatusFailed    = "failed"
)

// Payout destination statuses (PayoutDestination.Status).
const (
	PayoutDestinationStatusUnverified = "unverified"
	PayoutDestinationStatusVerified   = "verified"
	PayoutDestinationStatusDisabled   = "disabled"
)

// Payout destination kinds (CreatePayoutDestinationRequest.Kind). Informational: the
// gateway treats every kind the same.
const (
	PayoutDestinationKindExchange    = "exchange"
	PayoutDestinationKindCircleMint  = "circle_mint"
	PayoutDestinationKindSelfCustody = "self_custody"
	PayoutDestinationKindProvider    = "provider"
)

// The text a merchant confirms when registering a destination, or when changing its
// minimum (ConfirmDetails on CreatePayoutDestinationRequest / UpdatePayoutDestinationRequest).
// The gateway records the version — never the text — in the destination.created /
// destination.min_amount_changed event as changes.confirmed_details {version, by, at}.
// A new wording gets a new version. Show PayoutDetailsConfirmationText to the merchant
// and set ConfirmDetails only on their explicit agreement.
const (
	PayoutDetailsConfirmationVersion = "details_v1"
	PayoutDetailsConfirmationText    = "I have checked the address, network, token and minimum deposit on the venue's deposit page and confirm they are correct. Funds sent to a wrong address or network, or below the venue's minimum, may be permanently lost and cannot be recovered by rail0."
)

// ErrPayoutMinimumRequired is returned, before any request, by PayoutDestinations.Create
// when MinAmount is empty and by PayoutDestinations.Update when MinAmount points to "".
// The venue's deposit minimum is a required, explicit answer: "0" when it has none.
var ErrPayoutMinimumRequired = errors.New(`rail0: MinAmount is required: the venue's deposit minimum in base units, "0" when the venue has none`)

// PayoutDestination, Payout and PayoutEvent are generated from the gateway OpenAPI
// schema in types_gen.go.

// ── Destinations ─────────────────────────────────────────────────────────────

// PayoutDestinationsService manages the account's payout destinations
// (/payout_destinations, JWT).
type PayoutDestinationsService struct {
	http *httpClient
}

// ListPayoutDestinationsParams holds optional query parameters for
// PayoutDestinations.List.
type ListPayoutDestinationsParams struct {
	WalletID string // restrict to one wallet (uuid)
	Status   string // "unverified", "verified" or "disabled"
	Sort     string // sort fields: comma-separated, prefix with - for desc
	Page     int    // page number (1-based); 0 = use server default
	PerPage  int    // items per page; 0 = use server default
}

// List returns the account's payout destinations — every wallet's, not only the
// session's. Paged like Payments.List (bare array, pagination in headers).
func (s *PayoutDestinationsService) List(ctx context.Context, params ...ListPayoutDestinationsParams) (*PaginatedResponse[PayoutDestination], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "wallet_id", p.WalletID)
		setNonEmpty(q, "status", p.Status)
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[PayoutDestination](ctx, s.http, withQuery("/payout_destinations", q))
}

// CreatePayoutDestinationRequest is the body of PayoutDestinations.Create.
//
// The destination is bound to ONE chain and ONE token by design: an EVM address is the
// same string on every chain, but a venue credits only the network it issued the
// address for — USDC sent on another chain to that address is usually lost. Generate
// the deposit address at the venue WITH THE NETWORK SET TO ChainID.
type CreatePayoutDestinationRequest struct {
	// The wallet (uuid or address) that will sign transfers to it; must be the
	// session's own.
	WalletID string `json:"wallet_id"`
	// The chain the deposit address was issued for.
	ChainID int `json:"chain_id"`
	// The token's address on that chain.
	Token Address `json:"token"`
	// The venue's deposit address.
	Address Address `json:"address"`
	Label   string  `json:"label,omitempty"`
	// One of the PayoutDestinationKind* constants; empty = "exchange".
	Kind string `json:"kind,omitempty"`
	// Base units kept in the wallet on a full-balance payout (digits only, below 2^120);
	// empty = the gateway's suggestion (the refundable balance still inside its window).
	ReserveAmount Uint256String `json:"reserve_amount,omitempty"`
	// REQUIRED: the venue's deposit minimum in base units (digits only, below 2^120), read
	// from its deposit page; "0" when the venue has none. It is ALWAYS sent and never
	// defaulted by the SDK — the merchant must state it. Create refuses an empty value
	// locally (the gateway would answer 422 minimum_required). A payout below it is
	// refused (payout_below_minimum) and the proof is raised to it.
	MinAmount Uint256String `json:"min_amount"`
	// ConfirmDetails is the merchant's attestation, and the gateway refuses the request
	// (422 confirmation_required) unless it is true. It is ALWAYS sent, never defaulted
	// to true by the SDK: set it only after the merchant read and accepted the
	// details_v1 text (PayoutDetailsConfirmationText):
	//
	//	"I have checked the address, network, token and minimum deposit on the venue's
	//	deposit page and confirm they are correct. Funds sent to a wrong address or
	//	network, or below the venue's minimum, may be permanently lost and cannot be
	//	recovered by rail0."
	ConfirmDetails bool `json:"confirm_details"`
}

// Create registers a payout destination for the session's wallet
// (POST /payout_destinations). It is born "unverified": send the proof
// (Payouts.Prepare with PayoutPurposeProof) and Confirm once the venue credited it.
//
// Refused up front (422): the zero address, the wallet itself, and every contract the
// gateway knows on that chain (payout_destination_zero_address, _own_wallet,
// _is_contract); invalid_amount for a malformed ReserveAmount / MinAmount;
// minimum_required when MinAmount is blank; confirmation_required when ConfirmDetails
// is false. 409 payout_destination_exists when the wallet already has this address for
// this token and chain.
//
// An empty MinAmount is refused before any request (ErrPayoutMinimumRequired): the
// minimum is the merchant's statement, so the SDK never fills in "0" on its own.
func (s *PayoutDestinationsService) Create(ctx context.Context, req CreatePayoutDestinationRequest) (*PayoutDestination, error) {
	if req.MinAmount == "" {
		return nil, ErrPayoutMinimumRequired
	}
	var out PayoutDestination
	if err := s.http.post(ctx, "/payout_destinations", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get reads one payout destination (GET /payout_destinations/{id}). Another account's
// id answers 404 like an unknown one.
func (s *PayoutDestinationsService) Get(ctx context.Context, id string) (*PayoutDestination, error) {
	return getPayoutDestination(ctx, s.http, id)
}

// getPayoutDestination is Get without the service, so the payout signer can read the
// destination it verifies against without holding a second service.
func getPayoutDestination(ctx context.Context, h *httpClient, id string) (*PayoutDestination, error) {
	var out PayoutDestination
	if err := h.get(ctx, pathf("/payout_destinations/%s", id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdatePayoutDestinationRequest holds the fields PayoutDestinations.Update may change.
// A nil pointer leaves the field as it is.
type UpdatePayoutDestinationRequest struct {
	Label *string `json:"label,omitempty"`
	// Base units; a pointer to "" restores the gateway's suggestion.
	ReserveAmount *Uint256String `json:"reserve_amount,omitempty"`
	// The venue's deposit minimum, base units; a pointer to "0" says the venue has none
	// (that is how a minimum is removed). A pointer to "" is refused before any request
	// (ErrPayoutMinimumRequired; the gateway answers 422 minimum_required). CHANGING it
	// requires ConfirmDetails (422 confirmation_required otherwise, nothing written).
	// Raising it above the destination's latest proof takes the verification away: the
	// destination goes back to "unverified" and needs a new proof at the new minimum.
	MinAmount *Uint256String `json:"min_amount,omitempty"`
	// ConfirmDetails must be true when the request changes MinAmount (re-sending the
	// same value, and Label / ReserveAmount / Active, need nothing). Set it only after
	// the merchant accepted the details_v1 text (PayoutDetailsConfirmationText):
	//
	//	"I have checked the address, network, token and minimum deposit on the venue's
	//	deposit page and confirm they are correct. Funds sent to a wrong address or
	//	network, or below the venue's minimum, may be permanently lost and cannot be
	//	recovered by rail0."
	//
	// Omitted from the request when false.
	ConfirmDetails bool `json:"confirm_details,omitempty"`
	// false disables the destination, true re-enables it (a verified one stays verified).
	Active *bool `json:"active,omitempty"`
}

// Update changes a payout destination (PATCH /payout_destinations/{id}).
//
// A destination registered before the minimum was required may have none stated
// (MinAmount empty in the response): it sends nothing — Payouts.Prepare and Submit
// answer 422 payout_destination_minimum_unset — until Update sets MinAmount ("0" when
// the venue has none, with ConfirmDetails).
//
// Only the destination's own wallet may edit or re-enable it; any wallet of the account
// may disable it (Active=false alone). Disabling settles its pending payout first: an
// unsigned one fails "cancelled", a signed one refuses the switch-off with 409
// payout_already_signed (resend it or let it settle, then disable). A deactivated
// account is read-only: every change is 403 account_deactivated.
func (s *PayoutDestinationsService) Update(ctx context.Context, id string, req UpdatePayoutDestinationRequest) (*PayoutDestination, error) {
	if req.MinAmount != nil && *req.MinAmount == "" {
		return nil, ErrPayoutMinimumRequired
	}
	var out PayoutDestination
	if err := s.http.patch(ctx, pathf("/payout_destinations/%s", id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Confirm records the merchant's attestation that the venue credited the proof
// transfer (POST /payout_destinations/{id}/confirm): the destination becomes
// "verified". The proof must be confirmed on-chain first (422
// payout_destination_proof_missing); a disabled destination is
// payout_destination_disabled.
func (s *PayoutDestinationsService) Confirm(ctx context.Context, id string) (*PayoutDestination, error) {
	var out PayoutDestination
	if err := s.http.post(ctx, pathf("/payout_destinations/%s/confirm", id), struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPayoutEventsParams holds optional query parameters for the payout audit trails
// (PayoutDestinations.Events, Payouts.Events).
type ListPayoutEventsParams struct {
	Page    int // page number (1-based); 0 = use server default (25 per page, capped at 100)
	PerPage int // items per page; 0 = use server default
}

// Events lists a destination's audit trail (GET /payout_destinations/{id}/events): its
// own events AND those of every payout made to it, newest first, paged.
func (s *PayoutDestinationsService) Events(ctx context.Context, id string, params ...ListPayoutEventsParams) (*PaginatedResponse[PayoutEvent], error) {
	return listPayoutEvents(ctx, s.http, pathf("/payout_destinations/%s/events", id), params...)
}

// ── Payouts ──────────────────────────────────────────────────────────────────

// PayoutsService prepares, submits and reads the account's payouts (/payouts, JWT).
type PayoutsService struct {
	http *httpClient
}

// ListPayoutsParams holds optional query parameters for Payouts.List.
//
// Status and Statuses combine as on ListPaymentsParams: the union is sent as one
// comma-separated `status=` value, matching any of them.
type ListPayoutsParams struct {
	DestinationID string   // only payouts to this destination (uuid)
	WalletID      string   // only payouts from this wallet (uuid)
	Status        string   // filter by one payout status
	Statuses      []string // filter by several payout statuses; unioned with Status
	Purpose       string   // "payout" or "proof"
	CreatedFrom   string   // only payouts created at/after this ISO-8601 time
	CreatedTo     string   // only payouts created at/before this ISO-8601 time
	Sort          string   // sort fields: comma-separated, prefix with - for desc
	Page          int      // page number (1-based); 0 = use server default
	PerPage       int      // items per page; 0 = use server default
	// After switches to cursor paging, exactly as on ListPaymentsParams: the
	// Meta.NextCursor of the previous page; "" = offset paging.
	After string
}

// List returns the account's payout history, newest first by default, paged like
// Payments.List (bare array, pagination in headers).
func (s *PayoutsService) List(ctx context.Context, params ...ListPayoutsParams) (*PaginatedResponse[Payout], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "destination_id", p.DestinationID)
		setNonEmpty(q, "wallet_id", p.WalletID)
		setNonEmpty(q, "status", statusFilter(p.Status, p.Statuses))
		setNonEmpty(q, "purpose", p.Purpose)
		setNonEmpty(q, "created_from", p.CreatedFrom)
		setNonEmpty(q, "created_to", p.CreatedTo)
		setPaging(q, p.Sort, p.Page, p.PerPage)
		setNonEmpty(q, "after", p.After)
	}
	return listPage[Payout](ctx, s.http, withQuery("/payouts", q))
}

// PreparePayoutRequest is the body of Payouts.Prepare.
type PreparePayoutRequest struct {
	DestinationID string `json:"destination_id"`
	// PayoutPurposePayout (the default when empty) or PayoutPurposeProof.
	Purpose string `json:"purpose,omitempty"`
	// Required for a payout: a human decimal ("125.50") or PayoutAmountAll. Ignored for
	// a proof, whose amount the gateway sets.
	Amount string `json:"amount,omitempty"`
}

// Prepare builds the unsigned ERC-20 transfer for a payout (POST /payouts/prepare).
//
// The returned Payout carries UnsignedTransaction (JSON of an EIP-1559 transaction:
// `to` = the token, `data` = transfer(address,uint256), value 0) BESIDE its decoded
// intent (ChainID, Token, Address, Amount in base units). Never sign it blindly: check
// it with VerifyPayoutTransfer (or sign it with SignPayoutTransfer, which checks first),
// or let SignAndSubmit / PrepareAndSubmit do it. Then either Submit the signed bytes
// (the gateway broadcasts) or broadcast it yourself and report the hash with Submitted.
//
// One pending payout per destination: a re-prepare replaces it while unsigned (HTTP 200
// instead of 201); once a signature is stored it is refused with 409
// payout_already_signed — Resend that one instead. Other refusals (422):
// payout_destination_unverified (an unverified destination takes the proof amount at
// most), payout_destination_disabled, payout_below_minimum, insufficient_balance,
// payout_limit_reached.
func (s *PayoutsService) Prepare(ctx context.Context, req PreparePayoutRequest) (*Payout, error) {
	var out Payout
	if err := s.http.post(ctx, "/payouts/prepare", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get reads one payout (GET /payouts/{id}).
func (s *PayoutsService) Get(ctx context.Context, id string) (*Payout, error) {
	var out Payout
	if err := s.http.get(ctx, pathf("/payouts/%s", id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Events lists a payout's audit trail (GET /payouts/{id}/events), newest first, paged.
func (s *PayoutsService) Events(ctx context.Context, id string, params ...ListPayoutEventsParams) (*PaginatedResponse[PayoutEvent], error) {
	return listPayoutEvents(ctx, s.http, pathf("/payouts/%s/events", id), params...)
}

// Submit hands the signed transfer to the gateway, which stores it and broadcasts it
// (POST /payouts/{id}/submit; HTTP 202, now "submitted").
//
// The signed bytes and their hash are stored BEFORE the broadcast, so from here on the
// payout is Signed and carries its TransactionHash whatever the node answers. A node
// refusal is a 502 whose code names the reason (insufficient_gas_funds, …) and leaves
// the payout "pending": Resend it. Different bytes for a payout that already holds a
// signature are refused 409 payout_already_signed. Sign with SignPayoutTransfer.
func (s *PayoutsService) Submit(ctx context.Context, id string, signedTransaction string) (*Payout, error) {
	var out Payout
	body := SubmitTransactionRequest{SignedTransaction: signedTransaction}
	if err := s.http.post(ctx, pathf("/payouts/%s/submit", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Submitted reports a transfer the wallet signed and broadcast itself
// (POST /payouts/{id}/submitted; HTTP 202). A hash a node already knows must be THIS
// transfer — from the wallet, to the token, this amount to this destination
// (foreign_transfer_hash otherwise); one no node has seen yet is accepted and settled
// from its receipt. Also accepts a LATE hash for a payout that failed
// submission_timeout or dropped (a sped-up or replaced transaction), which puts it back
// to "submitted". A hash another payout records is 409 payout_hash_in_use.
func (s *PayoutsService) Submitted(ctx context.Context, id string, transactionHash string) (*Payout, error) {
	var out Payout
	body := SubmitByHashRequest{TransactionHash: transactionHash}
	if err := s.http.post(ctx, pathf("/payouts/%s/submitted", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Resend re-broadcasts the signed transfer a Submit stored (POST /payouts/{id}/resend;
// HTTP 202): the same bytes, so the same nonce and hash — it can be mined once at most.
// 422 payout_not_signed when nothing is stored (Submit first).
func (s *PayoutsService) Resend(ctx context.Context, id string) (*Payout, error) {
	var out Payout
	if err := s.http.post(ctx, pathf("/payouts/%s/resend", id), struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancel fails a pending payout the wallet has not signed (POST /payouts/{id}/cancel):
// it becomes "failed" with ErrorCode "cancelled" and the destination is free for a new
// prepare. A signed one is refused 409 payout_already_signed — its transaction may
// still be mined; resend it and let it settle.
func (s *PayoutsService) Cancel(ctx context.Context, id string) (*Payout, error) {
	var out Payout
	if err := s.http.post(ctx, pathf("/payouts/%s/cancel", id), struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SignAndSubmit verifies a prepared payout's transfer, signs it with privateKey and
// submits it (the gateway broadcasts). It is the prepare → verify → sign → submit
// sequence minus the prepare; PrepareAndSubmit adds that.
//
// What the transfer is held to is NOT read from the prepare response: the destination
// is fetched again (PayoutDestinations.Get) and the transaction must be exactly
// transfer(destination.Address, payout.Amount) on destination.Token, on
// destination.ChainID, value 0, sent from the key's own address (VerifyPayoutTransfer).
// A destination swapped for another address, token or chain is refused before the key
// touches anything. The amount is the payout's own (its decoded intent); to also hold it
// to an amount you typed, use PrepareAndSubmit or VerifyPayoutTransfer directly.
//
// A payout that is not "pending", or already Signed, is refused without a request: a
// signed one is Resend's (its bytes may already be in a mempool). A 502 from Submit
// leaves the payout pending and signed — Resend it.
func (s *PayoutsService) SignAndSubmit(ctx context.Context, payout *Payout, privateKey []byte) (*Payout, error) {
	if payout == nil {
		return nil, errors.New("rail0: SignAndSubmit: payout is nil")
	}
	if payout.Status != "" && payout.Status != PayoutStatusPending {
		return nil, fmt.Errorf("rail0: payout %s is %s, not pending: nothing to sign", payout.ID, payout.Status)
	}
	if payout.Signed {
		return nil, fmt.Errorf("rail0: payout %s already holds a signed transaction: resend it (Payouts.Resend) instead of signing again", payout.ID)
	}
	if payout.UnsignedTransaction == "" {
		return nil, fmt.Errorf("rail0: payout %s carries no unsigned_transaction (prepare it first)", payout.ID)
	}
	if payout.DestinationID == "" {
		return nil, refuse("payout %s names no destination, so its transfer cannot be checked", payout.ID)
	}
	dest, err := getPayoutDestination(ctx, s.http, payout.DestinationID)
	if err != nil {
		return nil, fmt.Errorf("rail0: read payout destination %s: %w", payout.DestinationID, err)
	}
	signed, err := SignPayoutTransfer(payout.UnsignedTransaction, privateKey, PayoutTransferExpectationFor(dest, payout.Amount))
	if err != nil {
		return nil, err
	}
	return s.Submit(ctx, payout.ID, signed)
}

// PrepareAndSubmit prepares a payout or a proof, verifies the transfer, signs it with
// privateKey and submits it — the whole cash-out step for a key held locally. It
// returns the submitted payout ("submitted", with its TransactionHash).
//
// On top of SignAndSubmit's checks it holds the prepared payout to the request: the
// same destination and purpose, and — for a payout with a decimal Amount (not
// PayoutAmountAll) — the same base units, scaled by the token's decimals from the
// public token catalog. A proof's and an "all" payout's amount are the gateway's to
// compute, so for those the payout's own amount is the one checked against the
// calldata.
//
// When anything after the prepare fails, the prepared (unsigned) payout stays pending
// on the gateway: prepare again (it is replaced) or Cancel it.
func (s *PayoutsService) PrepareAndSubmit(ctx context.Context, req PreparePayoutRequest, privateKey []byte) (*Payout, error) {
	// Fail on a bad key before the gateway builds anything.
	if _, err := SignerAddress(privateKey); err != nil {
		return nil, err
	}
	payout, err := s.Prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(payout.DestinationID, req.DestinationID) {
		return nil, refuse("the prepared payout is for destination %s but you asked for %s", payout.DestinationID, req.DestinationID)
	}
	wantPurpose := req.Purpose
	if wantPurpose == "" {
		wantPurpose = PayoutPurposePayout
	}
	if payout.Purpose != wantPurpose {
		return nil, refuse("the prepared payout is a %s but you asked for a %s", payout.Purpose, wantPurpose)
	}
	if wantPurpose == PayoutPurposePayout && !strings.EqualFold(strings.TrimSpace(req.Amount), PayoutAmountAll) {
		units, err := s.expectedPayoutUnits(ctx, payout, req.Amount)
		if err != nil {
			return nil, err
		}
		if units != payout.Amount {
			return nil, refuse("the prepared payout moves %s base units but you asked for %s (%s)", payout.Amount, req.Amount, units)
		}
	}
	return s.SignAndSubmit(ctx, payout, privateKey)
}

// expectedPayoutUnits scales the human amount the caller typed by the token's decimals,
// read from the public catalog (GET /tokens?chain_id=) rather than from the prepare
// response. Fail-closed: a token missing from the catalog refuses.
func (s *PayoutsService) expectedPayoutUnits(ctx context.Context, payout *Payout, amount string) (string, error) {
	tokens, err := (&TokensService{http: s.http}).List(ctx, ListTokensParams{ChainID: payout.ChainID})
	if err != nil {
		return "", fmt.Errorf("rail0: read the token catalog: %w", err)
	}
	for _, t := range tokens {
		if t.ChainID == payout.ChainID && strings.EqualFold(string(t.Address), string(payout.Token)) {
			units, err := ToBaseUnits(amount, t.Decimals)
			if err != nil {
				return "", refuse("amount %q: %w", amount, err)
			}
			return units, nil
		}
	}
	return "", refuse("token %s on chain %d is not in the catalog, so the payout amount cannot be checked", payout.Token, payout.ChainID)
}

// ── shared list plumbing ─────────────────────────────────────────────────────

// listPayoutEvents reads one page of a payout audit trail.
func listPayoutEvents(ctx context.Context, h *httpClient, path string, params ...ListPayoutEventsParams) (*PaginatedResponse[PayoutEvent], error) {
	q := url.Values{}
	if len(params) > 0 {
		setPaging(q, "", params[0].Page, params[0].PerPage)
	}
	return listPage[PayoutEvent](ctx, h, withQuery(path, q))
}

// listPage GETs a collection endpoint (bare JSON array, pagination in the response
// headers) and wraps it as a PaginatedResponse, as Payments.List does.
func listPage[T any](ctx context.Context, h *httpClient, path string) (*PaginatedResponse[T], error) {
	var data []T
	hdr, err := h.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[T]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}

func setNonEmpty(q url.Values, key, v string) {
	if v != "" {
		q.Set(key, v)
	}
}

func setPaging(q url.Values, sort string, page, perPage int) {
	setNonEmpty(q, "sort", sort)
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if perPage > 0 {
		q.Set("per_page", strconv.Itoa(perPage))
	}
}
