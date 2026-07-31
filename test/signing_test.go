package rail0_test

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"

	rail0 "github.com/rail0/go-sdk"
)

// ================================================================
//  Test fixtures — same vectors as signing.test.ts
// ================================================================

const (
	privateKeyHex   = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	payerAddress    = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	tokenAddress    = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	contractAddress = "0x1234567890123456789012345678901234567890"
	testNonce       = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// A second key/address pair, so a refund test can sign as the PAYEE and assert
	// which address the signature recovers to (the payer key cannot prove that).
	payeePrivateKeyHex = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	payeeAddress       = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
)

var testDomain = rail0.TokenDomain{
	Name:              "USD Coin",
	Version:           "2",
	ChainID:           8453,
	VerifyingContract: tokenAddress,
}

var testPayment = rail0.PaymentConfig{
	Payer:               payerAddress,
	Payee:               "0x2222222222222222222222222222222222222222",
	Token:               tokenAddress,
	Amount:              "100000000",
	AuthorizationExpiry: 9999999999,
	RefundExpiry:        9999999999 + 60*60*24*7,
}

// ================================================================
//  Helpers: keccak256 and digest recomputation for cross-checking
// ================================================================

func testKeccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func concatBytes(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func encAddr(addr string) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
	out := make([]byte, 32)
	copy(out[12:], b)
	return out
}

func encUint(v *big.Int) []byte {
	out := make([]byte, 32)
	b := v.Bytes()
	copy(out[32-len(b):], b)
	return out
}

func encB32(h string) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	out := make([]byte, 32)
	copy(out, b)
	return out
}

// recomputeDigest independently replicates the EIP-712 digest so tests can
// cross-check the implementation without calling its internal functions.
func recomputeDigest(domain rail0.TokenDomain, from, to string, value, validAfter, validBefore *big.Int, nonce string) []byte {
	return recomputeDigestWithType(
		"TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)",
		domain, from, to, value, validAfter, validBefore, nonce)
}

// recomputeReceiveDigest is recomputeDigest for the ReceiveWithAuthorization
// typehash — the one a refund authorization is signed under.
func recomputeReceiveDigest(domain rail0.TokenDomain, from, to string, value, validAfter, validBefore *big.Int, nonce string) []byte {
	return recomputeDigestWithType(
		"ReceiveWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)",
		domain, from, to, value, validAfter, validBefore, nonce)
}

func recomputeDigestWithType(typeStr string, domain rail0.TokenDomain, from, to string, value, validAfter, validBefore *big.Int, nonce string) []byte {
	domainTypehash := testKeccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	structTypehash := testKeccak256([]byte(typeStr))

	nameHash := testKeccak256([]byte(domain.Name))
	versionHash := testKeccak256([]byte(domain.Version))
	chainID := new(big.Int).SetUint64(domain.ChainID)

	ds := testKeccak256(concatBytes(domainTypehash, nameHash, versionHash, encUint(chainID), encAddr(domain.VerifyingContract)))
	sh := testKeccak256(concatBytes(structTypehash, encAddr(from), encAddr(to), encUint(value), encUint(validAfter), encUint(validBefore), encB32(nonce)))
	return testKeccak256(concatBytes([]byte{0x19, 0x01}, ds, sh))
}

// recoverSigner recovers the Ethereum address that produced sig over digest.
func recoverSigner(t *testing.T, sig rail0.Eip3009Signature, digest []byte) string {
	t.Helper()
	var compact [65]byte
	compact[0] = byte(sig.V)
	r, _ := hex.DecodeString(strings.TrimPrefix(sig.R, "0x"))
	s, _ := hex.DecodeString(strings.TrimPrefix(sig.S, "0x"))
	copy(compact[1:33], r)
	copy(compact[33:65], s)

	pub, _, err := ecdsa.RecoverCompact(compact[:], digest)
	if err != nil {
		t.Fatalf("RecoverCompact: %v", err)
	}
	pubBytes := pub.SerializeUncompressed() // [0x04, x(32), y(32)]
	addrHash := testKeccak256(pubBytes[1:]) // keccak of the 64-byte key material
	return "0x" + hex.EncodeToString(addrHash[12:])
}

// ================================================================
//  HexToPrivateKey
// ================================================================

func TestHexToPrivateKey(t *testing.T) {
	key, err := rail0.HexToPrivateKey(privateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(key))
	}
}

func TestHexToPrivateKey_ErrorOnBadHex(t *testing.T) {
	if _, err := rail0.HexToPrivateKey("0xnothex"); err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestHexToPrivateKey_ErrorOnWrongLength(t *testing.T) {
	if _, err := rail0.HexToPrivateKey("0xdeadbeef"); err == nil {
		t.Error("expected error for key shorter than 32 bytes")
	}
}

// ================================================================
//  SignTransferWithAuthorization
// ================================================================

func TestSignTransferWithAuthorization_ReturnsVRS(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	sig, err := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From:        payerAddress,
		To:          contractAddress,
		Value:       big.NewInt(50_000_000),
		ValidBefore: big.NewInt(9999999999),
		Nonce:       testNonce,
	})
	if err != nil {
		t.Fatal(err)
	}

	if sig.V != 27 && sig.V != 28 {
		t.Errorf("v must be 27 or 28, got %d", sig.V)
	}
	if len(sig.R) != 66 {
		t.Errorf("R must be 66 chars (0x + 64 hex), got %d", len(sig.R))
	}
	if len(sig.S) != 66 {
		t.Errorf("S must be 66 chars (0x + 64 hex), got %d", len(sig.S))
	}
}

func TestSignTransferWithAuthorization_RecoversPayer(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	value := big.NewInt(50_000_000)
	validAfter := new(big.Int)
	validBefore := big.NewInt(9999999999)

	sig, err := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From:        payerAddress,
		To:          contractAddress,
		Value:       value,
		ValidAfter:  validAfter,
		ValidBefore: validBefore,
		Nonce:       testNonce,
	})
	if err != nil {
		t.Fatal(err)
	}

	digest := recomputeDigest(testDomain, payerAddress, contractAddress, value, validAfter, validBefore, testNonce)
	recovered := recoverSigner(t, sig, digest)

	if !strings.EqualFold(recovered, payerAddress) {
		t.Errorf("recovered address %s does not match payer %s", recovered, payerAddress)
	}
}

func TestSignTransferWithAuthorization_ValidAfterDefaultsToZero(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	params := rail0.SignTransferParams{
		From: payerAddress, To: contractAddress,
		Value: big.NewInt(1), ValidBefore: big.NewInt(9999999999), Nonce: testNonce,
	}
	withDefault, err := rail0.SignTransferWithAuthorization(key, testDomain, params)
	if err != nil {
		t.Fatal(err)
	}

	params.ValidAfter = new(big.Int) // explicit 0
	withExplicit, err := rail0.SignTransferWithAuthorization(key, testDomain, params)
	if err != nil {
		t.Fatal(err)
	}

	if withDefault != withExplicit {
		t.Error("nil ValidAfter should produce the same signature as explicit 0")
	}
}

func TestSignTransferWithAuthorization_DifferentNoncesProduceDifferentSigs(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)
	nonce2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	sig1, _ := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From: payerAddress, To: contractAddress, Value: big.NewInt(1), ValidBefore: big.NewInt(9999999999), Nonce: testNonce,
	})
	sig2, _ := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From: payerAddress, To: contractAddress, Value: big.NewInt(1), ValidBefore: big.NewInt(9999999999), Nonce: nonce2,
	})

	if sig1.R == sig2.R {
		t.Error("different nonces must produce different signatures")
	}
}

// ================================================================
//  SignAuthorize
// ================================================================

func TestSignAuthorize_ReturnsValidSignature(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	sig, err := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sig.V != 27 && sig.V != 28 {
		t.Errorf("v must be 27 or 28, got %d", sig.V)
	}
}

func TestSignAuthorize_MatchesSignTransferWithExplicitParams(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	manual, _ := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From:        testPayment.Payer,
		To:          contractAddress,
		Value:       big.NewInt(100_000_000), // testPayment.Amount — the only value the contract signs
		ValidAfter:  new(big.Int),
		ValidBefore: big.NewInt(testPayment.AuthorizationExpiry),
		Nonce:       testNonce,
	})
	auto, _ := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})

	if manual != auto {
		t.Errorf("SignAuthorize should match SignTransferWithAuthorization with explicit params\nmanual=%+v\nauto=%+v", manual, auto)
	}
}

// The value is taken from Payment.Amount, so passing the same number explicitly
// changes nothing — Amount is a cross-check, not an input.
func TestSignAuthorize_ExplicitMatchingAmountIsANoOp(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	params := rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	}
	derived, err := rail0.SignAuthorize(params)
	if err != nil {
		t.Fatal(err)
	}

	params.Amount = big.NewInt(100_000_000) // == testPayment.Amount
	explicit, err := rail0.SignAuthorize(params)
	if err != nil {
		t.Fatal(err)
	}
	if derived != explicit {
		t.Error("an Amount equal to Payment.Amount must produce the same signature")
	}
}

// A disagreeing Amount is a caller bug that would otherwise surface as a 422
// signer_mismatch (or a reverted transaction), so it must fail locally.
func TestSignAuthorize_RejectsAmountDisagreeingWithPayment(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	if _, err := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment, // Amount "100000000"
		Amount:          big.NewInt(50_000_000),
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	}); err == nil {
		t.Error("expected an error when Amount disagrees with Payment.Amount")
	}

	if _, err := rail0.SignCharge(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Amount:          big.NewInt(50_000_000),
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	}); err == nil {
		t.Error("SignCharge: expected an error when Amount disagrees with Payment.Amount")
	}
}

func TestSignAuthorize_RejectsUnparseablePaymentAmount(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	bad := testPayment
	bad.Amount = "10.50" // a human decimal, not the base-unit integer the digest needs
	if _, err := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         bad,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	}); err == nil {
		t.Error("expected an error for a non-integer Payment.Amount")
	}
}

// ================================================================
//  SignCharge
// ================================================================

func TestSignCharge_ReturnsValidSignature(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	sig, err := rail0.SignCharge(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sig.V != 27 && sig.V != 28 {
		t.Errorf("v must be 27 or 28, got %d", sig.V)
	}
}

func TestSignCharge_DifferentSignatureFromSignAuthorize(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	authSig, _ := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})
	chargeSig, _ := rail0.SignCharge(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Nonce:           "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})

	if authSig.R == chargeSig.R {
		t.Error("SignCharge with a different nonce must produce a different signature than SignAuthorize")
	}
}

// ================================================================
//  SignReceiveWithAuthorization (refund — signed by the PAYEE)
// ================================================================

// The refund authorization is signed by the payee and names the payee as `from`
// (RAIL0 calls receiveWithAuthorization(p.payee, address(this), …)). Recovering the
// signer over a digest built with from=payee is the only assertion that pins the
// mapping: a from=payer digest still yields a well-formed VRS, it just recovers to
// an address that isn't `from`, and the token reverts on-chain.
func TestSignReceiveWithAuthorization_RecoversPayee(t *testing.T) {
	payeeKey, err := rail0.HexToPrivateKey(payeePrivateKeyHex)
	if err != nil {
		t.Fatal(err)
	}

	payment := testPayment
	payment.Payee = payeeAddress // so recovery can be compared against a known key

	value := big.NewInt(40_000_000) // a partial refund: not Payment.Amount
	sig, err := rail0.SignReceiveWithAuthorization(rail0.SignPaymentParams{
		PrivateKey:      payeeKey,
		Payment:         payment,
		Amount:          value,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})
	if err != nil {
		t.Fatal(err)
	}

	digest := recomputeReceiveDigest(testDomain, payeeAddress, contractAddress,
		value, new(big.Int), big.NewInt(payment.RefundExpiry), testNonce)
	if recovered := recoverSigner(t, sig, digest); !strings.EqualFold(recovered, payeeAddress) {
		t.Errorf("recovered address %s does not match payee %s — the digest must use from=payee", recovered, payeeAddress)
	}

	// And the payer must NOT be the `from`: the same signature over a payer-from
	// digest recovers to something else, which is exactly the on-chain revert.
	payerFrom := recomputeReceiveDigest(testDomain, payment.Payer, contractAddress,
		value, new(big.Int), big.NewInt(payment.RefundExpiry), testNonce)
	if recoverSigner(t, sig, payerFrom) == recoverSigner(t, sig, digest) {
		t.Error("payer-from and payee-from digests must not recover to the same address")
	}
}

// The refunded amount cannot be derived (a refund may be partial), so a missing
// one is an error rather than a panic in the ABI encoding.
func TestSignReceiveWithAuthorization_RequiresAmount(t *testing.T) {
	payeeKey, _ := rail0.HexToPrivateKey(payeePrivateKeyHex)

	if _, err := rail0.SignReceiveWithAuthorization(rail0.SignPaymentParams{
		PrivateKey:      payeeKey,
		Payment:         testPayment,
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	}); err == nil {
		t.Error("expected an error when Amount is nil")
	}
}

// ================================================================
//  SignSigningPayload — the payload-verbatim signer (#27)
// ================================================================
//
// The rule these pin: the gateway builds the signing payload, and the client signs
// it as given. Which typehash to use is stated by the payload's primaryType, never
// inferred. A rebuilt digest yields a well-formed signature over the WRONG digest —
// reported as success, failing only on-chain, after gas.

func testPayload(primaryType string) *rail0.SigningPayload {
	return &rail0.SigningPayload{
		PrimaryType: primaryType,
		Domain: rail0.EIP712Domain{
			Name:              "USD Coin",
			Version:           "2",
			ChainId:           8453,
			VerifyingContract: tokenAddress,
		},
		Message: rail0.EIP3009Message{
			From:        payerAddress,
			To:          contractAddress,
			Value:       "50000000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       testNonce,
		},
	}
}

// The digest is cross-checked against an independent recomputation, per primary
// type — so a mistake in the field mapping cannot pass by agreeing with itself.
func TestSignSigningPayload_MatchesTheIndependentDigest(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	for _, tc := range []struct {
		primaryType string
		digest      func() []byte
	}{
		{rail0.PrimaryTypeTransfer, func() []byte {
			return recomputeDigest(testDomain, payerAddress, contractAddress,
				big.NewInt(50_000_000), new(big.Int), big.NewInt(9999999999), testNonce)
		}},
		{rail0.PrimaryTypeReceive, func() []byte {
			return recomputeReceiveDigest(testDomain, payerAddress, contractAddress,
				big.NewInt(50_000_000), new(big.Int), big.NewInt(9999999999), testNonce)
		}},
	} {
		sig, err := rail0.SignSigningPayload(key, testPayload(tc.primaryType))
		if err != nil {
			t.Fatalf("%s: %v", tc.primaryType, err)
		}
		if got := recoverSigner(t, sig, tc.digest()); !strings.EqualFold(got, payerAddress) {
			t.Errorf("%s: recovered %s, want %s — the payload fields are not reaching the digest",
				tc.primaryType, got, payerAddress)
		}
	}
}

// The typehash is part of the digest, so identical signatures would mean
// primaryType was ignored.
func TestSignSigningPayload_HonoursPrimaryType(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	transfer, err := rail0.SignSigningPayload(key, testPayload(rail0.PrimaryTypeTransfer))
	if err != nil {
		t.Fatal(err)
	}
	receive, err := rail0.SignSigningPayload(key, testPayload(rail0.PrimaryTypeReceive))
	if err != nil {
		t.Fatal(err)
	}

	if rail0.PackSignature(transfer) == rail0.PackSignature(receive) {
		t.Error("the two primary types produced the same signature — the typehash is being ignored")
	}
}

// A silent fallback to one of the two known typehashes is how a client older than
// its gateway signs the wrong digest and reports success.
func TestSignSigningPayload_RefusesAnUnknownPrimaryType(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	_, err := rail0.SignSigningPayload(key, testPayload("SomeFutureAuthorization"))
	if err == nil {
		t.Fatal("an unknown primaryType must not be signed")
	}
	if !strings.Contains(err.Error(), "SomeFutureAuthorization") {
		t.Errorf("the error should name the offending primaryType, got: %v", err)
	}
	// It must also say WHY, so the reader reaches for an upgrade rather than a key.
	if !strings.Contains(err.Error(), "older than the gateway") {
		t.Errorf("the error should point at the version skew, got: %v", err)
	}

	if _, err := rail0.SignSigningPayload(key, testPayload("")); err == nil {
		t.Error("an empty primaryType must not be signed either")
	}
}

// Everything signed comes from the payload, so changing any one field must change
// the signature. This is what fails if someone reintroduces a record-derived path.
func TestSignSigningPayload_ReadsEveryPayloadField(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	base, err := rail0.SignSigningPayload(key, testPayload(rail0.PrimaryTypeTransfer))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		field  string
		mutate func(*rail0.SigningPayload)
	}{
		{"message.value", func(p *rail0.SigningPayload) { p.Message.Value = "50000001" }},
		{"message.from", func(p *rail0.SigningPayload) { p.Message.From = payeeAddress }},
		{"message.to", func(p *rail0.SigningPayload) { p.Message.To = payeeAddress }},
		{"message.validAfter", func(p *rail0.SigningPayload) { p.Message.ValidAfter = "1" }},
		{"message.validBefore", func(p *rail0.SigningPayload) { p.Message.ValidBefore = "8888888888" }},
		{"message.nonce", func(p *rail0.SigningPayload) { p.Message.Nonce = strings.Replace(testNonce, "aa", "bb", -1) }},
		{"domain.name", func(p *rail0.SigningPayload) { p.Domain.Name = "Other Coin" }},
		{"domain.version", func(p *rail0.SigningPayload) { p.Domain.Version = "1" }},
		{"domain.chainId", func(p *rail0.SigningPayload) { p.Domain.ChainId = 1 }},
		{"domain.verifyingContract", func(p *rail0.SigningPayload) { p.Domain.VerifyingContract = contractAddress }},
	} {
		p := testPayload(rail0.PrimaryTypeTransfer)
		tc.mutate(p)
		got, err := rail0.SignSigningPayload(key, p)
		if err != nil {
			t.Fatalf("%s: %v", tc.field, err)
		}
		if rail0.PackSignature(got) == rail0.PackSignature(base) {
			t.Errorf("changing %s did not change the signature — that field is not read from the payload", tc.field)
		}
	}
}

// These arrive off the wire, so "invalid value" without the field name is a dead end.
func TestSignSigningPayload_NamesAMalformedField(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)

	for _, tc := range []struct {
		field  string
		mutate func(*rail0.SigningPayload)
	}{
		{"value", func(p *rail0.SigningPayload) { p.Message.Value = "not-a-number" }},
		{"validAfter", func(p *rail0.SigningPayload) { p.Message.ValidAfter = "" }},
		{"validBefore", func(p *rail0.SigningPayload) { p.Message.ValidBefore = "0x10" }},
	} {
		p := testPayload(rail0.PrimaryTypeTransfer)
		tc.mutate(p)
		_, err := rail0.SignSigningPayload(key, p)
		if err == nil {
			t.Errorf("%s: a malformed field must be rejected", tc.field)
			continue
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Errorf("the error should name %q, got: %v", tc.field, err)
		}
	}
}

func TestSignSigningPayload_RejectsANilPayload(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)
	if _, err := rail0.SignSigningPayload(key, nil); err == nil {
		t.Error("a nil signing payload must be rejected")
	}
}

// A refund payload's `from` is the PAYEE — the funds being moved are already the
// merchant's — so signing it with the payee key must recover the payee.
func TestSignSigningPayload_RefundRecoversThePayee(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(payeePrivateKeyHex)

	p := testPayload(rail0.PrimaryTypeReceive)
	p.Message.From = payeeAddress

	sig, err := rail0.SignSigningPayload(key, p)
	if err != nil {
		t.Fatal(err)
	}

	digest := recomputeReceiveDigest(testDomain, payeeAddress, contractAddress,
		big.NewInt(50_000_000), new(big.Int), big.NewInt(9999999999), testNonce)
	if got := recoverSigner(t, sig, digest); !strings.EqualFold(got, payeeAddress) {
		t.Errorf("recovered %s, want the payee %s", got, payeeAddress)
	}
}

// PackSignature is the format PUT /payments/{id}/sign takes; every caller was
// assembling it by hand.
func TestPackSignature(t *testing.T) {
	key, _ := rail0.HexToPrivateKey(privateKeyHex)
	sig, err := rail0.SignSigningPayload(key, testPayload(rail0.PrimaryTypeTransfer))
	if err != nil {
		t.Fatal(err)
	}

	packed := rail0.PackSignature(sig)
	if len(packed) != 132 {
		t.Errorf("packed signature is %d chars, want 132 (0x + r + s + v)", len(packed))
	}
	if !strings.HasPrefix(packed, "0x") {
		t.Errorf("packed signature must be 0x-prefixed: %q", packed)
	}
	// v is one byte, zero-padded — a bare %x would emit "1b" fine but drop a leading
	// zero for any other value.
	if tail := packed[130:]; tail != "1b" && tail != "1c" {
		t.Errorf("packed signature ends in v=%q, want 1b or 1c", tail)
	}
	if packed[2:66] != strings.TrimPrefix(sig.R, "0x") || packed[66:130] != strings.TrimPrefix(sig.S, "0x") {
		t.Error("packed signature does not carry r then s")
	}
}
