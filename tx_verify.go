package rail0

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// ================================================================
//  Pre-sign verification of a gateway-built unsigned transaction
// ================================================================
//
// SignTransaction signs whatever `unsigned_transaction` a prepare step returned. A
// hostile or merely misconfigured gateway endpoint could therefore have the
// merchant's own key sign an arbitrary transaction — an approve(spender, MAX) on the
// token, a capture answered to a void prepare, a capture for a larger amount than
// asked. The checks here run on data the caller already holds (the payment record,
// the operation it asked for, the amount it typed) BEFORE the key touches the
// transaction, so they add no gateway round-trip. A mismatch is an error whose text
// starts with "rail0: refusing to sign:" and names the field; no signature is
// produced.
//
// Ported from the rail0 CLI's cmd/tx_verify.go (#63), which is the reference for the
// check order and the fail-closed rules.

// TxExpectation is what the caller independently knows about the transaction it asked
// the gateway to build: the payment record it already has (from Create or Get), the
// operation it called the prepare for (the gateway's wire name: "authorize",
// "charge", "capture", "void", "release", "refund", "dispute", "close_dispute"), and —
// for capture and refund only — the human-decimal Amount it passed to the prepare,
// which is scaled by Payment.Decimals before comparison.
type TxExpectation struct {
	Payment   *Payment
	Operation string
	Amount    string
}

// rail0PaymentTuple is RAIL0.sol's `Payment` struct as the ABI encodes it: payer,
// payee, token, amount (uint120), authorizationExpiry and refundExpiry (uint48). All
// static types, so the tuple is encoded INLINE — six 32-byte words right after the
// paymentId — which is what fixes the position of the amount argument below.
const rail0PaymentTuple = "(address,address,address,uint120,uint48,uint48)"

// Rail0FunctionSignatures are the eight RAIL0 entry points, keyed by operation.
// Copied from RAIL0.sol; TestRail0SelectorsMatchTheABI pins the resulting selectors
// against the contract's compiled methodIdentifiers, so a typo here cannot agree
// with itself.
var Rail0FunctionSignatures = map[string]string{
	"authorize":     "authorize(bytes32," + rail0PaymentTuple + ",uint8,bytes32,bytes32)",
	"charge":        "charge(bytes32," + rail0PaymentTuple + ",uint8,bytes32,bytes32)",
	"capture":       "capture(bytes32," + rail0PaymentTuple + ",uint256)",
	"void":          "void(bytes32," + rail0PaymentTuple + ")",
	"release":       "release(bytes32," + rail0PaymentTuple + ")",
	"refund":        "refund(bytes32," + rail0PaymentTuple + ",uint256,uint8,bytes32,bytes32)",
	"dispute":       "dispute(bytes32," + rail0PaymentTuple + ",bytes32)",
	"close_dispute": "closeDispute(bytes32," + rail0PaymentTuple + ",bytes32)",
}

// Rail0Selectors maps operation → the function's 4-byte selector as 8 lowercase hex
// chars: the first four bytes of keccak256(signature), computed from the table above
// rather than hand-copied.
var Rail0Selectors = func() map[string]string {
	out := make(map[string]string, len(Rail0FunctionSignatures))
	for op, sig := range Rail0FunctionSignatures {
		out[op] = hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
	}
	return out
}()

// rail0AmountWord is the calldata position (in 32-byte words after the selector) of
// the `uint256 amount` argument of capture and refund: word 0 is the paymentId, words
// 1-6 the inline Payment tuple, so the amount is word 7. Only these two operations
// take an amount.
var rail0AmountWord = map[string]int{"capture": 7, "refund": 7}

const (
	selectorHexLen = 8  // 4-byte selector
	wordHexLen     = 64 // 32-byte ABI word
)

// CanonicalOperation folds the vocabularies an operation arrives in onto the gateway's
// wire name: "closeDispute", "close dispute" and "close_dispute" are one operation.
func CanonicalOperation(op string) string {
	s := strings.TrimSpace(op)
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && s[i-1] != ' ' && s[i-1] != '_' && s[i-1] != '-' {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		if r == ' ' || r == '-' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("rail0: refusing to sign: "+format, args...)
}

// unsignedTxHeader is the subset of the gateway's `unsigned_transaction` JSON the
// checks read. Value stays RAW: a native quantity can legitimately arrive as a number
// or a hex string, and a hostile one can exceed any Go integer type — decoding it would
// refuse the signature (safe) but report "not valid JSON" instead of naming the value.
type unsignedTxHeader struct {
	ChainID json.Number     `json:"chain_id"`
	From    string          `json:"from"`
	To      string          `json:"to"`
	Value   json.RawMessage `json:"value"`
	Data    string          `json:"data"`
}

// isZeroNativeValue accepts the encodings a zero native value legitimately arrives in
// — absent, null, the number 0, or the string "0"/"0x0" — and treats everything else
// as non-zero. A whitelist, deliberately: an unrecognised encoding must refuse the
// signature rather than be guessed at.
func isZeroNativeValue(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimSpace(strings.Trim(s, `"`))
	return s == "" || s == "null" || s == "0" || strings.EqualFold(s, "0x0")
}

// SignerAddress is the 0x-prefixed, lowercase address a private key signs as — what
// the transaction's `from` must name.
func SignerAddress(privateKey []byte) (string, error) {
	priv, err := crypto.ToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("rail0: invalid private key: %w", err)
	}
	return strings.ToLower(crypto.PubkeyToAddress(priv.PublicKey).Hex()), nil
}

// VerifyUnsignedTransaction checks a gateway-built `unsigned_transaction` against what
// the caller asked for, BEFORE any key touches it. signer is the address of the key
// about to sign (SignerAddress), or "" to skip that comparison. It returns an error
// naming the first field that disagrees, nil otherwise.
//
// In order:
//
//  1. the JSON parses;
//  2. `value` is zero — every RAIL0 operation is value-0, funds move as token
//     transfers the contract performs, so a non-zero value is the gateway asking for ETH;
//  3. `chain_id` is the payment's chain;
//  4. `from`, when present, is the signing key's address;
//  5. `to` is the payment's rail0_contract — a consistency check (the address comes
//     from the same gateway), but it catches a response stitched from two payments;
//  6. the 4-byte selector is the RAIL0 function for the operation, and for capture
//     and refund the amount word equals Amount scaled by the payment's Decimals;
//  7. the paymentId word is the payment's rail0_id.
//
// Every right-hand side is something the caller supplied or derived locally — never
// the prepare response itself. FAIL-CLOSED on what cannot be compared: an unknown
// operation, a capture/refund without an Amount, or a payment with unknown Decimals
// refuses rather than skips. Only the paymentId comparison is skipped, when the
// payment carries no usable rail0_id.
func VerifyUnsignedTransaction(unsignedJSON string, signer string, want TxExpectation) error {
	var tx unsignedTxHeader
	if err := json.Unmarshal([]byte(unsignedJSON), &tx); err != nil {
		return refuse("the gateway's unsigned transaction is not valid JSON: %w", err)
	}
	p := want.Payment
	if p == nil {
		return refuse("cannot check the transaction without the payment it is for")
	}

	if !isZeroNativeValue(tx.Value) {
		return refuse("transaction attaches native currency (value=%s); every RAIL0 operation is value-0", tx.Value)
	}

	if p.ChainID != 0 && tx.ChainID.String() != fmt.Sprint(p.ChainID) {
		return refuse("transaction targets chain %s but this payment is on chain %d", tx.ChainID, p.ChainID)
	}

	if signer != "" && tx.From != "" && !strings.EqualFold(tx.From, signer) {
		return refuse("transaction is prepared from %s but the signing key is %s", tx.From, signer)
	}

	if p.Rail0Contract != "" && tx.To != "" && !strings.EqualFold(tx.To, string(p.Rail0Contract)) {
		return refuse("transaction is addressed to %s but this payment's RAIL0 contract is %s", tx.To, p.Rail0Contract)
	}

	// The operation before the payment id, so a swapped operation is named as such.
	wantUnits, err := expectedAmountUnits(want)
	if err != nil {
		return err
	}
	if err := checkCalldataCall(tx.Data, want.Operation, wantUnits); err != nil {
		return err
	}
	return checkCalldataPaymentID(tx.Data, string(p.Rail0Id))
}

// expectedAmountUnits scales the human Amount the CALLER typed by the payment's token
// decimals, for comparison against the calldata. "" when the operation takes no
// amount. For one that does, a missing Amount or unknown Decimals is a refusal — the
// whole guard exists for that comparison, so it must not be skipped.
func expectedAmountUnits(want TxExpectation) (string, error) {
	key := CanonicalOperation(want.Operation)
	if _, takesAmount := rail0AmountWord[key]; !takesAmount {
		return "", nil
	}
	if want.Amount == "" {
		return "", refuse("cannot check the %s amount without the amount you asked for", key)
	}
	if want.Payment == nil || want.Payment.Decimals == nil {
		return "", refuse("the payment's token decimals are unknown, so the %s amount cannot be checked (fetch the payment with Get first)", key)
	}
	units, err := ToBaseUnits(want.Amount, *want.Payment.Decimals)
	if err != nil {
		return "", refuse("amount %q: %w", want.Amount, err)
	}
	return units, nil
}

// checkCalldataCall asserts the call is the RAIL0 function for op and, for capture and
// refund, that it moves exactly wantUnits base units.
//
// Checking the paymentId alone binds the transaction to the right PAYMENT but not to
// the right OPERATION: every RAIL0 function takes the paymentId first, so a hostile
// endpoint could answer a void prepare with capture calldata (settling the escrow to
// the merchant instead of returning it). The selector closes that; the amount word
// closes the remaining capture/refund swap — the right function for a different
// quantity than the caller typed. Fail-closed: no known selector, or an amount-taking
// operation with nothing local to compare, refuses rather than skips.
func checkCalldataCall(data, op, wantUnits string) error {
	key := CanonicalOperation(op)
	want, ok := Rail0Selectors[key]
	if !ok {
		return refuse("no known RAIL0 function for operation %q", op)
	}
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(data)), "0x")
	if len(raw) < selectorHexLen {
		return refuse("calldata is %d hex chars, too short to carry a function selector", len(raw))
	}
	if got := raw[:selectorHexLen]; got != want {
		return refuse("the call is %s but you asked to %s (%s)", describeSelector(got), key, Rail0FunctionSignatures[key])
	}

	word, takesAmount := rail0AmountWord[key]
	if !takesAmount {
		return nil
	}
	if wantUnits == "" {
		return refuse("cannot check the %s amount without the amount you asked for", key)
	}
	wantInt, ok := new(big.Int).SetString(wantUnits, 10)
	if !ok {
		return refuse("expected amount %q is not a base-unit integer", wantUnits)
	}
	start := selectorHexLen + word*wordHexLen
	if len(raw) < start+wordHexLen {
		return refuse("calldata is %d hex chars, too short to carry the %s amount", len(raw), key)
	}
	gotInt, ok := new(big.Int).SetString(raw[start:start+wordHexLen], 16)
	if !ok {
		return refuse("the %s amount in the calldata is not a number", key)
	}
	if gotInt.Cmp(wantInt) != 0 {
		return refuse("the call would %s %s base units but you asked for %s", key, gotInt, wantInt)
	}
	return nil
}

// describeSelector names a selector for an error message: the RAIL0 function it
// belongs to when it is one of ours, and the bare hex otherwise.
func describeSelector(sel string) string {
	for op, s := range Rail0Selectors {
		if s == sel {
			sig := Rail0FunctionSignatures[op]
			return sig[:strings.IndexByte(sig, '(')] + "()"
		}
	}
	return "selector 0x" + sel
}

// checkCalldataPaymentID asserts the call's first argument is THIS payment's id.
//
// All eight RAIL0 operations take `bytes32 paymentId` first, so under ABI encoding
// the id always occupies bytes 4..36 of the calldata. A hostile gateway's most
// profitable swap is approve(spender, MAX) on the token contract, which this rejects:
// approve's first argument is a left-padded address, so it cannot be a rail0_id.
// Skipped only when there is no usable rail0_id to compare against.
func checkCalldataPaymentID(data, rail0ID string) error {
	want := strings.TrimPrefix(strings.ToLower(rail0ID), "0x")
	if len(want) != wordHexLen {
		return nil
	}
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(data)), "0x")
	if len(raw) < selectorHexLen+wordHexLen {
		return refuse("calldata is %d hex chars, too short to carry a paymentId", len(raw))
	}
	if got := raw[selectorHexLen : selectorHexLen+wordHexLen]; got != want {
		return refuse("the call acts on payment 0x%s but you asked for 0x%s", got, want)
	}
	return nil
}

// SignTransactionFor verifies the gateway's `unsigned_transaction` against what the
// caller asked for (VerifyUnsignedTransaction, with the key's own address as the
// expected sender) and only then signs it — the SignTransaction every RAIL0 flow
// should use. A transaction that fails the check leaves no signature behind.
//
//	payment, _ := client.Payments.Get(ctx, rail0ID)          // chain_id, rail0_contract, rail0_id, decimals
//	prep, _ := client.Payments.CapturePrepare(ctx, rail0ID, rail0.PrepareRequest{Amount: "50.00"})
//	raw, err := rail0.SignTransactionFor(prep.UnsignedTransaction, key, rail0.TxExpectation{
//	    Payment: payment, Operation: "capture", Amount: "50.00",
//	})
func SignTransactionFor(unsignedJSON string, privateKey []byte, want TxExpectation) (string, error) {
	signer, err := SignerAddress(privateKey)
	if err != nil {
		return "", err
	}
	if err := VerifyUnsignedTransaction(unsignedJSON, signer, want); err != nil {
		return "", err
	}
	return SignTransaction(unsignedJSON, privateKey)
}
