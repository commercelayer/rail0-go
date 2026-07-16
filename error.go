package rail0

import "fmt"

// APIError is returned when the RAIL0 API responds with a non-2xx status code.
type APIError struct {
	// Status is the HTTP status code (e.g. 404, 409, 422).
	Status int
	// Code is the machine-readable error identifier, e.g. "PaymentNotFound".
	Code string
	// Message is the human-readable description.
	Message string
	// Reason is an optional sub-cause the gateway attaches to some errors
	// (e.g. "rpc_unreachable" on a 502 signing_payload_unavailable); "" if absent.
	Reason string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("rail0 error (HTTP %d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("rail0 %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// Hint returns an actionable next-step hint for this error, or "" when the cause
// isn't a recognised code. It checks Code first, then the Reason sub-cause (which
// carries the specific error for a contract_revert).
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
	// contract reverts (surfaced as contract_revert, or on a failed transaction)
	"not_payee":                 "only the merchant (payee) may do this",
	"not_payer":                 "only the buyer (payer) may do this",
	"not_payer_or_payee":        "only the payer or the payee may do this",
	"refund_expired":            "the refund window has closed (refundExpiry passed) — refund/dispute is no longer possible",
	"authorization_not_expired": "release opens only after authorizationExpiry — wait until it passes",
	"already_captured":          "already (partially) captured — use release for the remainder, not void",
	"token_not_accepted":        "the token isn't in this deployment's allowlist",
	"payment_already_exists":    "a payment with this id already exists on-chain",
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
