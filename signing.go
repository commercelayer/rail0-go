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

// encodeUint256 left-pads v into one 32-byte ABI word. It requires 0 <= v < 2^256:
// a wider value would make the copy below slice out of range (a panic), and a
// negative one would be encoded as its absolute value (big.Int.Bytes drops the sign),
// silently signing a different number. Every public signer therefore validates its
// integers with checkUint256 before building a digest.
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

// uint256Max is 2^256 - 1, the largest value a uint256 ABI word can hold.
var uint256Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// uintArg names an integer about to be ABI-encoded, so a refusal says which field.
type uintArg struct {
	name string
	v    *big.Int
}

// checkUint256 refuses any integer encodeUint256 cannot represent faithfully: nil
// (which would panic on .Bytes()), negative (encoded as its absolute value) or at
// least 2^256 (which used to panic with "slice bounds out of range"). It is the guard
// every signer runs before building a digest, so hostile or mistyped input becomes an
// error naming the field instead of a crash or a signature over another number.
func checkUint256(args ...uintArg) error {
	for _, a := range args {
		switch {
		case a.v == nil:
			return fmt.Errorf("rail0: %s is required", a.name)
		case a.v.Sign() < 0:
			return fmt.Errorf("rail0: %s %s is negative: want a uint256", a.name, a.v)
		case a.v.Cmp(uint256Max) > 0:
			return fmt.Errorf("rail0: %s %s does not fit in a uint256 (must be below 2^256)", a.name, a.v)
		}
	}
	return nil
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
	if err := checkUint256(uintArg{"Payment.Amount", value}); err != nil {
		return nil, err
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

// Primary types the gateway can ask us to sign. Which one a given payment needs is
// the gateway's business, not ours: it builds its client per-payment with the
// deployment's contract_version, and the version selects the typehash, the EIP-712
// domain and the field layout.
const (
	PrimaryTypeTransfer = "TransferWithAuthorization"
	PrimaryTypeReceive  = "ReceiveWithAuthorization"
)

// SignSigningPayload signs the gateway's signing_payload VERBATIM, WITHOUT checking
// it against anything the caller asked for.
//
// Deprecated: use SignSigningPayloadFor, which runs VerifySigningPayload first. An
// EIP-3009 authorization is a bearer instrument the TOKEN honours directly, with no
// RAIL0 involvement, for up to message.value: a hostile or misconfigured endpoint that
// answers with {to: <attacker>, value: <your whole balance>} gets exactly that signed
// here. Kept for compatibility; it signs byte-identically to SignSigningPayloadFor once
// that function's checks have passed.
//
// The typehash comes from sp.PrimaryType, every message field from sp.Message, the
// domain from sp.Domain. Nothing is re-derived from a payment record, which is the
// whole point: only the gateway knows which contract version a payment lives on,
// and the version selects the typehash, the domain AND the field layout. A client
// that rebuilds the digest has re-imported the versioning the gateway exists to
// absorb — so a contract change produces a well-formed signature over the WRONG
// digest, which reports success and fails only on-chain, after gas, looking like a
// bad key. rail0#58 (authorize/charge moving to ReceiveWithAuthorization) is
// exactly that change; this function needs no edit for it. (#27)
//
// For a refund the key is the PAYEE's — the refund payload's `from` is the payee,
// since a refund moves funds the payee already holds.
func SignSigningPayload(privateKey []byte, sp *SigningPayload) (Eip3009Signature, error) {
	return signSigningPayload(privateKey, sp)
}

// signSigningPayload is the verbatim payload signer shared by the deprecated
// SignSigningPayload and the checked SignSigningPayloadFor, so neither calls the other
// and the two produce byte-identical signatures for the same payload.
func signSigningPayload(privateKey []byte, sp *SigningPayload) (Eip3009Signature, error) {
	if sp == nil {
		return Eip3009Signature{}, errors.New("rail0: signing payload is nil (Create answers 502 signing_payload_unavailable when it cannot build one)")
	}

	var typehash []byte
	switch sp.PrimaryType {
	case PrimaryTypeTransfer:
		typehash = transferTypehash
	case PrimaryTypeReceive:
		typehash = receiveTypehash
	default:
		// Never guess. Falling back to one of the two known typehashes is how a
		// client older than its gateway signs the wrong digest and reports success.
		return Eip3009Signature{}, fmt.Errorf(
			"rail0: unsupported signing payload primaryType %q (want %s or %s): this SDK is older than the gateway it is talking to",
			sp.PrimaryType, PrimaryTypeTransfer, PrimaryTypeReceive)
	}

	value, err := payloadInt("value", sp.Message.Value)
	if err != nil {
		return Eip3009Signature{}, err
	}
	validAfter, err := payloadInt("validAfter", sp.Message.ValidAfter)
	if err != nil {
		return Eip3009Signature{}, err
	}
	validBefore, err := payloadInt("validBefore", sp.Message.ValidBefore)
	if err != nil {
		return Eip3009Signature{}, err
	}

	// ChainId is an int on the wire; a negative one would wrap to a huge uint64.
	if sp.Domain.ChainId <= 0 {
		return Eip3009Signature{}, fmt.Errorf("rail0: invalid chainId %d in signing payload: want a positive chain id", sp.Domain.ChainId)
	}

	domain := TokenDomain{
		Name:              sp.Domain.Name,
		Version:           sp.Domain.Version,
		ChainID:           uint64(sp.Domain.ChainId),
		VerifyingContract: sp.Domain.VerifyingContract,
	}

	digest := buildDigestWithTypehash(typehash, domain, sp.Message.From, sp.Message.To,
		value, validAfter, validBefore, sp.Message.Nonce)
	return doSign(privateKey, digest)
}

// PackSignature renders {V, R, S} as the 0x + r || s || v hex that
// PUT /payments/{id}/sign expects. Every caller was assembling this by hand with
// a fmt.Sprintf that is easy to get subtly wrong (dropping the 0x slice, or
// formatting V without the leading zero).
func PackSignature(sig Eip3009Signature) string {
	return fmt.Sprintf("0x%s%s%02x", strings.TrimPrefix(sig.R, "0x"), strings.TrimPrefix(sig.S, "0x"), sig.V)
}

// payloadInt parses a decimal string field of the signing payload, naming the
// field so a malformed payload is diagnosable rather than a bare parse failure, and
// refuses a value outside the uint256 range.
func payloadInt(field string, raw Uint256String) (*big.Int, error) {
	n, ok := new(big.Int).SetString(string(raw), 10)
	if !ok {
		return nil, fmt.Errorf("rail0: invalid %s %q in signing payload: want a base-unit decimal integer string", field, raw)
	}
	// A parseable decimal can still be negative or wider than a uint256 — the latter
	// used to panic inside encodeUint256 (slice bounds out of range).
	if err := checkUint256(uintArg{field + " in signing payload", n}); err != nil {
		return nil, err
	}
	return n, nil
}

// SignTransferWithAuthorization signs a raw EIP-3009 transferWithAuthorization message.
//
// Prefer SignSigningPayloadFor: it checks the gateway's payload against what you asked
// for, then reads the typehash and every field from it, so it survives a contract
// change. This stays as the low-level
// escape hatch for talking to an EIP-3009 token directly, outside a RAIL0 payment.
func SignTransferWithAuthorization(privateKey []byte, domain TokenDomain, params SignTransferParams) (Eip3009Signature, error) {
	validAfter := params.ValidAfter
	if validAfter == nil {
		validAfter = new(big.Int)
	}
	if err := checkUint256(uintArg{"Value", params.Value}, uintArg{"ValidAfter", validAfter}, uintArg{"ValidBefore", params.ValidBefore}); err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigest(domain, params.From, params.To, params.Value, validAfter, params.ValidBefore, params.Nonce)
	return doSign(privateKey, digest)
}

// Deprecated: use SignSigningPayloadFor with created.SigningPayload. This rebuilds the
// digest from a payment record and hardcodes the Transfer typehash, so rail0#58
// (authorize/charge moving to ReceiveWithAuthorization) makes it sign a digest the
// token refuses, with no error here. (#27)
//
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
	validBefore := big.NewInt(params.Payment.AuthorizationExpiry)
	if err := checkUint256(uintArg{"Payment.AuthorizationExpiry", validBefore}); err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigest(params.TokenDomain, params.Payment.Payer, params.ContractAddress,
		value, new(big.Int), validBefore, params.Nonce)
	return doSign(params.PrivateKey, digest)
}

// Deprecated: use SignSigningPayloadFor with created.SigningPayload — see SignAuthorize. (#27)
//
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
	validBefore := big.NewInt(params.Payment.AuthorizationExpiry)
	if err := checkUint256(uintArg{"Payment.AuthorizationExpiry", validBefore}); err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigest(params.TokenDomain, params.Payment.Payer, params.ContractAddress,
		value, new(big.Int), validBefore, params.Nonce)
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
	validBefore := big.NewInt(params.Payment.RefundExpiry)
	if err := checkUint256(uintArg{"Amount", params.Amount}, uintArg{"Payment.RefundExpiry", validBefore}); err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigestWithTypehash(receiveTypehash,
		params.TokenDomain, params.Payment.Payee, params.ContractAddress,
		params.Amount, new(big.Int), validBefore, params.Nonce)
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
//
// Value, ValidAfter and ValidBefore are all required and must lie in the uint256
// range; anything else is an error rather than a panic or a signature over another
// number.
func SignReceiveWithAuthorizationRaw(params SignReceiveParams) (Eip3009Signature, error) {
	if err := checkUint256(uintArg{"Value", params.Value}, uintArg{"ValidAfter", params.ValidAfter}, uintArg{"ValidBefore", params.ValidBefore}); err != nil {
		return Eip3009Signature{}, err
	}
	digest := buildDigestWithTypehash(receiveTypehash,
		params.TokenDomain, params.From, params.To,
		params.Value, params.ValidAfter, params.ValidBefore, params.Nonce)
	return doSign(params.PrivateKey, digest)
}
