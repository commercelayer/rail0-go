package rail0

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// SignTransaction used to sign whatever the gateway returned. These pin the checks that
// now run before the key touches the transaction — ported from the CLI's
// cmd/tx_verify_test.go, which is the reference for what must pass and what must refuse.

const (
	verifyKeyHex   = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	verifySigner   = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	verifyRail0ID  = "0xa1b2c3d40000000000000000000000000000000000000000000000000000beef"
	verifyContract = "0x2222222222222222222222222222222222222222"
	verifyChainID  = 84532
)

func verifyPayment() *Payment {
	decimals := 6
	return &Payment{
		ChainID:       verifyChainID,
		Rail0Id:       Bytes32(verifyRail0ID),
		Rail0Contract: Address(verifyContract),
		Decimals:      &decimals,
	}
}

func expectation(op, amount string) TxExpectation {
	return TxExpectation{Payment: verifyPayment(), Operation: op, Amount: amount}
}

// calldataFor builds RAIL0 calldata the way the ABI lays it out: selector, paymentId,
// the six inline Payment words (zeros — their content is not checked), then any
// trailing arguments. amountUnits, when set, is capture/refund's uint256 amount.
func calldataFor(t *testing.T, op, rail0ID, amountUnits string) string {
	t.Helper()
	sel, ok := Rail0Selectors[CanonicalOperation(op)]
	if !ok {
		t.Fatalf("no selector for %q", op)
	}
	var b strings.Builder
	b.WriteString("0x" + sel + strings.TrimPrefix(strings.ToLower(rail0ID), "0x"))
	b.WriteString(strings.Repeat("0", 64*6))
	if amountUnits != "" {
		n, ok := new(big.Int).SetString(amountUnits, 10)
		if !ok {
			t.Fatalf("bad amount %q", amountUnits)
		}
		b.WriteString(fmt.Sprintf("%064x", n))
	}
	if CanonicalOperation(op) == "refund" {
		b.WriteString(strings.Repeat("0", 64*3)) // v, r, s
	}
	return b.String()
}

// unsignedFor builds the gateway's unsigned_transaction shape for op, with overrides.
func unsignedFor(t *testing.T, op string, overrides map[string]any) string {
	t.Helper()
	fields := map[string]any{
		"chain_id":                 verifyChainID,
		"from":                     verifySigner,
		"to":                       verifyContract,
		"value":                    0,
		"data":                     calldataFor(t, op, verifyRail0ID, ""),
		"nonce":                    7,
		"gas_limit":                200000,
		"max_priority_fee_per_gas": 1000000,
		"max_fee_per_gas":          2000000000,
	}
	for k, v := range overrides {
		fields[k] = v
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The selectors are pinned against RAIL0.sol's compiled methodIdentifiers
// (rail0/contracts/out/RAIL0.sol/RAIL0.json), not against the table they are computed
// from — a typo in a signature string would otherwise agree with itself.
func TestRail0SelectorsMatchTheABI(t *testing.T) {
	abi := map[string]string{
		"authorize":     "c36c951c",
		"charge":        "1eddc5e5",
		"capture":       "11bf1806",
		"void":          "69438554",
		"release":       "955b0ee2",
		"refund":        "e8ed339d",
		"dispute":       "eebaa720",
		"close_dispute": "6e6923ab",
	}
	if len(Rail0Selectors) != len(abi) {
		t.Errorf("%d selectors, want %d", len(Rail0Selectors), len(abi))
	}
	for op, want := range abi {
		if got := Rail0Selectors[op]; got != want {
			t.Errorf("%s selector = %s, want %s (from the contract ABI)", op, got, want)
		}
	}
}

func TestCanonicalOperation(t *testing.T) {
	for _, in := range []string{"close_dispute", "closeDispute", "close dispute", " Close-Dispute "} {
		if got := CanonicalOperation(in); got != "close_dispute" {
			t.Errorf("CanonicalOperation(%q) = %q, want close_dispute", in, got)
		}
	}
	if got := CanonicalOperation("capture"); got != "capture" {
		t.Errorf("CanonicalOperation(capture) = %q", got)
	}
}

// The happy path must not be disturbed: the right calldata for each operation passes,
// the amount-taking ones only with the amount the caller asked for.
func TestVerifyAcceptsTheRightCallForEachOperation(t *testing.T) {
	for _, op := range []string{"authorize", "charge", "void", "release", "dispute", "close_dispute"} {
		if err := VerifyUnsignedTransaction(unsignedFor(t, op, nil), verifySigner, expectation(op, "")); err != nil {
			t.Errorf("%s: rejected its own calldata: %v", op, err)
		}
	}
	for _, op := range []string{"capture", "refund"} {
		tx := unsignedFor(t, op, map[string]any{"data": calldataFor(t, op, verifyRail0ID, "1500000")})
		if err := VerifyUnsignedTransaction(tx, verifySigner, expectation(op, "1.5")); err != nil {
			t.Errorf("%s: rejected its own calldata: %v", op, err)
		}
	}
	// Case-insensitive on addresses (the gateway may return checksummed), and the SDK
	// method's spelling of the operation is accepted too.
	upper := unsignedFor(t, "close_dispute", map[string]any{
		"from": strings.ToUpper(verifySigner), "to": strings.ToUpper(verifyContract),
	})
	if err := VerifyUnsignedTransaction(upper, verifySigner, expectation("closeDispute", "")); err != nil {
		t.Errorf("rejected checksum-cased addresses / camelCase operation: %v", err)
	}
}

func TestVerifyRejectsHostileTransactions(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]any
		wantErr   string
	}{
		{"attaches native currency", map[string]any{"value": 1000000000000000000}, "attaches native currency"},
		{"native value as a hex quantity", map[string]any{"value": "0xde0b6b3a7640000"}, "attaches native currency"},
		// Must refuse rather than overflow into a plausible small number.
		{"native value beyond int64", map[string]any{"value": json.Number("99999999999999999999999999")}, "attaches native currency"},
		{"wrong chain", map[string]any{"chain_id": 1}, "targets chain 1 but this payment is on chain 84532"},
		{"prepared for a different account", map[string]any{"from": "0x9999999999999999999999999999999999999999"}, "prepared from"},
		{"addressed to another contract", map[string]any{"to": "0x3333333333333333333333333333333333333333"}, "RAIL0 contract is 0x2222"},
		{"calldata acts on a different payment", map[string]any{"data": calldataFor(t, "void", "0x"+strings.Repeat("ab", 32), "")}, "acts on payment 0xabab"},
		// The profitable swap: approve(spender, MAX) on the token contract. Its selector
		// is not a RAIL0 one, and its first argument is a left-padded address.
		{"approve(spender, MAX) on the token", map[string]any{
			"data": "0x095ea7b3" + strings.Repeat("0", 24) + strings.Repeat("9", 40) + strings.Repeat("f", 64),
		}, "selector 0x095ea7b3"},
		{"calldata too short for a selector", map[string]any{"data": "0xdead"}, "too short"},
		{"no calldata at all", map[string]any{"data": ""}, "too short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyUnsignedTransaction(unsignedFor(t, "void", tc.overrides), verifySigner, expectation("void", ""))
			if err == nil {
				t.Fatalf("accepted a hostile transaction (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "rail0: refusing to sign") {
				t.Errorf("error = %q, want it to say it is refusing to sign", err)
			}
		})
	}
}

func TestVerifyRejectsUnparseableJSON(t *testing.T) {
	err := VerifyUnsignedTransaction("{not json", verifySigner, expectation("void", ""))
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("accepted an unparseable unsigned transaction: %v", err)
	}
}

// Zero-value encodings that must be accepted, and everything that must not.
func TestIsZeroNativeValue(t *testing.T) {
	for _, ok := range []string{"", "null", "0", `"0"`, "0x0", `"0x0"`, `"0X0"`, " 0 "} {
		if !isZeroNativeValue(json.RawMessage(ok)) {
			t.Errorf("isZeroNativeValue(%s) = false, want true", ok)
		}
	}
	for _, bad := range []string{"1", `"0x1"`, "00", "0.0", "-0", `"0xde0b6b3a7640000"`, "99999999999999999999999999"} {
		if isZeroNativeValue(json.RawMessage(bad)) {
			t.Errorf("isZeroNativeValue(%s) = true, want false (unrecognised must refuse)", bad)
		}
	}
}

// The swaps the payment-id check alone cannot see: the right payment, the wrong
// function. Each would have been signed before.
func TestVerifyRejectsASwappedOperation(t *testing.T) {
	cases := []struct{ asked, served, amount string }{
		{"capture", "void", ""},
		{"void", "capture", "1000000"},
		{"capture", "release", ""},
		{"release", "capture", "1000000"},
		{"dispute", "close_dispute", ""},
		{"close_dispute", "dispute", ""},
		{"refund", "capture", "1000000"},
	}
	for _, tc := range cases {
		tx := unsignedFor(t, tc.served, map[string]any{"data": calldataFor(t, tc.served, verifyRail0ID, tc.amount)})
		err := VerifyUnsignedTransaction(tx, verifySigner, expectation(tc.asked, "1"))
		if err == nil {
			t.Errorf("asked to %s, served %s: signed it", tc.asked, tc.served)
			continue
		}
		name := Rail0FunctionSignatures[tc.served]
		name = name[:strings.IndexByte(name, '(')]
		if !strings.Contains(err.Error(), "the call is "+name+"() but you asked to "+tc.asked) {
			t.Errorf("asked to %s, served %s: error %q should name %s()", tc.asked, tc.served, err, name)
		}
	}
}

// Capture and refund: the right function for a different quantity than the caller
// typed, named with both figures; and calldata truncated before the amount word.
func TestVerifyRejectsAnAmountOtherThanAskedFor(t *testing.T) {
	for _, op := range []string{"capture", "refund"} {
		tx := unsignedFor(t, op, map[string]any{"data": calldataFor(t, op, verifyRail0ID, "999000000")})
		err := VerifyUnsignedTransaction(tx, verifySigner, expectation(op, "1"))
		if err == nil || !strings.Contains(err.Error(), "999000000 base units but you asked for 1000000") {
			t.Errorf("%s: an inflated amount must be refused naming both, got %v", op, err)
		}
		short := calldataFor(t, op, verifyRail0ID, "")
		if op == "refund" {
			short = short[:len(short)-64*3]
		}
		err = VerifyUnsignedTransaction(unsignedFor(t, op, map[string]any{"data": short}), verifySigner, expectation(op, "1"))
		if err == nil || !strings.Contains(err.Error(), "too short to carry the "+op+" amount") {
			t.Errorf("%s: calldata with no amount word must be refused, got %v", op, err)
		}
	}
}

// Fail closed: everything that cannot be compared refuses rather than skips.
func TestVerifyFailsClosed(t *testing.T) {
	capture := unsignedFor(t, "capture", map[string]any{"data": calldataFor(t, "capture", verifyRail0ID, "1000000")})

	if err := VerifyUnsignedTransaction(capture, verifySigner, expectation("capture", "")); err == nil ||
		!strings.Contains(err.Error(), "cannot check the capture amount without the amount you asked for") {
		t.Errorf("capture with no expected amount must be refused, got %v", err)
	}
	noDecimals := expectation("capture", "1")
	noDecimals.Payment.Decimals = nil
	if err := VerifyUnsignedTransaction(capture, verifySigner, noDecimals); err == nil ||
		!strings.Contains(err.Error(), "decimals are unknown") {
		t.Errorf("capture with unknown decimals must be refused, got %v", err)
	}
	// An amount finer than the token allows is a refusal, not a silent truncation.
	if err := VerifyUnsignedTransaction(capture, verifySigner, expectation("capture", "1.0000001")); err == nil ||
		!strings.Contains(err.Error(), `amount "1.0000001"`) {
		t.Errorf("an amount finer than the token's decimals must be refused, got %v", err)
	}
	if err := VerifyUnsignedTransaction(unsignedFor(t, "void", nil), verifySigner, expectation("approve", "")); err == nil ||
		!strings.Contains(err.Error(), `no known RAIL0 function for operation "approve"`) {
		t.Errorf("an operation with no known selector must be refused, got %v", err)
	}
	if err := VerifyUnsignedTransaction(unsignedFor(t, "void", nil), verifySigner, TxExpectation{Operation: "void"}); err == nil ||
		!strings.Contains(err.Error(), "without the payment") {
		t.Errorf("a nil payment must be refused, got %v", err)
	}
}

// What the payment record cannot support is skipped — never the value check — and a
// missing rail0_id is nothing to assert rather than a false alarm.
func TestVerifySkipsOnlyWhatThePaymentCannotSupport(t *testing.T) {
	sparse := TxExpectation{Payment: &Payment{}, Operation: "void"}
	if err := VerifyUnsignedTransaction(unsignedFor(t, "void", nil), "", sparse); err != nil {
		t.Errorf("a sparse payment must not refuse a well-formed transaction: %v", err)
	}
	if err := VerifyUnsignedTransaction(unsignedFor(t, "void", map[string]any{"value": 1}), "", sparse); err == nil {
		t.Error("a sparse payment still must reject a non-zero value")
	}
	for _, id := range []string{"", "0x", "not-an-id", "0xabc"} {
		if err := checkCalldataPaymentID("0xdeadbeef", id); err != nil {
			t.Errorf("checkCalldataPaymentID(_, %q) = %v, want nil", id, err)
		}
	}
}

func TestSignTransactionForRefusesBeforeSigning(t *testing.T) {
	key, _ := HexToPrivateKey(verifyKeyHex)

	raw, err := SignTransactionFor(unsignedFor(t, "void", nil), key, expectation("void", ""))
	if err != nil {
		t.Fatalf("refused a well-formed transaction: %v", err)
	}
	blind, _ := SignTransaction(unsignedFor(t, "void", nil), key)
	if raw != blind {
		t.Error("a verified signature must be byte-identical to the blind one")
	}

	if _, err := SignTransactionFor(unsignedFor(t, "void", map[string]any{"value": 1}), key, expectation("void", "")); err == nil {
		t.Error("signed a transaction attaching native currency")
	}
	other := unsignedFor(t, "void", map[string]any{"from": "0x9999999999999999999999999999999999999999"})
	_, err = SignTransactionFor(other, key, expectation("void", ""))
	if err == nil || !strings.Contains(err.Error(), "the signing key is "+strings.ToLower(verifySigner)) {
		t.Errorf("a transaction prepared for another account must be refused naming the key, got %v", err)
	}

	if addr, err := SignerAddress(key); err != nil || addr != strings.ToLower(verifySigner) {
		t.Errorf("SignerAddress = %q, %v; want %s", addr, err, strings.ToLower(verifySigner))
	}
	if _, err := SignerAddress([]byte{1, 2, 3}); err == nil {
		t.Error("a malformed key must be an error")
	}
}
