package rail0

// Payment is the full payment record returned by the payment endpoints.
// chain_id, rail0_contract, signing_payload, and transactions are present only
// on the endpoints/states that populate them.
type Payment struct {
	ID                  string         `json:"id"`
	ContractID          string         `json:"contract_id"`
	Rail0Id             Bytes32        `json:"rail0_id"`
	Status              string         `json:"status"`
	Mode                string         `json:"mode"`
	Amount              Uint256String  `json:"amount"`
	ConfigHash          Bytes32        `json:"config_hash"`
	Payer               Address        `json:"payer"`
	Payee               Address        `json:"payee"`
	Token               Address        `json:"token"`
	AuthorizationExpiry int64          `json:"authorization_expiry"`
	RefundExpiry        int64          `json:"refund_expiry"`
	Signature           string         `json:"signature,omitempty"`
	LastBroadcastHash   string         `json:"last_broadcast_hash,omitempty"`
	LastErrorCode       string         `json:"last_error_code,omitempty"`
	LastErrorMessage    string         `json:"last_error_message,omitempty"`
	Description         string         `json:"description,omitempty"`
	Metadata            *any           `json:"metadata,omitempty"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at,omitempty"`

	// Present only on some endpoints/states:
	ChainID        int             `json:"chain_id,omitempty"`
	Rail0Contract  Address         `json:"rail0_contract,omitempty"`
	SigningPayload *SigningPayload `json:"signing_payload,omitempty"`
	Transactions   []Transaction   `json:"transactions,omitempty"`
}

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

// Transaction is an on-chain transaction attempt associated with a payment.
// SigningPayload is populated only by the refund prepare phase-1 response.
type Transaction struct {
	ID                  string        `json:"id"`
	PaymentID           string        `json:"payment_id"`
	Operation           string        `json:"operation"`
	Status              string        `json:"status"`
	UnsignedTransaction string        `json:"unsigned_transaction,omitempty"`
	SignedTransaction   string        `json:"signed_transaction,omitempty"`
	TransactionHash     *Bytes32      `json:"transaction_hash,omitempty"`
	Amount              Uint256String `json:"amount,omitempty"`
	BlockNumber         *int          `json:"block_number,omitempty"`
	ErrorReason         string        `json:"error_reason,omitempty"`
	PendingAt           string        `json:"pending_at,omitempty"`
	SubmittedAt         string        `json:"submitted_at,omitempty"`
	ConfirmedAt         string        `json:"confirmed_at,omitempty"`
	CreatedAt           string        `json:"created_at,omitempty"`
	UpdatedAt           string        `json:"updated_at,omitempty"`

	// SigningPayload is returned by the refund prepare phase-1 step (no signature
	// supplied yet) so the payee can sign the EIP-3009 authorization.
	SigningPayload *SigningPayload `json:"signing_payload,omitempty"`
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

// WalletToken is a wallet-token join row returned by the account wallet list and
// single-get endpoints — one row per (wallet, token) pairing on a chain.
type WalletToken struct {
	ID            string  `json:"id"`
	WalletID      string  `json:"wallet_id"`
	Address       Address `json:"address"`
	Label         string  `json:"label,omitempty"`
	Default       bool    `json:"default"`
	Active        bool    `json:"active"`
	TokenID       string  `json:"token_id"`
	TokenSymbol   string  `json:"token_symbol"`
	TokenAddress  Address `json:"token_address"`
	TokenDecimals int     `json:"token_decimals"`
	ChainID       int     `json:"chain_id"`
	ChainName     string  `json:"chain_name"`
	ExplorerURL   string  `json:"explorer_url,omitempty"`
}

// Wallet is the plain wallet record returned by create/update wallet endpoints.
type Wallet struct {
	ID        string  `json:"id"`
	AccountID string  `json:"account_id"`
	Address   Address `json:"address"`
	Label     string  `json:"label,omitempty"`
	Active    bool    `json:"active"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

// APIErrorBody is the JSON shape of error responses from the RAIL0 API.
// Used internally by the HTTP client to parse non-2xx responses.
type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
