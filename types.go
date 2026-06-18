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

// APIErrorBody is the JSON shape of error responses from the RAIL0 API.
// Used internally by the HTTP client to parse non-2xx responses.
type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
