package rail0

// Payment (the full payment record, merged from the OpenAPI Payment and
// PaymentDetail schemas), Transaction, Wallet, WalletToken, and Error are
// generated from the gateway OpenAPI schema in types_gen.go.

// CreatePaymentRequest is the flat body for POST /payments.
type CreatePaymentRequest struct {
	ChainID     int     `json:"chain_id"`
	Mode        string  `json:"mode"`
	Amount      string  `json:"amount"`
	Token       Address `json:"token"`
	Payer       Address `json:"payer"`
	Payee       Address `json:"payee"`
	Description string  `json:"description,omitempty"`
	Metadata    *any    `json:"metadata,omitempty"`
}

// PayerSignatureRequest is the body for PUT /payments/{id}/sign.
type PayerSignatureRequest struct {
	// 65-byte secp256k1 signature (0x-prefixed, 132 chars): r (32) + s (32) + v (1).
	Signature string `json:"signature"`
}

// PrepareRequest is the optional body for POST /payments/{id}/{op}/prepare.
//   - Amount is required for capture and refund.
//   - Signature is the payee's EIP-3009 signature, required for refund phase-2.
//   - From overrides the submitter address (release; defaults to the payer).
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
// `title` a short label, `detail` a sentence fit to show a user. `status` and
// `message` are the same values under their older names — `status` carries the wider
// family (e.g. `forbidden`) where `code` is narrower, `message` equals `detail` — and
// Grape validation errors put their text in `error`. All of them are parsed so a
// useful code and text can be surfaced whatever the gateway's age.
type APIErrorBody struct {
	Code    string `json:"code"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Reason  string `json:"reason"`
}
