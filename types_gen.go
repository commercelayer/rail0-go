// This file was originally generated from rail0-api/docs/openapi.json. The
// generator's schema source (rail0-api) is currently out of scope, so this file
// is now hand-maintained. It holds the shared primitive aliases and the EIP-712 /
// EIP-3009 signing types used by signing.go. Domain request/response shapes live
// in types.go alongside their services.
//
// When the gateway publishes an OpenAPI document again, regenerate via gen/.

package rail0

// Address is a type alias for string. EVM address (checksummed or lowercase hex, 20 bytes).
type Address = string

// Bytes32 is a type alias for string. 32-byte hex value.
type Bytes32 = string

// Uint256String is a type alias for string. Non-negative integer encoded as a decimal string to avoid JSON numeric precision loss.
type Uint256String = string

// EIP3009Message Message fields for the EIP-3009 TransferWithAuthorization signature.
type EIP3009Message struct {
	From Address `json:"from"`
	// keccak256(NONCE_PREFIX, rail0_id, config_hash). Binds the signature to the exact config and operation type.
	Nonce Bytes32 `json:"nonce"`
	// RAIL0 contract address.
	To Address `json:"to"`
	// Always '0' for RAIL0 flows.
	ValidAfter Uint256String `json:"validAfter"`
	// Equals authorization_expiry.
	ValidBefore Uint256String `json:"validBefore"`
	Value Uint256String `json:"value"`
}

// EIP712Domain EIP-712 domain for the token contract.
type EIP712Domain struct {
	ChainId int    `json:"chainId"`
	Name    string `json:"name"`
	// Token contract address.
	VerifyingContract Address `json:"verifyingContract"`
	Version           string  `json:"version"`
}

// PaymentConfig Immutable payment configuration that maps 1-to-1 to the RAIL0 `Payment` Solidity struct.
type PaymentConfig struct {
	// Exact amount the payer commits to pay (in token base units).
	Amount Uint256String `json:"amount"`
	// Unix timestamp (seconds). Capture must happen before this; release opens after.
	AuthorizationExpiry int64 `json:"authorization_expiry"`
	// Account address. Authorized to capture, void, and refund.
	Payee Address `json:"payee"`
	// Buyer address. Funds are pulled from this address.
	Payer Address `json:"payer"`
	// Unix timestamp (seconds). Refund must happen before this. Must be >= authorization_expiry.
	RefundExpiry int64 `json:"refund_expiry"`
	// EIP-3009-capable ERC-20 token address (must be accepted by the RAIL0 deployment).
	Token Address `json:"token"`
}

// SigningPayload EIP-712 typed-data structure that the payer (or payee for refund) must sign. Pass verbatim to `eth_signTypedData_v4`.
type SigningPayload struct {
	Domain      EIP712Domain   `json:"domain"`
	Message     EIP3009Message `json:"message"`
	PrimaryType string         `json:"primaryType"`
	Types       any            `json:"types"`
}

// Error is the JSON shape of error responses from the RAIL0 API.
type Error struct {
	// Machine-readable error code.
	Code string `json:"code"`
	// Human-readable error description.
	Message string `json:"message"`
}

// PageMeta holds pagination metadata returned by list endpoints.
type PageMeta struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
}

// PaginatedResponse is a generic paginated list response.
type PaginatedResponse[T any] struct {
	Data []T      `json:"data"`
	Meta PageMeta `json:"meta"`
}
