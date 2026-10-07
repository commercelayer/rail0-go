package rail0

// Payment (the full payment record, merged from the OpenAPI Payment and
// PaymentDetail schemas), Transaction, Wallet, and Error are generated from the
// gateway OpenAPI schema in types_gen.go.

// CreatePaymentRequest is the flat body for POST /payments.
type CreatePaymentRequest struct {
	ChainID int    `json:"chain_id"`
	Mode    string `json:"mode"`
	// Human decimal amount (e.g. "10.50") — the gateway converts to token base units.
	Amount      string  `json:"amount"`
	Token       Address `json:"token"`
	Payer       Address `json:"payer"`
	Payee       Address `json:"payee"`
	Description string  `json:"description,omitempty"`
	Metadata    *any    `json:"metadata,omitempty"`
}

// UpdatePaymentRequest is the body for PATCH /payments/{id}. Description is the
// only mutable field.
//
// The key is required by the gateway, so the field has no omitempty: a nil
// Description is sent as JSON null and, like a pointer to "", clears the stored
// description. A non-empty value replaces it (max 255 characters — the gateway
// answers 422 above that).
type UpdatePaymentRequest struct {
	Description *string `json:"description"`
}

// PayerSignatureRequest is the body for PUT /payments/{id}/sign.
type PayerSignatureRequest struct {
	// 65-byte secp256k1 signature (0x-prefixed, 132 chars): r (32) + s (32) + v (1).
	Signature string `json:"signature"`
}

// PrepareRequest is the optional body for POST /payments/{id}/{op}/prepare.
//   - Amount is required for capture and refund — a human decimal (e.g. "10.50");
//     the gateway converts to token base units.
//   - Signature is the payee's EIP-3009 signature, required for refund phase-2.
//   - From is the release submitter — the payer or the payee; empty means the
//     signed-in caller.
//
// Omit Signature on the refund prepare call to receive the EIP-3009 signing
// payload (refund phase-1) instead of an unsigned transaction.
type PrepareRequest struct {
	Amount    string  `json:"amount,omitempty"`
	Signature string  `json:"signature,omitempty"`
	From      Address `json:"from,omitempty"`
}

// SubmitTransactionRequest is the body for POST /payments/{id}/{op}.
type SubmitTransactionRequest struct {
	// RLP-encoded signed EIP-1559 transaction (0x-prefixed).
	SignedTransaction string `json:"signed_transaction"`
}

// SubmitByHashRequest reports a transaction the caller broadcast themselves
// (MetaMask/wallet flow) to POST /payments/{id}/{op}/submitted.
type SubmitByHashRequest struct {
	TransactionHash string `json:"transaction_hash"`
}

// DisputeDetail is a Dispute with its parent payment embedded (the gateway's
// DisputeDetail schema), returned by the account-level DisputesService.List.
// The embedded Dispute promotes its fields, so a DisputeDetail marshals/unmarshals
// exactly like a Dispute plus the extra `payment` object.
type DisputeDetail struct {
	Dispute
	Payment *Payment `json:"payment,omitempty"`
}

// APIErrorBody is the JSON shape of error responses from the RAIL0 API.
// Used internally by the HTTP client to parse non-2xx responses.
//
// The gateway sends a code/title/detail triple: `code` is the specific condition,
// `title` a short label, `detail` a sentence fit to show a user.
//
// Exactly those three, plus whatever context field a route attaches: the gateway trimmed
// the shape and DELETED the older aliases rather than dual-sending them (`status`, the
// wider family; `message`, equal to detail; `error`, Grape's validation text) — see
// rail0-gateway#252. Parsing them was how an older gateway still yielded a useful code;
// there is no older gateway to serve now, and a field that cannot arrive is a fallback
// that cannot fire.
//
// `reason` stays because it is not an alias: it is a genuine sub-cause one route still
// attaches (`signing_payload_unavailable` names the RPC failure behind a 502).
type APIErrorBody struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Reason string `json:"reason"`
}
