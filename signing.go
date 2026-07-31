package rail0

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// ================================================================
//  EIP-712 type strings and pre-computed type hashes
// ================================================================

const (
	domainTypeStr   = "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
	transferTypeStr = "TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
	receiveTypeStr  = "ReceiveWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
)

var (
	domainTypehash   = keccak256([]byte(domainTypeStr))
	transferTypehash = keccak256([]byte(transferTypeStr))
	receiveTypehash  = keccak256([]byte(receiveTypeStr))
)

// ================================================================
//  Public types
// ================================================================

// TokenDomain is the EIP-712 domain of the ERC-20 token (NOT the RAIL0 contract).
// Use the token's own name/version/chainId — for USDC on Base this is "USD Coin"/"2"/8453.
type TokenDomain struct {
	// Name is the token's EIP-712 name, e.g. "USD Coin" for USDC.
	Name string
	// Version is the token's EIP-712 version, e.g. "2" for USDC.
	Version string
	ChainID uint64
	// VerifyingContract is the token contract address used as verifyingContract in the domain.
	VerifyingContract Address
}

// Eip3009Signature is the EIP-3009 transferWithAuthorization signature. Pack it
// into the 65-byte hex string PayerSignatureRequest expects with:
//
//	packed := fmt.Sprintf("0x%s%s%02x", sig.R[2:], sig.S[2:], sig.V) // r || s || v
type Eip3009Signature struct {
	// V is the recovery identifier (27 or 28).
	V int
	R Bytes32
	S Bytes32
}

// SignTransferParams holds all fields for a raw transferWithAuthorization signature.
type SignTransferParams struct {
	From Address
	// To is the recipient of the transfer — the RAIL0 contract address.
	To Address
	// Value is the amount in token base units (e.g. 6 decimals for USDC).
	Value *big.Int
	// ValidAfter is the earliest timestamp the signature is valid. nil means 0 (immediate).
	ValidAfter *big.Int
	// ValidBefore is the latest timestamp the signature is valid.
	ValidBefore *big.Int
	// Nonce is a unique bytes32 that must not have been used before for this (From, To) pair.
	Nonce Bytes32
}

// SignPaymentParams holds parameters for SignAuthorize and SignCharge.
// Obtain the Nonce from the CreatePayment response: resp.SigningPayload.Message.Nonce.
// The contract hardcodes validAfter=0 and validBefore=Payment.AuthorizationExpiry;
// these are not configurable by the caller.
type SignPaymentParams struct {
	// PrivateKey is the 32-byte secp256k1 private key of the signer — the payer for
	// SignAuthorize/SignCharge, the payee for SignReceiveWithAuthorization.
	// Use HexToPrivateKey to convert from a 0x-prefixed hex string.
	PrivateKey []byte
	Payment    PaymentConfig
	// Amount is the value of the EIP-3009 authorization, in token base units.
	//
	// For SignAuthorize/SignCharge it is OPTIONAL and exists only as a
	// cross-check: the contract binds value to the payment's own amount, so the
	// value signed is always Payment.Amount. Leave it nil, or set it to the same
	// number and a mismatch is reported as an error instead of producing a
	// signature the gateway would reject.
	//
	// For SignReceiveWithAuthorization it is REQUIRED and free: a refund may be
	// partial, so the refunded amount is not derivable from Payment.Amount.
	Amount *big.Int
	// Nonce from CreatePayment response: resp.SigningPayload.Message.Nonce.
	Nonce           Bytes32
	ContractAddress Address
	TokenDomain     TokenDomain
}

// ================================================================
//  Helper: private key conversion
// ================================================================

// HexToPrivateKey decodes a 0x-prefixed or raw 64-char hex string into 32 raw bytes
// suitable for use as PrivateKey in SignPaymentParams or SignTransferWithAuthorization.
func HexToPrivateKey(hexKey string) ([]byte, error) {
	h := strings.TrimPrefix(hexKey, "0x")
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("rail0: invalid private key hex: %w", err)
	}
	if len(b) != 32 {
		return nil, errors.New("rail0: private key must be exactly 32 bytes")
	}
	return b, nil
}

// ================================================================
//  Internal: keccak256 and ABI encoding
// ================================================================

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func encodeAddress(addr Address) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
	out := make([]byte, 32) // 12 zero-pad + 20 bytes
	copy(out[12:], b)
	return out
}

func encodeUint256(v *big.Int) []byte {
	out := make([]byte, 32)
	b := v.Bytes()
	copy(out[32-len(b):], b)
	return out
}

func encodeBytes32(h Bytes32) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	out := make([]byte, 32)
	copy(out, b)
	return out
}

func concat(parts ...[]byte) []byte {
	var total int
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ================================================================
//  EIP-712 digest construction
// ================================================================

func hashDomainSeparator(domain TokenDomain) []byte {
	nameHash := keccak256([]byte(domain.Name))
	versionHash := keccak256([]byte(domain.Version))
	chainID := new(big.Int).SetUint64(domain.ChainID)
	return keccak256(concat(
		domainTypehash,
		nameHash,
		versionHash,
		encodeUint256(chainID),
		encodeAddress(domain.VerifyingContract),
	))
}

func hashStructTransfer(from, to Address, value, validAfter, validBefore *big.Int, nonce Bytes32) []byte {
	return keccak256(concat(
		transferTypehash,
		encodeAddress(from),
		encodeAddress(to),
		encodeUint256(value),
		encodeUint256(validAfter),
		encodeUint256(validBefore),
		encodeBytes32(nonce),
	))
}

func buildDigest(domain TokenDomain, from, to Address, value, validAfter, validBefore *big.Int, nonce Bytes32) []byte {
	return buildDigestWithTypehash(transferTypehash, domain, from, to, value, validAfter, validBefore, nonce)
}

func buildDigestWithTypehash(typehash []byte, domain TokenDomain, from, to Address, value, validAfter, validBefore *big.Int, nonce Bytes32) []byte {
	ds := hashDomainSeparator(domain)
	sh := keccak256(concat(
		typehash,
		encodeAddress(from),
		encodeAddress(to),
		encodeUint256(value),
		encodeUint256(validAfter),
		encodeUint256(validBefore),
		encodeBytes32(nonce),
	))
	return keccak256(concat([]byte{0x19, 0x01}, ds, sh))
}

// ================================================================
//  Core signing
// ================================================================

func doSign(privateKey []byte, digest []byte) (Eip3009Signature, error) {
	if len(privateKey) != 32 {
		return Eip3009Signature{}, errors.New("rail0: private key must be 32 bytes")
	}
	privKey := secp.PrivKeyFromBytes(privateKey)
	// isCompressedKey=false → first byte is 27 or 28 (Ethereum v convention).
	// SignCompact normalises S to the lower half of the curve order (low-S).
	compact := ecdsa.SignCompact(privKey, digest, false)
	// compact layout: [v(1), r(32), s(32)]
	v := int(compact[0])
	r := "0x" + hex.EncodeToString(compact[1:33])
	s := "0x" + hex.EncodeToString(compact[33:65])
	return Eip3009Signature{V: v, R: r, S: s}, nil
}

// authorizedValue resolves the value to sign for an authorize/charge digest.
//
// The RAIL0 contract passes the payment's own amount as the EIP-3009 `value`
// (`transferWithAuthorization(p.payer, address(this), p.amount, …)`) and the
// gateway builds the signing payload the same way, so exactly ONE value can ever
// produce an accepted signature: Payment.Amount. It is therefore derived here
// rather than taken from the caller. params.Amount is kept as an optional
// cross-check — when it is set and disagrees, the caller has a wrong number in
// hand and would otherwise learn about it only from a 422 signer_mismatch (or a
// reverted transaction after gas), so fail loudly and locally instead.
func authorizedValue(params SignPaymentParams) (*big.Int, error) {
	value, ok := new(big.Int).SetString(params.Payment.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("rail0: invalid Payment.Amount %q: want a base-unit decimal integer string", params.Payment.Amount)
	}
	if params.Amount != nil && params.Amount.Cmp(value) != 0 {
		return nil, fmt.Errorf("rail0: Amount %s disagrees with Payment.Amount %s: the contract signs the payment amount, so leave Amount nil or pass the same value",
			params.Amount, value)
	}
	return value, nil
}

// ================================================================
//  Public signing API
// ================================================================

// SignTransferWithAuthorization signs a raw EIP-3009 transferWithAuthorization message.
//
// For RAIL0 payment flows prefer SignAuthorize or SignCharge, which set From, To,
// and ValidBefore automatically from the Payment struct.
func SignTransferWithAuthorization(privateKey []byte, domain TokenDomain, params SignTransferParams) (Eip3009Signature, error) {
	validAfter := params.ValidAfter
	if validAfter == nil {
		validAfter = new(big.Int)
	}
	digest := buildDigest(domain, params.From, params.To, params.Value, validAfter, params.ValidBefore, params.Nonce)
	return doSign(privateKey, digest)
}

// SignAuthorize signs the EIP-3009 payload required by an Authorize call. The
// value signed is Payment.Amount (the only one the contract accepts — see
// SignPaymentParams.Amount). The nonce comes from the create response's signing
// payload; the packed signature is deposited via Payments.Sign (not spread into
// the submit call).
//
//	created, _ := client.Payments.Create(ctx, req)
//	cfg := rail0.PaymentConfig{Payer: created.Payer, Amount: created.Amount /* …from the create response */}
//	sig, _ := rail0.SignAuthorize(rail0.SignPaymentParams{
//	    PrivateKey: key, Payment: cfg,
//	    Nonce: created.SigningPayload.Message.Nonce, ContractAddress: contractAddr, TokenDomain: domain,
//	})
//	packed := fmt.Sprintf("0x%s%s%02x", sig.R[2:], sig.S[2:], sig.V)
//	client.Payments.Sign(ctx, created.Rail0Id, rail0.PayerSignatureRequest{Signature: packed})
func SignAuthorize(params SignPaymentParams) (Eip3009Signature, error) {
	value, err := authorizedValue(params)
	if err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigest(params.TokenDomain, params.Payment.Payer, params.ContractAddress,
		value, new(big.Int), big.NewInt(params.Payment.AuthorizationExpiry), params.Nonce)
	return doSign(params.PrivateKey, digest)
}

// SignCharge signs the EIP-3009 payload required by a Charge call. Identical
// digest to SignAuthorize (value = Payment.Amount included); the operation is
// distinguished by the nonce prefix the gateway encodes. The nonce comes from the
// create response's signing payload, and the packed signature is deposited via
// Payments.Sign.
//
//	created, _ := client.Payments.Create(ctx, req) // req.Mode == "charge"
//	cfg := rail0.PaymentConfig{Payer: created.Payer, Amount: created.Amount /* …from the create response */}
//	sig, _ := rail0.SignCharge(rail0.SignPaymentParams{
//	    PrivateKey: key, Payment: cfg,
//	    Nonce: created.SigningPayload.Message.Nonce, ContractAddress: contractAddr, TokenDomain: domain,
//	})
//	packed := fmt.Sprintf("0x%s%s%02x", sig.R[2:], sig.S[2:], sig.V)
//	client.Payments.Sign(ctx, created.Rail0Id, rail0.PayerSignatureRequest{Signature: packed})
func SignCharge(params SignPaymentParams) (Eip3009Signature, error) {
	value, err := authorizedValue(params)
	if err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigest(params.TokenDomain, params.Payment.Payer, params.ContractAddress,
		value, new(big.Int), big.NewInt(params.Payment.AuthorizationExpiry), params.Nonce)
	return doSign(params.PrivateKey, digest)
}

// SignReceiveWithAuthorization signs an EIP-3009 receiveWithAuthorization message.
// Used for refund flows where the payee authorises the token transfer back to the payer.
// The typehash differs from TransferWithAuthorization — FiatTokenV2 enforces both.
//
// The signer here is the PAYEE, not the payer: a refund moves funds the payee
// already holds, so the contract calls receiveWithAuthorization(p.payee,
// address(this), …) and `from` must be the payee — otherwise the token recovers a
// signer that isn't `from` and the transaction reverts. PrivateKey must be the
// payee's key, and Amount is the (possibly partial) refunded amount.
func SignReceiveWithAuthorization(params SignPaymentParams) (Eip3009Signature, error) {
	// Unlike authorize/charge there is nothing to derive it from — a refund may be
	// partial — and a nil *big.Int would panic inside the ABI encoding.
	if params.Amount == nil {
		return Eip3009Signature{}, errors.New("rail0: Amount is required for SignReceiveWithAuthorization (the refunded amount, in token base units)")
	}
	digest := buildDigestWithTypehash(receiveTypehash,
		params.TokenDomain, params.Payment.Payee, params.ContractAddress,
		params.Amount, new(big.Int), big.NewInt(params.Payment.RefundExpiry), params.Nonce)
	return doSign(params.PrivateKey, digest)
}

// SignReceiveParams holds the raw fields for a ReceiveWithAuthorization signature,
// taken directly from the signing payload returned by the API. Use this when
// the from/to/value fields are already known (e.g. from the signing payload)
// rather than derived from a PaymentConfig.
type SignReceiveParams struct {
	PrivateKey  []byte
	TokenDomain TokenDomain
	From        Address
	To          Address
	Value       *big.Int
	ValidAfter  *big.Int
	ValidBefore *big.Int
	Nonce       Bytes32
}

// SignReceiveWithAuthorizationRaw signs a ReceiveWithAuthorization message using
// the raw fields from the API signing payload, avoiding any field mapping errors.
func SignReceiveWithAuthorizationRaw(params SignReceiveParams) (Eip3009Signature, error) {
	digest := buildDigestWithTypehash(receiveTypehash,
		params.TokenDomain, params.From, params.To,
		params.Value, params.ValidAfter, params.ValidBefore, params.Nonce)
	return doSign(params.PrivateKey, digest)
}
