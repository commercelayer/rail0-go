package rail0

import (
	"encoding/json"
	"fmt"
	"time"
)

// APIError is returned when the RAIL0 API responds with a non-2xx status code.
//
// The gateway's error body carries a code/title/detail triple; this mirrors it.
// Prefer Detail when showing a user what happened and Title for a heading: both come
// from the gateway's error catalogue, so the same condition always reads the same way
// — unlike Hint(), which is this SDK's own local advice for the codes it knows.
type APIError struct {
	// Status is the HTTP status code (e.g. 404, 409, 422).
	Status int
	// Code is the machine-readable error identifier and the only field to branch on,
	// e.g. "amount_exceeds_refundable" or "insufficient_token_balance".
	Code string
	// Title is a short label for the failure, e.g. "Not enough balance".
	Title string
	// Detail is one or two sentences fit to show a user verbatim.
	Detail string
	// Message is Detail under its pre-code/title/detail name; kept so existing callers
	// keep compiling, and now ALWAYS equal to Detail — the gateway no longer sends a
	// separate `message`, so the two cannot diverge. Read Detail in new code.
	//
	// Deprecated: use Detail.
	Message string
	// Reason is an optional sub-cause the gateway attaches to some errors
	// (e.g. "rpc_unreachable" on a 502 signing_payload_unavailable); "" if absent.
	Reason string
	// RetryAfter is how long to wait before retrying, parsed from the response's
	// Retry-After header. Set on a 429 (rate limited) — 0 when the header is absent
	// or unparseable. The client retries automatically only when RetryOn429 is set.
	RetryAfter time.Duration
	// Body is the raw response body. Most error bodies are fully described by the fields
	// above; it is kept for the endpoints whose ERROR response carries data — GET /health
	// answers 503 with the full health report when its database is down.
	Body json.RawMessage
}

func (e *APIError) Error() string {
	text := e.Detail
	if text == "" {
		text = e.Message
	}
	if e.Code == "" {
		return fmt.Sprintf("rail0 error (HTTP %d): %s", e.Status, text)
	}
	return fmt.Sprintf("rail0 %s (HTTP %d): %s", e.Code, e.Status, text)
}

// Hint returns this SDK's own actionable next step for the error, or "" when the code
// isn't one it knows. It is a SUPPLEMENT to Detail, not a replacement: Detail is the
// gateway's own sentence and is always present, while a hint exists only for codes
// worth adding client-side advice to. It checks Code first, then the Reason sub-cause
// (which carries the specific error for a contract_revert).
func (e *APIError) Hint() string {
	if h := errorHints[e.Code]; h != "" {
		return h
	}
	return errorHints[e.Reason]
}

// DescribeError returns an actionable hint for a rail0 error code — a gateway
// state-guard code or a contract revert (the snake_case Solidity error) — or ""
// when the code is unknown.
func DescribeError(code string) string { return errorHints[code] }

// errorHints maps an error code to an actionable next step, so a rejected request
// can explain what to do rather than surfacing a bare code. Shared source with the
// TS SDK (describeError), the admin's locked-action reasons and the CLI's hints.
var errorHints = map[string]string{
	// payment-state guards (HTTP 422)
	"amount_exceeds_capturable": "amount is above the capturable balance — check capturableAmount on the payment",
	"amount_exceeds_refundable": "amount is above the refundable balance — check refundableAmount on the payment",
	"not_capturable":            "the payment must be 'authorized' or 'partially_captured' to capture",
	"not_voidable":              "void is only allowed while 'authorized' with nothing captured — use release for the remainder after a capture",
	// Not the expiry window (that is authorization_not_expired): nothing uncaptured is left.
	"not_releasable":     "there is no uncaptured (capturable) balance left to return to the buyer — check capturableAmount on the payment",
	"not_refundable":     "nothing is refundable — the payment must be charged/captured and within the refund window",
	"not_signable":       "the payment must be 'unsigned' to sign",
	"already_signed":     "the payer signature is already stored — the payee can act now",
	"no_signature":       "the payer has not signed yet",
	"wrong_mode":         "this operation doesn't match the payment's mode (authorize vs charge)",
	"already_disputed":   "a dispute is already open — close it first",
	"not_disputed":       "there is no open dispute to close",
	"nothing_to_dispute": "a dispute needs a merchant-held (refundable) balance",
	// The open and close lists differ (and full_refund is system-only), so the hint
	// points at both rather than guessing which one the caller meant.
	"unknown_dispute_reason":       "the reason must be a dispute-reason code (or its exact bytes32) from the right list — DisputeOpenReasons to open, DisputeCloseReasons to close",
	"transaction_not_overwritable": "a transaction for this operation is already in flight — wait for it to settle",
	"signer_mismatch":              "the signature recovers to a different wallet than the one that must sign — the payment's payer/payee, or at POST /claim the wallet the onboarding request names",
	// The SIWE BINDING failures, split out of signer_mismatch so a failed login says
	// WHICH part of the proof did not bind (rail0-gateway#216). None of these names the
	// server's expectation: that endpoint is unauthenticated, so echoing the allow-list
	// would turn every hint into a probe. siwe_chain_mismatch may point at GET /blockchains:
	// the accepted chain ids (1 plus every active catalog chain) are public there anyway.
	"siwe_domain_not_allowed": "sign with the origin the front-end is served from, and have it added to the gateway's SIWE domain allow-list",
	"siwe_uri_mismatch":       "the message's uri host must equal its own domain",
	"siwe_chain_mismatch":     "sign with chain id 1 (what the SDK's signing helpers send) or the id of a chain the gateway has active (GET /blockchains) — usually a hand-built message or a wallet on an unsupported or deactivated network",
	"siwe_proof_expired":      "get a fresh nonce and sign again",
	// Each signed flow (login, wallet registration, account claim, revoke-all) has its
	// own exact statement; reusing one flow's message for another is refused.
	"siwe_purpose_mismatch": "the signed message's statement is for a different flow — build the message with this endpoint's own statement (login, wallet registration, claim and revoke-all each have one) and sign again",
	"nonce_used":            "that sign-in nonce was already consumed — request a fresh nonce and sign again (each is single-use)",
	// The catalogue text names a bad recovery byte too: a v of 0/1 where 27/28 is
	// expected (or the reverse), not only a truncated or malformed signature.
	"invalid_signature": "the signature could not be recovered — it must be 65 bytes (0x + r || s || v, 130 hex chars) with a valid recovery byte v; PackSignature builds that shape",
	// Address-wide, not one token: "sign in again", not "that token is dead".
	"sessions_revoked":             "every session issued before this address's revoke-all cutoff is refused — sign in again",
	"config_hash_mismatch":         "the payment record and its on-chain deployment disagree — the payment cannot be operated as recorded",
	"payment_not_on_chain":         "the contract has no record of this payment — its opening transaction may never have confirmed",
	"unsupported_contract_version": "the payment's RAIL0 deployment is newer or older than this gateway supports — upgrade the gateway",
	// token-level reverts: raised by the ERC-20 / EIP-3009 token, not by RAIL0
	"insufficient_token_balance":  "the paying wallet does not hold enough of the token — top it up and retry",
	"invalid_token_signature":     "the EIP-3009 authorization did not recover to the paying wallet — wrong key, chain, token or amount",
	"authorization_already_used":  "that EIP-3009 authorization was already spent or cancelled — each is single-use, create a fresh payment",
	"authorization_not_yet_valid": "the authorization's validAfter is still in the future",
	"token_account_blocked":       "the token issuer has blocklisted one of the wallets in this transfer",
	"token_paused":                "the token contract is paused by its issuer — no transfer can settle right now",
	// broadcast rejections: the node refused the transaction, it never reached the chain
	"insufficient_gas_funds":  "the sending wallet cannot cover gas — fund it with the chain's native token",
	"nonce_too_low":           "a transaction with that nonce is already on-chain — re-prepare the operation",
	"replacement_underpriced": "another transaction with that nonce is pending and this one does not pay enough to replace it",
	"gas_price_too_low":       "the fee is below what the node accepts — re-prepare to pick up current fees",
	"already_known":           "the node already has this exact transaction — wait for it to confirm rather than resending",
	"rpc_unavailable":         "no configured RPC endpoint answered — the transaction was not submitted",
	// authorization: which party the session is missing
	"not_the_payee":     "only the payment's payee can do this — sign in with the merchant's wallet",
	"not_the_payer":     "only the payment's payer can do this — sign in with the buyer's wallet",
	"not_a_participant": "only the payer and the payee can see or act on a payment",
	"not_your_account":  "the session belongs to a different account than the one in the path — use your own account id",
	// A session is bound to the one wallet it signed in with: the account's other wallets
	// are readable, but their label/active flag and accepted tokens are not this session's
	// to change (operators are exempt).
	"not_your_wallet": "a session can change only the wallet it signed in with — sign in with that wallet to update it, deactivate it or manage its tokens",
	// POST /accounts/:id/wallets is suspended for every session until the merchant
	// wallet-management section lands; the request shape and its proof stay as they are.
	"wallet_add_unavailable": "adding a wallet to an account is not available yet — it returns with the merchant wallet-management section",
	// Deactivation blocks money-in and account writes only; reads and the operations that
	// return funds keep working, so a wind-down is never trapped.
	"account_deactivated": "the merchant account is deactivated — void, release and refund still work, but new payments, authorize, charge, capture and account changes are blocked until an operator reactivates it",
	"wallet_deactivated":  "this wallet is deactivated — it can still read, but cannot act on payments or change the account; if you switched it off, its own session can switch it back on (Wallets.Update with Active=true), or sign in with another of the account's wallets",
	// gateway#439: an operator's switch-off revokes the key, so the key's own session
	// cannot lift it (the owner's own switch-off it can).
	"wallet_deactivated_by_operator": "an operator deactivated this wallet, so only an operator can reactivate it — ask an operator to reactivate it",
	"release_submitter_not_a_party":  "from must be the payment's payer or payee — omit it to build the release for the signed-in caller",
	"payer_must_be_caller":           "the payer must be the signed-in wallet — the buyer creates and authorizes its own payment",
	// Login resolves the account FROM a wallet, so an account left with no active one
	// could neither act nor add a wallet back. Adding a wallet is suspended and a session
	// changes only its own wallet, so the other wallet comes back on from its own session
	// (if its owner switched it off) or from an operator (gateway#439).
	"last_active_wallet": "this is the account's only active wallet — another wallet must be active before this one can be switched off: sign in with one you switched off and reactivate it, or ask an operator (adding a wallet is not available yet)",
	"last_admin":         "the change would leave no active operator — grant the operator role to another active account first",
	// contract reverts (surfaced as contract_revert, or on a failed transaction)
	"not_payee":                   "only the merchant (payee) may do this",
	"not_payer":                   "only the buyer (payer) may do this",
	"not_payer_or_payee":          "only the payer or the payee may do this",
	"refund_expired":              "the refund window has closed (refundExpiry passed) — refund/dispute is no longer possible",
	"authorization_not_expired":   "release opens only after authorizationExpiry — wait until it passes",
	"already_captured":            "already (partially) captured — use release for the remainder, not void",
	"unsupported_payment_method":  "the payee (merchant) doesn't accept this token/chain — check the merchant's payment methods",
	"idempotency_key_reused":      "that Idempotency-Key was already used for a payment with different terms — reuse it only to retry the same request, or pick a new key",
	"unknown_token":               "the token isn't configured on this chain",
	"no_active_contract":          "no active RAIL0 contract on that chain",
	"missing_param":               "a required parameter is missing from the request",
	"validation_failed":           "a parameter is invalid — the error detail names the offending field",
	"rate_limited":                "rate limit reached — wait for Retry-After (the time left in the window) and retry, or set RetryOn429",
	"event_not_found":             "the reported transaction mined but did not perform this operation on this payment — nothing moved; check the hash and prepare the operation again",
	"submission_timeout":          "the transaction was not mined in time and was recorded as failed — if it mines later, re-report its hash via the operation's submitted endpoint",
	"token_not_accepted":          "this RAIL0 deployment does not accept that token — its allowlist is fixed at deploy time",
	"not_redrivable":              "only a pending transaction that already holds its signed transaction can be redriven — check Redrivable on the transaction",
	"queue_unavailable":           "the gateway's job queue could not be reached, so nothing was enqueued — retry in a moment",
	"transaction_mismatch":        "the submitted transaction's target, calldata, value or chain differs from the one this operation prepared — sign the prepare's unsigned_transaction unmodified",
	"signing_payload_unavailable": "the authorization payload could not be built because a chain read failed — retry in a moment; Reason tells an unreachable RPC apart from a token or configuration problem",
	"invalid_sort":                "that sort field is not one this endpoint can order by — the error detail names the allowed set",
	"webhook_limit_reached":       "the account already holds the most webhook subscriptions it may — delete one, or add topics to an existing subscription",
	// Onboarding: POST /signup and POST /claim (the gateway's onboarding flow).
	// The /admin/onboardings codes (onboarding_not_found, onboarding_completed) are left
	// out: those routes are the console's, not the SDK's. The link codes never say which
	// term failed — a stale link has one remedy: the newest email, or a new link.
	"signup_unavailable":            "self-service sign-up is switched off on this gateway for now — try again later, or ask your RAIL0 contact for an invite",
	"onboarding_unavailable":        "this gateway has no onboarding secret configured, so it can neither issue nor verify sign-up links — ask the operator",
	"email_provider_not_configured": "the gateway has no email provider that delivers, so no link was sent and nothing was saved — ask the operator to configure one",
	"email_domain_not_allowed":      "this environment emails only its allowed domains (the detail lists them) — use an address on one of them; nothing was saved",
	"account_email_taken":           "an account with this email already exists — sign in with its wallet, or use another email",
	"account_wallet_taken":          "this wallet is already registered to an account — sign in with it, or use another wallet",
	"account_name_taken":            "an account with this name already exists — choose another name",
	"terms_required":                "the claim needs terms_version: send the version of the terms the merchant accepted",
	"onboarding_link_invalid":       "the link was altered or its request no longer exists — use the link in the most recent email, or ask for a new one",
	"onboarding_link_expired":       "the link has expired — sign up again, or ask your RAIL0 contact for a new link",
	"onboarding_link_superseded":    "a newer link was sent for this request and this one no longer works — use the link in the most recent email",
	// A BARE forbidden is not a party mismatch: the gateway split those into codes of
	// their own (not_the_payee, not_the_payer, wallet_deactivated, account_deactivated,
	// not_your_account) because they need different fixes. This entry kept describing
	// one of them long after the split — and the rule it named no longer exists in the
	// gateway at all.
	"forbidden":              "not permitted for this session — typically the operator grant, a resource owned by another account, or a transaction signed by the wrong wallet",
	"payment_already_exists": "a payment with this id already exists on-chain",
}

// firstNonEmpty returns the first argument that isn't the empty string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
