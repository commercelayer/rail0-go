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
	domainTypehash := testKeccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	transferTypehash := testKeccak256([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))

	nameHash := testKeccak256([]byte(domain.Name))
	versionHash := testKeccak256([]byte(domain.Version))
	chainID := new(big.Int).SetUint64(domain.ChainID)

	ds := testKeccak256(concatBytes(domainTypehash, nameHash, versionHash, encUint(chainID), encAddr(domain.VerifyingContract)))
	sh := testKeccak256(concatBytes(transferTypehash, encAddr(from), encAddr(to), encUint(value), encUint(validAfter), encUint(validBefore), encB32(nonce)))
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
		Amount:          big.NewInt(50_000_000),
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
		Value:       big.NewInt(50_000_000),
		ValidAfter:  new(big.Int),
		ValidBefore: big.NewInt(testPayment.AuthorizationExpiry),
		Nonce:       testNonce,
	})
	auto, _ := rail0.SignAuthorize(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Amount:          big.NewInt(50_000_000),
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})

	if manual != auto {
		t.Errorf("SignAuthorize should match SignTransferWithAuthorization with explicit params\nmanual=%+v\nauto=%+v", manual, auto)
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
		Amount:          big.NewInt(25_000_000),
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
		Amount:          big.NewInt(50_000_000),
		Nonce:           testNonce,
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})
	chargeSig, _ := rail0.SignCharge(rail0.SignPaymentParams{
		PrivateKey:      key,
		Payment:         testPayment,
		Amount:          big.NewInt(50_000_000),
		Nonce:           "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		ContractAddress: contractAddress,
		TokenDomain:     testDomain,
	})

	if authSig.R == chargeSig.R {
		t.Error("SignCharge with a different nonce must produce a different signature than SignAuthorize")
	}
}
