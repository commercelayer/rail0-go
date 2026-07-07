# rail0-go

Go SDK for the [RAIL0](https://github.com/commercelayer/rail0) stablecoin payment API.

RAIL0 is an immutable EVM smart contract that brings the authorize → capture → refund lifecycle of card networks to stablecoin payments — no intermediaries, no protocol fees, no permission required. This SDK wraps the REST API that sits in front of the contract.

## Requirements

Go ≥ 1.22

## Installation

```bash
go get github.com/rail0/go-sdk
```

## Quick start

```go
client := rail0.NewClient(rail0.ClientOptions{
    BaseURL: "https://api.rail0.xyz",
    Headers: map[string]string{"Authorization": "Bearer <jwt>"},
})
ctx := context.Background()

// Discover the merchant's wallets (each with its tokens nested) and pick one
wallets, _ := client.Wallets.List(ctx, "account-uuid")
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
| `client.Webhooks` | Webhook subscription management (JWT) |

## Authentication (SIWE)

```go
// 1. Get a nonce (POST /auth/nonces)
nonce, _ := client.Auth.GetNonce(ctx)
_ = nonce.Value // embed in the SIWE message

// 2. Build + sign the EIP-4361 message, then verify (POST /auth)
resp, _ := client.Auth.Verify(ctx, rail0.AuthRequest{
    Message:   siweMessage,
    Signature: "0x...",
})
// resp.Token — include as Authorization: Bearer <token>

// Or do the whole flow with a raw private key:
resp, _ = client.Auth.Login(ctx, privKeyBytes, "api.rail0.xyz")
```

## Catalog (public)

```go
chains, _ := client.Chains.List(ctx)        // GET /blockchains
tokens, _ := client.Tokens.List(ctx, 8453)  // GET /tokens?chain_id=8453
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

// Create a wallet
w, _ := client.Wallets.Create(ctx, accountID, rail0.CreateWalletRequest{
    Address: "0xABC...",
    Label:   "Treasury",
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

// Open/close history for a payment
disputes, _ := client.Payments.Disputes(ctx, rail0ID)
```

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
    Headers:    map[string]string{"Authorization": "Bearer " + token},
    Timeout:    30 * time.Second,
    MaxRetries: 3,
    RetryDelay: 200 * time.Millisecond,
    Logger:     rail0.DebugLogger,
})
```

## Error handling

```go
_, err := client.Payments.Authorize(ctx, rail0ID, params)
if err != nil {
    var apiErr *rail0.APIError
    if errors.As(err, &apiErr) {
        fmt.Println(apiErr.Status, apiErr.Code, apiErr.Message)
    }
}
```

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
> The client method files (`auth.go`, `payments.go`, `accounts.go`,
> `webhooks.go`, `health.go`, `chains.go`, `tokens.go`) are hand-maintained and
> are never overwritten by the generator.

## License

[MIT](LICENSE)
