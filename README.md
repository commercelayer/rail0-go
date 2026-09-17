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

`Auth.Logout` ends the session for real: the gateway records the token's `jti` in
its denylist, so the JWT stops being accepted **before** its `exp`, and the client
drops it locally. Discarding a stored token without this leaves a bearer
credential valid for the rest of its lifetime — whoever holds a copy keeps the
session — so revoke before discarding, and before pointing a client at a
different gateway. Check `Revoked`: `false` means the gateway accepted the request
but could not record the revocation, and the token is still usable until `exp`.

`Auth.RevokeAll` is the other question, and `Logout` cannot answer it: `Logout` is
per **token**, so an address with five live sessions needs five tokens you do not
have. `RevokeAll` is per **address** and reaches the ones you never saw — including
any an attacker is holding — which makes it the call for a key you no longer trust.
The gateway records a cutoff **instant** rather than enumerating tokens, so a session
minted a moment before the call is refused by its own `iat`. `Cutoff` carries that
instant and is the field worth logging: it says exactly which sessions died, which a
boolean cannot. The local token is cleared either way.

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
| `client.Accounts` | The merchant account's own profile (JWT) |
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
// nonce and signs with the given key, but does NOT touch the client's token. The
// message carries the WALLET-LINK statement, not the login one — see below:
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
if h.Sidekiq != nil { // authenticated calls only — see below
    fmt.Println(h.Sidekiq.Status, h.Sidekiq.Processes, h.Sidekiq.Enqueued)
}
```

The endpoint is public, but only `Status`, `DB` and `Timestamp` come back to an
anonymous caller: `APIVersion`, `ContractVersion`, `ActiveChains`,
`ActiveContracts` and `Sidekiq` are session-gated, so set a token on the client
first or they arrive zero-valued (`Sidekiq` nil). `Status` is `"ok"`,
`"degraded"` (database fine, workers not — `Sidekiq` says why) or `"error"`
(database down, the only case that also returns HTTP 503).

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
// handshake as login; it fetches a nonce and signs, but does NOT log the client
// in). The added address need not equal the session address — a merchant may
// register several payee wallets — but you must hold each added address's key.
proof, _ := client.Auth.ProveAddress(ctx, walletKeyBytes, "api.rail0.xyz")
w, _ := client.Wallets.Create(ctx, accountID, rail0.CreateWalletRequest{
    Address:   "0xABC...",
    Message:   proof.Message,
    Signature: proof.Signature,
    Label:     "Treasury",
})
```

**The proof is purpose-bound, and a login proof will not do.** The gateway pins each
endpoint to one SIWE statement and refuses the other with 422 `siwe_purpose_mismatch`:

| Endpoint | Statement |
|---|---|
| `POST /auth` | `Sign in to RAIL0` |
| `POST /accounts/:id/wallets` | `Add this wallet to your RAIL0 account` |

That is a security boundary rather than a label: a login signature is handed out on
every sign-in, so a wallet-link endpoint that accepted one would let anyone holding a
captured login proof bind that address to their **own** account. `Login` and
`ProveAddress` each pick the right statement; there is nothing to choose at the call site.

```go

// Update a wallet (label / active)
client.Wallets.Update(ctx, accountID, w.ID, rail0.UpdateWalletRequest{Label: "Main"})

// Delete a wallet
client.Wallets.Delete(ctx, accountID, w.ID)

// What the wallet ACCEPTS — this is what drives the public /payment_methods and
// gates payment creation: a wallet with no active holding is invisible to buyers
// and unusable as a payee. AddToken is an upsert on (wallet, token), so a
// previously-disabled holding is reactivated rather than duplicated.
isDefault := true
holding, _ := client.Wallets.AddToken(ctx, accountID, w.ID, rail0.AddWalletTokenRequest{
    ChainID: 84532,
    Token:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    Default: &isDefault, // at most one default per wallet; setting it clears the others
})

// The handle for the three calls below is the TOKEN's UUID (holding.TokenID),
// not an id of the holding row — the gateway looks the holding up by (wallet, token).
client.Wallets.DisableToken(ctx, accountID, w.ID, holding.TokenID) // returns the holding
client.Wallets.EnableToken(ctx, accountID, w.ID, holding.TokenID)  // 404 if none exists
client.Wallets.RemoveToken(ctx, accountID, w.ID, holding.TokenID)  // soft delete, 204

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

## Pagination

Every `PaginatedResponse[T]` carries `Data` and `Meta`. `Meta` is `{Page, PerPage, Total, TotalPages, Links}`.

`TotalPages` is **zero** for an empty collection — "no pages" is what there are, so a pager rendered off it renders none. `Links` comes from the `Link` header: `First` and `Last` are always present, `Prev` and `Next` only where they exist, and all four are empty when the collection is. The URIs are **relative** (path + query) and resolve against the URL you requested — the gateway emits them that way so they cannot advertise the wrong scheme through a TLS-terminating proxy.

```go
page, _ := client.Payments.List(ctx, rail0.ListPaymentsParams{PerPage: 100})
for page.Meta.Links.Next != "" {
    page, _ = client.Payments.List(ctx, rail0.ListPaymentsParams{Page: page.Meta.Page + 1, PerPage: 100})
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

// Create — optional idempotency key replays the existing payment on retry.
// The key is bound to the request: reusing it with different terms returns
// a 422 with code "idempotency_key_reused", never the first payment.
resp, _ := client.Payments.Create(ctx, rail0.CreatePaymentRequest{
    ChainID: 8453, Mode: "authorize", Amount: "50.00",
    Token: "0x...", Payer: "0x...", Payee: "0x...",
}, "idempotency-key-123")

// Submit payer signature
client.Payments.Sign(ctx, rail0ID, rail0.PayerSignatureRequest{Signature: "0x..."})

// Authorize → Capture (each prepare returns a pending Transaction)
// One of a payment's transactions by id - the lookup for an action id, so a caller
// holding one resolves it directly instead of listing and scanning.
tx0, _ := client.Payments.GetTransaction(ctx, rail0ID, transactionID)

// Idempotent prepare. Without a key, a retry arriving after the first transaction was
// signed and broadcast opens a SECOND one - right for a sequential partial capture,
// wrong for a retry, and only the caller can tell those apart.
tx1, _ := client.Payments.PrepareWithKey(ctx, rail0ID, rail0.OpCapture, orderID,
    rail0.PrepareRequest{Amount: "50.00"})

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

// Redrive a broadcast that was prepared and signed but never landed. Offer this on
// Transaction.Redrivable — the same predicate the gateway guards the route with — and
// not on `status == "pending"`: a pending row holding no signed transaction is not
// redrivable, and there the next step is submitting the signature.
tx, _ := client.Payments.Redrive(ctx, rail0ID, "019f8a3d-…")
```

### Disputes

Disputes are payer-driven and signal-only (they never move funds). They follow
the same prepare → submit lifecycle as the other operations: prepare returns a
pending `Transaction` with the unsigned tx, the payer signs it, and Submit
broadcasts the signed raw tx. The on-chain event then flips the payment's
`Disputed` flag.

```go
// Open a dispute (payer only). reason is optional but must be a FULL bytes32
// when given — "0x" + exactly 64 hex digits (right-pad a short code); omit it
// and the gateway defaults to the zero bytes32.
tx, _ := client.Payments.DisputePrepare(ctx, rail0ID,
    "0x0100000000000000000000000000000000000000000000000000000000000000")
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

## Account (JWT)

```go
// The caller's OWN profile. The gateway's /accounts/{id} is behind an ownership
// guard (a JWT whose account matches the path), so there is no way to read
// another merchant's account here — and an id that is not an account answers 404
// exactly as another account's id does, so the pair says nothing about existence.
me, _ := client.Accounts.Get(ctx, accountID)
fmt.Println(me.Name, me.Email)
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
// Headline KPIs: order/dispute counts, refund/dispute/failed rates, by-status counts,
// per-(token, chain) volume and per-chain gas. All filters optional.
sum, _ := client.Analytics.Summary(ctx, rail0.AnalyticsFilters{
    Mode: "charge", From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z",
})
fmt.Println(sum.Orders, sum.RefundRate, sum.ByStatus["captured"])
for _, v := range sum.Volume {
    // Settled/Escrowed say where the money is now (mirrored on-chain residuals);
    // Captured/Refunded say what happened (confirmed transactions).
    fmt.Println(v.Symbol, v.Gross, v.Settled, v.Captured) // format with *v.Decimals
}

// Gas the merchant's own transactions cost, per CHAIN and in that chain's NATIVE
// token — never summed across chains, since Base ETH and Polygon POL are different
// currencies. Wasted is gas an on-chain revert burned.
for _, g := range sum.Gas {
    fmt.Println(*g.ChainName, g.Spent, g.Wasted, *g.Symbol) // Decimals is always 18
    // Mean broadcast→confirmation for that chain; nil when none confirmed (not 0, which
    // would read as instant).
    if g.ConfirmationSecs != nil {
        fmt.Println("confirms in", *g.ConfirmationSecs, "s")
    }
}

// Why the failures failed, commonest first. FailedRate says how much fails; this says
// what to act on — a revert is a state problem, a rejection that never reached the chain
// is a wallet problem.
for _, f := range sum.Failures {
    fmt.Println(f.Code, f.Transactions)
}

// The same gas regrouped: by payment status, and by the operation that paid it.
// Each cut adds back up to its chain's row. Orders is the denominator for the
// average cost of an order, and is nil on the by-operation cut — one order spans
// several operations. FailedRate is per resolved TRANSACTION, not per order.
for _, s := range sum.GasByStatus {
    fmt.Println(s.Key, s.Spent, s.Orders) // e.g. "captured" 62000 2
}

// Order count per time bucket (oldest first). Per-bucket Volume is non-nil only
// when BOTH Token and ChainID are set (a single token is summable). Interval is
// "day" (default), "week", or "month".
buckets, _ := client.Analytics.Timeseries(ctx, rail0.TimeseriesParams{
    AnalyticsFilters: rail0.AnalyticsFilters{Token: "0x…", ChainID: 8453},
    Interval:         "week",
})

// Aggregate orders by a dimension (By is required): "token" | "chain" | "mode" |
// "status" | "operation". token/chain rows carry Token/ChainID/Decimals/Volume;
// mode/status rows are counts only (those fields are nil). Row.Key is the dimension
// value.
rows, _ := client.Analytics.Breakdown(ctx, rail0.BreakdownParams{By: "token"})
for _, r := range rows {
    fmt.Println(r.Key, r.Orders) // r.Key is a string (or an int chain id fallback)
}

// "operation" is the one dimension that groups the merchant's own CONFIRMED
// transactions rather than payments, so it reports both counts: Transactions is how
// often the operation ran — a partial capture runs several times on one order — and
// Orders is how many orders it touched.
ops, _ := client.Analytics.Breakdown(ctx, rail0.BreakdownParams{By: "operation"})
for _, r := range ops {
    fmt.Println(r.Key, *r.Transactions, r.Orders) // e.g. capture 2 1
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

// EIP-3009 authorization: sign the gateway's signing_payload VERBATIM. One call
// for every operation — authorize, charge, refund — because which typehash and
// which fields to use is stated by the payload itself.
sig, _ := rail0.SignSigningPayload(key, created.SigningPayload)
packed := rail0.PackSignature(sig) // 0x + r || s || v, what the gateway expects
client.Payments.Sign(ctx, created.Rail0Id, rail0.PayerSignatureRequest{Signature: packed})

// A refund is the same call with the PAYEE's key — the refund payload's `from` is
// the payee, since the funds being moved are already the merchant's.
sig, _ = rail0.SignSigningPayload(payeeKey, phase1.SigningPayload)
```

Signing helpers: `HexToPrivateKey`, `SignTransaction`, **`SignSigningPayload`**,
`PackSignature`, `SignTransferWithAuthorization`, `SignReceiveWithAuthorizationRaw`.

**The gateway builds the payload; you sign it verbatim.** Only the gateway knows
which contract version a payment lives on, and the version selects the typehash,
the EIP-712 domain *and* the field layout. `SignSigningPayload` takes all three
from the payload, so a contract change — rail0#58 moves authorize/charge to
`ReceiveWithAuthorization` — needs no change here. An unrecognised `primaryType`
returns an error naming the version skew rather than falling back, because a
guessed typehash yields a *valid* signature over the wrong digest: it reports
success and fails only on-chain, after gas, looking like a bad key.

`SignAuthorize`, `SignCharge` and `SignReceiveWithAuthorization` are **deprecated**
for that reason — they rebuild the digest from a `PaymentConfig`.
`SignTransferWithAuthorization` and `SignReceiveWithAuthorizationRaw` remain as
low-level escape hatches for talking to an EIP-3009 token outside a RAIL0 payment.

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

More fractional digits than the token has is an **error**, not a truncation: the gateway 422s an amount a token cannot express, so dropping the extra digits would send a different amount than you wrote. A malformed amount errors too.

Which `decimals` to pass resolves from a payment's `Token` **together with** its
`ChainID` — a token address identifies a token only within one chain. Both are on
every `Payment`, list rows included, so formatting an amount from
`Payments.List` never needs a per-row `Payments.Get`.

## Webhooks (JWT)

One subscription covers a **set** of topics: one shared secret, one circuit breaker,
however many events. The delivery names the event that fired in `X-Rail0-Topic`.

```go
// Create — response includes the one-time SharedSecret
wh, _ := client.Webhooks.Create(ctx, rail0.CreateWebhookRequest{
    Name:        "orders",
    CallbackURL: "https://example.com/hook",
    Topics: []string{
        rail0.TopicPaymentsAuthorized,
        rail0.TopicPaymentsCaptured,
        rail0.TopicPaymentsVoided,
        rail0.TopicPaymentsRefunded,
    },
})
secret := wh.SharedSecret

// List with filters — `Topic` asks which subscriptions INCLUDE that event
active := true
hooks, _ := client.Webhooks.List(ctx, rail0.ListWebhooksParams{Topic: rail0.TopicPaymentsFailed, Active: &active})

// Lifecycle. Topics REPLACES the set (so it is also how a topic is removed) and never
// touches the secret. Two subscriptions on one CallbackURL must not overlap — the
// gateway answers 409 and names the topic that collided.
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

### Verifying a delivery

Every delivery carries `X-Rail0-Topic`, `X-Rail0-Timestamp` and
`X-Rail0-Signature` — a hex HMAC-SHA256 over `"{timestamp}.{body}"` keyed with the
webhook's shared secret. `VerifyWebhookSignature` does the compare in constant
time and enforces the ±5 min freshness window (`rail0.WebhookToleranceSecs`), so
there is no need to hand-roll it:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body) // the RAW body: verify before any JSON re-parse
    if !rail0.VerifyWebhookSignature(secret, string(body),
        r.Header.Get(rail0.HeaderWebhookTimestamp), r.Header.Get(rail0.HeaderWebhookSignature)) {
        http.Error(w, "invalid signature", http.StatusUnauthorized)
        return
    }
    // …handle r.Header.Get(rail0.HeaderWebhookTopic)
}
```

The timestamp is inside the signed preimage, which is what bounds a replay: it
cannot be moved forward to refresh a captured delivery. This requires a gateway
sending the timestamped signature (rail0-gateway#176, from 2026-07-31) — against
an older gateway, which signed the body alone and sent no `X-Rail0-Timestamp`,
every delivery fails verification.

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
    RetryOn429:    true,            // retry 429s, waiting the server's Retry-After
    RetryAfterCap: 60 * time.Second, // longest Retry-After to honour (default 60s)
    Logger:        rail0.DebugLogger,
})
```

Network errors and timeouts are retried up to `MaxRetries` with exponential backoff.
HTTP errors are **not** retried by default.

### Rate limits

The gateway throttles the public surface **per IP** (100 requests / 60s by default) and
everything authenticated **per session**, keyed on the JWT subject (300 / 60s). Over budget
it answers `429` with `code: "rate_limited"`, a `Retry-After`, and — since
rail0-gateway#201 — `RateLimit-Limit`/`-Remaining`/`-Reset` on *every* response, so a
client with work to pace can slow down instead of discovering the wall.

`APIError.RetryAfter` carries the header. Read it when you handle the error yourself, or
set `RetryOn429` and let the client wait:

- **`RetryOn429` works on its own.** It used to need `MaxRetries > 0` as well, which made
  it a silent no-op for anyone who set only this flag (`maxAttempts` is `MaxRetries+1`, and
  the zero value gives one attempt). On its own it now allows one retry; with `MaxRetries`
  it uses that budget.
- **`Retry-After` is clamped** by `RetryAfterCap` (60s default): a gateway older than
  rail0-gateway#201 sends its whole throttle period rather than the time left in the
  window, so a limit hit one second in would otherwise park the request for the full 60.
- **The wait is jittered**, and never shortened below what it is for: additive on a
  server-instructed wait (scaling a `Retry-After` down means retrying before the window it
  named has passed), equal jitter — half fixed, half random — on a guessed one. Jitter
  matters because callers align: rail0-admin proxies every merchant over ONE session, so
  they share the bucket, are told the same number, and would otherwise wake together.
- **A 429 is the only status retried**, on any method including `POST`, because the gateway
  rejects it in middleware *before* the request reaches the application: nothing ran, so
  nothing can run twice. That is not true of a 502 or a timeout on a capture, where the
  broadcast may already be in flight.

The wait also respects the context: a cancelled `ctx` ends the request rather than sleeping
it out.

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
