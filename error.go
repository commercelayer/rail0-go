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
	"amount_exceeds_capturable":    "amount is above the capturable balance — check capturableAmount on the payment",
	"amount_exceeds_refundable":    "amount is above the refundable balance — check refundableAmount on the payment",
	"not_capturable":               "the payment must be 'authorized' or 'partially_captured' to capture",
	"not_voidable":                 "void is only allowed while 'authorized' with nothing captured — use release for the remainder after a capture",
	"not_releasable":               "release opens only after authorizationExpiry",
	"not_refundable":               "nothing is refundable — the payment must be charged/captured and within the refund window",
	"not_signable":                 "the payment must be 'unsigned' to sign",
	"already_signed":               "the payer signature is already stored — the payee can act now",
	"no_signature":                 "the payer has not signed yet",
	"wrong_mode":                   "this operation doesn't match the payment's mode (authorize vs charge)",
	"already_disputed":             "a dispute is already open — close it first",
	"not_disputed":                 "there is no open dispute to close",
	"nothing_to_dispute":           "a dispute needs a merchant-held (refundable) balance",
	"transaction_not_overwritable": "a transaction for this operation is already in flight — wait for it to settle",
	"signer_mismatch":              "the signing key doesn't match the payment's payer/payee",
	// The SIWE BINDING failures, split out of signer_mismatch so a failed login says
	// WHICH part of the proof did not bind (rail0-gateway#216). None of these names the
	// server's expectation: that endpoint is unauthenticated, so echoing the allow-list
	// or the expected chain id would turn every hint into a probe.
	"siwe_domain_not_allowed": "sign with the origin the front-end is served from, and have it added to the gateway's SIWE domain allow-list",
	"siwe_uri_mismatch":       "the message's uri host must equal its own domain",
	"siwe_chain_mismatch":     "use the chain id the client library sends — this login is off-chain and nominal",
	"siwe_proof_expired":      "get a fresh nonce and sign again",
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
	// contract reverts (surfaced as contract_revert, or on a failed transaction)
	"not_payee":                  "only the merchant (payee) may do this",
	"not_payer":                  "only the buyer (payer) may do this",
	"not_payer_or_payee":         "only the payer or the payee may do this",
	"refund_expired":             "the refund window has closed (refundExpiry passed) — refund/dispute is no longer possible",
	"authorization_not_expired":  "release opens only after authorizationExpiry — wait until it passes",
	"already_captured":           "already (partially) captured — use release for the remainder, not void",
	"unsupported_payment_method": "the payee (merchant) doesn't accept this token/chain — check the merchant's payment methods",
	"idempotency_key_reused":     "that Idempotency-Key was already used for a payment with different terms — reuse it only to retry the same request, or pick a new key",
	"unknown_token":              "the token isn't configured on this chain",
	"no_active_contract":         "no active RAIL0 contract on that chain",
	"missing_param":              "a required parameter is missing from the request",
	// A BARE forbidden is not a party mismatch: the gateway split those into codes of
	// their own (not_the_payee, not_the_payer, wallet_deactivated, not_your_account)
	// because they need different fixes. This entry kept describing one of them long
	// after the split — and the rule it named no longer exists in the gateway at all.
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
