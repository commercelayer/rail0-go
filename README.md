# rail0-go

Go SDK for the [RAIL0](https://github.com/commercelayer/rail0) stablecoin payment gateway.

RAIL0 is an immutable EVM smart contract that brings the authorize → capture → refund lifecycle of card networks to stablecoin payments — no intermediaries, no protocol fees, no permission required. This SDK is a REST client for the RAIL0 gateway that sits in front of the contract.

## Requirements

Go ≥ 1.25

## Installation

```bash
go get github.com/rail0/go-sdk
```

## Quick start

`/payments`, `/accounts`, and `/analytics` are SIWE-gated, so log in first. `Auth.Login`
runs the full SIWE flow with a local private key and **stashes the JWT on the same
client**, so every subsequent call is authenticated — no manual header wiring.

```go
client := rail0.NewClient(rail0.ClientOptions{BaseURL: "https://api.rail0.xyz"})
ctx := context.Background()

// Authenticate (SIWE): this client is now authorized for the gated endpoints.
key, _ := rail0.HexToPrivateKey("0x…")
auth, _ := client.Auth.Login(ctx, key, "api.rail0.xyz")
_ = auth.AccountID

// Discover the merchant's wallets (each with its tokens nested) and pick one
wallets, _ := client.Wallets.List(ctx, auth.AccountID)
w := wallets.Data[0]   // a wallet
tk := w.Tokens[0].Token // one of its accepted tokens (token/chain)

// Create a payment (flat params)
resp, _ := client.Payments.Create(ctx, rail0.CreatePaymentRequest{
    ChainID: tk.ChainID,
    Mode:    "authorize",
    Amount:  "50.00", // human decimal amount
    Token:   string(tk.Address),
    Payer:   "0xBuyer...",
    Payee:   w.Address,
})
```

Already hold a JWT? Seed it with the `Token` option (or `client.SetToken(jwt)` later),
instead of running the SIWE flow:

```go
client := rail0.NewClient(rail0.ClientOptions{
    BaseURL: "https://api.rail0.xyz",
    Token:   "<jwt>", // sent as Authorization: Bearer <jwt> on every request
})
```

## Client services

| Service | Description |
|---------|-------------|
| `client.Auth` | SIWE authentication |
| `client.Health` | Gateway health check |
| `client.Chains` | Public blockchain catalog |
| `client.Tokens` | Public token catalog |
| `client.Wallets` | Wallet management under `/accounts/:id/wallets` (JWT) |
| `client.PaymentMethods` | Public discovery of a merchant's accepted wallets/tokens (`GET /payment_methods`, no JWT) |
| `client.Payments` | Full payment lifecycle |
| `client.Analytics` | Merchant sales analytics (JWT + account) |
| `client.Webhooks` | Webhook subscription management (JWT) |

## Authentication (SIWE)

```go
// 1. Get a nonce (POST /auth/nonces)
nonce, _ := client.Auth.GetNonce(ctx)
_ = nonce.Nonce // embed in the SIWE message

// 2. Build + sign the EIP-4361 message, then verify (POST /auth)
resp, _ := client.Auth.Verify(ctx, rail0.AuthRequest{
    Message:   siweMessage,
    Signature: "0x...",
})
// Verify (and Login) stash resp.Token on this client automatically — the SAME
// client is now authenticated for the gated endpoints.

// Or do the whole flow with a raw private key:
resp, _ = client.Auth.Login(ctx, privKeyBytes, "api.rail0.xyz")

// Login embeds SIWE chain id 1 by default. Pass an explicit chain id to match a
// gateway whose SIWE_CHAIN_ID policy differs:
resp, _ = client.Auth.Login(ctx, privKeyBytes, "api.rail0.xyz", 5042002)

// The same SIWE handshake, without logging in, produces a proof-of-ownership of
// an address for Wallets.Create (see "Wallets" below). ProveAddress fetches a
// nonce and signs the identical message with the given key, but does NOT touch
// the client's token:
proof, _ := client.Auth.ProveAddress(ctx, walletKeyBytes, "api.rail0.xyz")
```

The JWT lives on the shared transport, so all services see it at once. Set it three
ways — the `Token` client option, `client.SetToken(jwt)` at any time (pass `""` to
clear), or automatically via `Auth.Login` / `Auth.Verify`. A per-request header still
overrides it.

```go
client.SetToken(resp.Token) // apply a token to an already-built client
```

## Catalog (public)

```go
chains, _ := client.Chains.List(ctx)                                      // GET /blockchains
tokens, _ := client.Tokens.List(ctx, rail0.ListTokensParams{ChainID: 8453}) // GET /tokens?chain_id=8453
```

## Health

```go
h, _ := client.Health.Get(ctx) // GET /health
fmt.Println(h.Status, h.ActiveChains)
```

## Wallets (under accounts, JWT)

All wallet endpoints are behind SIWE — a merchant manages **its own** wallets
here. The list endpoint returns the account's wallets, each with its token
holdings nested under `Tokens` (`rail0.WalletWithTokens`) — a wallet with no
tokens is still returned (`Tokens` empty), never omitted. Single-get/create/
update return the plain `rail0.Wallet`. (Buyer-facing discovery of a merchant's
wallets is public and lives on `client.PaymentMethods`, below.)

```go
// List wallets, each with its tokens nested. ChainID/TokenSymbol restrict the
// nested tokens (the wallet is still returned, possibly with no tokens); Active
// filters the wallets.
active := true
wallets, _ := client.Wallets.List(ctx, accountID, rail0.ListWalletsParams{
    ChainID:     8453,
    TokenSymbol: "USDC",
    Active:      &active,
    Sort:        "-created_at",
})
// wallets.Data[i].Tokens[j].Token.Symbol

// Get a single wallet by id or 0x address (address is unique per account)
w, _ := client.Wallets.Get(ctx, accountID, "0xABC…")

// Create a wallet — requires a SIWE proof-of-ownership of the address being
// added. Build it with Auth.ProveAddress using the KEY OF THAT ADDRESS (the same
// SIWE handshake as login; it fetches a nonce and signs, but does NOT log the
// client in). The added address need not equal the session address — a merchant
// may register several payee wallets — but you must hold each added address's key.
proof, _ := client.Auth.ProveAddress(ctx, walletKeyBytes, "api.rail0.xyz")
w, _ := client.Wallets.Create(ctx, accountID, rail0.CreateWalletRequest{
    Address:   "0xABC...",
    Message:   proof.Message,
    Signature: proof.Signature,
    Label:     "Treasury",
})

// Update a wallet (label / active)
client.Wallets.Update(ctx, accountID, w.ID, rail0.UpdateWalletRequest{Label: "Main"})

// Delete a wallet
client.Wallets.Delete(ctx, accountID, w.ID)

// Read a wallet's live on-chain balances (one entry per chain; each carries the
// native balance and active token balances, or a per-chain Error if that RPC was
// unreachable). Filters are optional.
bal, _ := client.Wallets.Balances(ctx, accountID, walletID, rail0.WalletBalancesParams{
    ChainID:     84532,
    TokenSymbol: "USDC",
})
for _, c := range bal.Balances {
    if c.Error.Code != "" {
        continue // RPC unreachable for this chain
    }
    fmt.Println(c.ChainID, c.Native.Amount, len(c.Tokens))
}
```

## Payment methods (public)

Public, buyer-facing discovery of a merchant's accepted payment methods — a
payer that only knows the merchant can list the active wallet/token combinations
it accepts, without holding the merchant's session. Provide **exactly one** of
`AccountID` (all the merchant's active wallets) or `Address` (just that one
wallet); it returns `[]rail0.WalletWithTokens` (only active wallets and active
holdings). No JWT required.

```go
// By merchant account id — all the merchant's active wallets.
methods, _ := client.PaymentMethods.List(ctx, rail0.PaymentMethodsQuery{AccountID: accountID})

// Or by a single wallet address the payer already knows.
methods, _ = client.PaymentMethods.List(ctx, rail0.PaymentMethodsQuery{Address: "0xABC…"})

for _, w := range methods {
    for _, h := range w.Tokens {
        // pay h.Token (h.Token.Symbol on h.Token.ChainID) to w.Address
        fmt.Println(w.Address, h.Token.Symbol, h.Token.ChainID)
    }
}
```

## Payments

The `paymentID` argument to every payment method may be **either** the payment's
UUID **or** its `rail0_id` (the contract's bytes32 id) — the gateway resolves both.

Operation outcomes are happy-path: `void` is allowed only before any capture
(else the contract reverts `AlreadyCaptured`); `release` closes the payment as
`released` only on a total release (an untouched authorization), otherwise it
leaves the status unchanged; `refund` closes it as `refunded` only once fully
settled, so a partial refund leaves the status unchanged (the `partially_refunded`
value is retained only for historical rows).

```go
// List (requires JWT)
payments, _ := client.Payments.List(ctx, rail0.ListPaymentsParams{
    Status: "authorized",
    Sort:   "-created_at",
})

// Get (embeds transactions)
p, _ := client.Payments.Get(ctx, rail0ID)

// Create — optional idempotency key replays the existing payment on retry
resp, _ := client.Payments.Create(ctx, rail0.CreatePaymentRequest{
    ChainID: 8453, Mode: "authorize", Amount: "50.00",
    Token: "0x...", Payer: "0x...", Payee: "0x...",
}, "idempotency-key-123")

// Submit payer signature
client.Payments.Sign(ctx, rail0ID, rail0.PayerSignatureRequest{Signature: "0x..."})

// Authorize → Capture (each prepare returns a pending Transaction)
tx, _ := client.Payments.AuthorizePrepare(ctx, rail0ID)
// sign tx.UnsignedTransaction → signedBytes
client.Payments.Authorize(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

tx, _ = client.Payments.CapturePrepare(ctx, rail0ID, "50.00")
client.Payments.Capture(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

// Refund is two-phase. Phase 1 (omit signature) returns a Transaction whose
// SigningPayload the payee signs; phase 2 supplies the signature.
ph1, _ := client.Payments.RefundPrepare(ctx, rail0ID, rail0.PrepareRequest{Amount: "10.00"})
_ = ph1.SigningPayload
ph2, _ := client.Payments.RefundPrepare(ctx, rail0ID, rail0.PrepareRequest{Amount: "10.00", Signature: "0x..."})
// sign ph2.UnsignedTransaction → signedBytes
client.Payments.Refund(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

// Generic op helpers also exist:
client.Payments.Prepare(ctx, rail0ID, rail0.OpVoid)
client.Payments.Submit(ctx, rail0ID, rail0.OpVoid, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

// MetaMask (and any wallet that signs + broadcasts in one step) reports the
// broadcast tx hash instead of a raw signed transaction. Record it with
// SubmitByHash (POST /payments/{id}/{op}/submitted). Payee-only for the merchant
// operations; `release` accepts either the payer or payee.
client.Payments.SubmitByHash(ctx, rail0ID, rail0.OpCapture, rail0.SubmitByHashRequest{TransactionHash: "0x..."})

// Transactions for a payment
txs, _ := client.Payments.Transactions(ctx, rail0ID, rail0.ListTransactionsParams{Sort: "-submitted_at"})
```

### Disputes

Disputes are payer-driven and signal-only (they never move funds). They follow
the same prepare → submit lifecycle as the other operations: prepare returns a
pending `Transaction` with the unsigned tx, the payer signs it, and Submit
broadcasts the signed raw tx. The on-chain event then flips the payment's
`Disputed` flag.

```go
// Open a dispute (payer only). reason is an optional bytes32 code (0x…).
tx, _ := client.Payments.DisputePrepare(ctx, rail0ID, "0x01")
// sign tx.UnsignedTransaction → signedBytes
client.Payments.Dispute(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

// Close a dispute (payer only)
tx, _ = client.Payments.CloseDisputePrepare(ctx, rail0ID)
client.Payments.CloseDispute(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: signedBytes})

// MetaMask buyer: report the broadcast dispute / close-dispute tx by hash instead
// of a raw signed tx (payer-only; the payer authenticates account-less via SIWE).
client.Payments.DisputeSubmitByHash(ctx, rail0ID, rail0.SubmitByHashRequest{TransactionHash: "0x..."})
client.Payments.CloseDisputeSubmitByHash(ctx, rail0ID, rail0.SubmitByHashRequest{TransactionHash: "0x..."})

// Open/close history for a payment
disputes, _ := client.Payments.Disputes(ctx, rail0ID)

// Account-level: every dispute (open AND closed) across the caller's payments,
// each with its parent payment embedded. Unlike the `disputed` flag on
// Payments.List (current-state), this still surfaces closed disputes.
all, _ := client.Disputes.List(ctx, rail0.ListDisputesParams{Status: "closed"})
for _, d := range all.Data {
    _ = d.Payment.Rail0Id // embedded payment
}
```

## Analytics (JWT + account)

Merchant sales analytics, scoped to the authenticated **account**'s own wallets as
payee — a merchant only ever sees its own sales. Every endpoint needs a JWT bound to
a non-null account (401 without a token, 403 for an account-less buyer session).
Reads are aggregates (counts / SUMs), not payment records.

Amounts are token base-unit integer strings and are only summed **within** a single
`(token, chain)` — different tokens are never added together, so per-token volume
comes with its `Decimals` for display.

```go
// Headline KPIs: order/dispute counts, refund & dispute rates, by-status counts,
// and per-(token, chain) gross/captured/refunded volume. All filters optional.
sum, _ := client.Analytics.Summary(ctx, rail0.AnalyticsFilters{
    Mode: "charge", From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z",
})
fmt.Println(sum.Orders, sum.RefundRate, sum.ByStatus["captured"])
for _, v := range sum.Volume {
    fmt.Println(v.Symbol, v.Gross, v.Captured) // format v.Gross with *v.Decimals
}

// Order count per time bucket (oldest first). Per-bucket Volume is non-nil only
// when BOTH Token and ChainID are set (a single token is summable). Interval is
// "day" (default), "week", or "month".
buckets, _ := client.Analytics.Timeseries(ctx, rail0.TimeseriesParams{
    AnalyticsFilters: rail0.AnalyticsFilters{Token: "0x…", ChainID: 8453},
    Interval:         "week",
})

// Aggregate orders by a dimension (By is required): "token" | "chain" | "mode" |
// "status". token/chain rows carry Token/ChainID/Decimals/Volume; mode/status rows
// are counts only (those fields are nil). Row.Key is the dimension value.
rows, _ := client.Analytics.Breakdown(ctx, rail0.BreakdownParams{By: "token"})
for _, r := range rows {
    fmt.Println(r.Key, r.Orders) // r.Key is a string (or an int chain id fallback)
}
```

## Signing

The SDK signs locally — the gateway never holds a private key. Two kinds of
signature are needed across the lifecycle:

- **EIP-3009 authorization** (off-chain), stored via `Payments.Sign` / supplied to
  `RefundPrepare` phase-2.
- **EIP-1559 transaction** signing for each `prepare`→`submit` step.

```go
key, _ := rail0.HexToPrivateKey("0x…") // 0x-hex → 32 raw bytes

// EIP-1559: sign a prepare step's unsigned_transaction → raw hex for submit.
raw, _ := rail0.SignTransaction(tx.UnsignedTransaction, key)
client.Payments.Authorize(ctx, rail0ID, rail0.SubmitTransactionRequest{SignedTransaction: raw})

// EIP-3009 payer authorization (authorize/charge). SignCharge is the charge variant.
sig, _ := rail0.SignAuthorize(rail0.SignPaymentParams{
    PrivateKey: key, Payment: cfg, Amount: big.NewInt(50_000_000),
    Nonce: created.SigningPayload.Message.Nonce, ContractAddress: contractAddr, TokenDomain: domain,
})
// Pack {v,r,s} into the 65-byte hex the gateway expects (r || s || v):
packed := fmt.Sprintf("0x%s%s%02x", sig.R[2:], sig.S[2:], sig.V)
client.Payments.Sign(ctx, created.Rail0Id, rail0.PayerSignatureRequest{Signature: packed})

// EIP-3009 payee refund authorization: SignReceiveWithAuthorization (from a
// PaymentConfig) or SignReceiveWithAuthorizationRaw (from explicit payload fields).
```

Signing helpers: `HexToPrivateKey`, `SignTransaction`, `SignTransferWithAuthorization`,
`SignAuthorize`, `SignCharge`, `SignReceiveWithAuthorization`, `SignReceiveWithAuthorizationRaw`.

### Stablecoin registry

`ChainInfo(chain)`, `EIP3009Tokens(chain)`, and `EIP2612Tokens(chain)` expose a
static registry of known stablecoins per chain (RAIL0 requires EIP-3009 support).

## Amounts

Convert between a human decimal and the token's base-unit integer string with
string math (no float rounding):

```go
b, _ := rail0.ToBaseUnits("1.50", 6) // "1500000" (USDC has 6 decimals)
rail0.FormatAmount("1500000", 6)      // "1.5"     (trailing zeros trimmed)
```

Fractional digits beyond `decimals` are truncated; a malformed amount errors.

## Webhooks (JWT)

```go
// Create — response includes the one-time SharedSecret
wh, _ := client.Webhooks.Create(ctx, rail0.CreateWebhookRequest{
    Name: "orders", CallbackURL: "https://example.com/hook", Topic: rail0.TopicPaymentsCaptured,
})
secret := wh.SharedSecret

// List with filters
active := true
hooks, _ := client.Webhooks.List(ctx, rail0.ListWebhooksParams{Topic: rail0.TopicPaymentsFailed, Active: &active})

// Lifecycle
client.Webhooks.Update(ctx, wh.ID, rail0.UpdateWebhookRequest{Name: "renamed"})
client.Webhooks.Disable(ctx, wh.ID)
client.Webhooks.Enable(ctx, wh.ID)
client.Webhooks.ResetCircuit(ctx, wh.ID)
rotated, _ := client.Webhooks.RotateSecret(ctx, wh.ID) // rotated.SharedSecret

// Delivery attempts
cbs, _ := client.Webhooks.EventCallbacks(ctx, wh.ID, rail0.ListEventCallbacksParams{Status: "failed"})

client.Webhooks.Delete(ctx, wh.ID)
```

Topics: `payments.created`, `payments.signed`, `payments.authorized`,
`payments.charged`, `payments.captured`, `payments.voided`,
`payments.released`, `payments.refunded`, `payments.failed`,
`payments.disputed`, `payments.dispute_closed`
(also available as `rail0.TopicPayments*` constants).

## Sorting

List endpoints accept a `Sort` field using JSON:API convention:

- `"created_at"` — ascending
- `"-created_at"` — descending
- `"-created_at,amount"` — multiple fields, left-to-right priority

## Configuration

```go
client := rail0.NewClient(rail0.ClientOptions{
    BaseURL:    "https://api.rail0.xyz",
    Token:      token, // Authorization: Bearer <token> (or use Headers / SetToken)
    Timeout:    30 * time.Second,
    MaxRetries: 3,
    RetryDelay: 200 * time.Millisecond,
    RetryOn429: true, // retry 429s, sleeping for Retry-After (uses the MaxRetries budget)
    Logger:     rail0.DebugLogger,
})
```

Network errors and timeouts are retried up to `MaxRetries` with exponential backoff.
HTTP errors are **not** retried by default; opt in to rate-limit retries with
`RetryOn429`, which draws on the same `MaxRetries` budget and waits for the server's
`Retry-After` (falling back to the backoff when the header is absent).

## Error handling

```go
_, err := client.Payments.Authorize(ctx, rail0ID, params)
if err != nil {
    var apiErr *rail0.APIError
    if errors.As(err, &apiErr) {
        fmt.Println(apiErr.Code)   // branch on this: "insufficient_token_balance"
        fmt.Println(apiErr.Title)  // short label: "Not enough balance"
        fmt.Println(apiErr.Detail) // a sentence you can show a user verbatim
        fmt.Println(apiErr.Hint()) // this SDK's own extra advice, "" when it has none
        if apiErr.Status == 429 {
            time.Sleep(apiErr.RetryAfter) // parsed from the Retry-After header (0 if absent)
        }
    }
}
```

**`Code` is the only field to branch on.** `Title` and `Detail` come from the
gateway's error catalogue, so the same condition always reads the same way whichever
endpoint surfaced it; `Detail` is written to be shown to a user as-is. `Message` is
`Detail` under its older name, kept so existing callers keep compiling.

`apiErr.Hint()` (or `rail0.DescribeError(code)`) is this SDK's own local advice, a
*supplement* to `Detail` rather than a replacement — present only for codes worth
adding a next step to, `""` otherwise.

The codes span four families, and the last two are the ones most requests actually
hit — neither is raised by RAIL0 itself:

| Family | Examples |
| --- | --- |
| Request & state guards | `not_capturable`, `amount_exceeds_refundable`, `not_the_payee` |
| RAIL0 custom errors | `not_payee`, `already_captured`, `refund_expired` |
| Token reverts | `insufficient_token_balance`, `invalid_token_signature`, `authorization_already_used` |
| Broadcast rejections | `insufficient_gas_funds`, `nonce_too_low`, `replacement_underpriced` |

A **failed transaction** carries the same triple as `ErrorCode`, `ErrorTitle` and
`ErrorDetail`, whether it reverted on-chain or was refused before broadcast — so both
render through one code path.

## Development

```bash
go test ./...
go build ./...
```

> Note: `types_gen.go` is generated from the gateway's OpenAPI document
> (`rail0-gateway/docs/openapi.json`) by `gen/generate.go`. Do not hand-edit it —
> after a gateway API change, regenerate it:
>
> ```bash
> go run gen/generate.go
> ```
>
> The client method files (`auth.go`, `payments.go`, `wallets.go`,
> `payment_methods.go`, `webhooks.go`, `analytics.go`, `health.go`, `chains.go`, `tokens.go`),
> the signing helpers (`signing.go`, `transaction.go`), and the stablecoin
> registry (`stablecoins.go`) are hand-maintained and never overwritten by the
> generator.

## License

[MIT](LICENSE)
