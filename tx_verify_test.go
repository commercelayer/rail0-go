package rail0

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
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
	sparse := TxExpectation{Payment: &Payment{Rail0Contract: Address(verifyContract)}, Operation: "void"}
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

// S1-parity: `to` is never optional. An empty one is a contract creation, and a
// payment with no rail0_contract leaves nothing to compare it with — both refuse,
// whichever signer runs next (SignTransaction refuses an empty `to` on its own, a KMS
// or hardware wallet may not).
func TestVerifyRequiresToAndTheRail0Contract(t *testing.T) {
	for _, to := range []any{"", nil, "0x", "0x2222", "not-an-address", "2222222222222222222222222222222222222222", "0x" + strings.Repeat("zz", 20)} {
		overrides := map[string]any{"to": to}
		err := VerifyUnsignedTransaction(unsignedFor(t, "void", overrides), verifySigner, expectation("void", ""))
		if err == nil || !strings.Contains(err.Error(), "no valid `to` address") {
			t.Errorf("to=%v: want a refusal naming `to`, got %v", to, err)
		}
	}
	absent := unsignedFor(t, "void", nil)
	var m map[string]any
	_ = json.Unmarshal([]byte(absent), &m)
	delete(m, "to")
	b, _ := json.Marshal(m)
	if err := VerifyUnsignedTransaction(string(b), verifySigner, expectation("void", "")); err == nil ||
		!strings.Contains(err.Error(), "no valid `to` address") {
		t.Errorf("an absent `to` must be refused, got %v", err)
	}

	for _, contract := range []string{"", "0x1234"} {
		want := expectation("void", "")
		want.Payment.Rail0Contract = Address(contract)
		if err := VerifyUnsignedTransaction(unsignedFor(t, "void", nil), verifySigner, want); err == nil ||
			!strings.Contains(err.Error(), "rail0_contract") {
			t.Errorf("rail0_contract=%q: want a refusal naming rail0_contract, got %v", contract, err)
		}
	}
}

// ── VerifySigningPayload ─────────────────────────────────────────────────────
//
// Ported from the CLI's cmd/tx_verify_test.go: the same payload, signer, token and
// hostile cases. Unlike the CLI's internal check, an unknown expectation REFUSES
// rather than skips (the CLI's callers always supply every field anyway).

const (
	payloadToken    = "0x3333333333333333333333333333333333333333"
	payloadAttacker = "0x4444444444444444444444444444444444444444"
)

// gatewayTypes is `types` as the gateway emits it (Rail0::Eip3009#typed_data) and as
// encoding/json decodes it into SigningPayload.Types.
func gatewayTypes(primaryType string) map[string]any {
	field := func(name, typ string) any { return map[string]any{"name": name, "type": typ} }
	return map[string]any{
		"EIP712Domain": []any{
			field("name", "string"), field("version", "string"),
			field("chainId", "uint256"), field("verifyingContract", "address"),
		},
		primaryType: []any{
			field("from", "address"), field("to", "address"), field("value", "uint256"),
			field("validAfter", "uint256"), field("validBefore", "uint256"), field("nonce", "bytes32"),
		},
	}
}

func signingPayloadFor(overrides func(*SigningPayload)) *SigningPayload {
	sp := &SigningPayload{
		PrimaryType: PrimaryTypeReceive,
		Types:       gatewayTypes(PrimaryTypeReceive),
		Domain: EIP712Domain{
			Name:              "USD Coin",
			Version:           "2",
			ChainId:           verifyChainID,
			VerifyingContract: Address(payloadToken),
		},
		Message: EIP3009Message{
			From:        Address(verifySigner),
			To:          Address(verifyContract),
			Value:       "1000000",
			ValidAfter:  "0",
			ValidBefore: "2000000000",
			Nonce:       Bytes32("0x" + strings.Repeat("ab", 32)),
		},
	}
	if overrides != nil {
		overrides(sp)
	}
	return sp
}

func payloadWant() PayloadExpectation {
	return PayloadExpectation{
		Signer:  Address(verifySigner),
		ChainID: verifyChainID,
		Token:   Address(payloadToken),
		To:      Address(verifyContract),
		Value:   "1000000",
	}
}

func TestVerifySigningPayloadAcceptsAWellFormedPayload(t *testing.T) {
	for _, pt := range []string{PrimaryTypeTransfer, PrimaryTypeReceive} {
		sp := signingPayloadFor(func(sp *SigningPayload) { sp.PrimaryType = pt; sp.Types = gatewayTypes(pt) })
		if err := VerifySigningPayload(sp, payloadWant()); err != nil {
			t.Fatalf("%s: rejected a well-formed payload: %v", pt, err)
		}
	}
	// Addresses compare case-insensitively — the gateway may return checksummed ones.
	upper := signingPayloadFor(func(sp *SigningPayload) {
		sp.Message.From = Address(strings.ToUpper(verifySigner))
		sp.Message.To = Address(strings.ToUpper(verifyContract))
		sp.Domain.VerifyingContract = Address(strings.ToUpper(payloadToken))
	})
	if err := VerifySigningPayload(upper, payloadWant()); err != nil {
		t.Errorf("rejected checksum-cased addresses: %v", err)
	}
}

func TestVerifySigningPayloadRejectsHostilePayloads(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*SigningPayload)
		wantErr string
	}{
		// THE case this exists for: a drain of the signer's whole balance, which the
		// token would honour on its own.
		{"value inflated far past the amount asked for", func(sp *SigningPayload) { sp.Message.Value = "999999999999" }, "999999999999 base units (message.value)"},
		{"value one unit off", func(sp *SigningPayload) { sp.Message.Value = "1000001" }, "message.value"},
		{"value beyond uint256", func(sp *SigningPayload) { sp.Message.Value = new(big.Int).Lsh(big.NewInt(1), 256).String() }, "uint256"},
		{"value negative", func(sp *SigningPayload) { sp.Message.Value = "-1000000" }, "negative"},
		{"value not a number", func(sp *SigningPayload) { sp.Message.Value = "0xf4240" }, "invalid value"},
		{"from is not the signing key", func(sp *SigningPayload) { sp.Message.From = Address(payloadAttacker) }, "(message.from) but the signing key is"},
		{"domain names another token", func(sp *SigningPayload) { sp.Domain.VerifyingContract = Address(payloadAttacker) }, "token " + payloadAttacker + " (domain.verifyingContract)"},
		{"domain names another chain", func(sp *SigningPayload) { sp.Domain.ChainId = verifyChainID + 1 }, "(domain.chainId) but this payment is on chain"},
		{"pays another address", func(sp *SigningPayload) { sp.Message.To = Address(payloadAttacker) }, "pays " + payloadAttacker + " (message.to)"},
		{"pays no one", func(sp *SigningPayload) { sp.Message.To = "" }, "(message.to)"},
		{"unknown primaryType", func(sp *SigningPayload) { sp.PrimaryType = "SomeFutureAuthorization" }, "primaryType \"SomeFutureAuthorization\""},
		{"validBefore beyond uint256", func(sp *SigningPayload) { sp.Message.ValidBefore = new(big.Int).Lsh(big.NewInt(1), 300).String() }, "validBefore"},
		{"validAfter malformed", func(sp *SigningPayload) { sp.Message.ValidAfter = "" }, "validAfter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySigningPayload(signingPayloadFor(tc.mutate), payloadWant())
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.HasPrefix(err.Error(), "rail0: refusing to sign: ") {
				t.Errorf("error should say it is refusing to sign, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error should mention %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// The payload exactly as the gateway serialises it — produced by running gateway main's
// Rail0::Eip3009#typed_data — decodes and passes, and signs.
func TestVerifySigningPayloadAcceptsTheGatewaysPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/gateway_signing_payload.json")
	if err != nil {
		t.Fatal(err)
	}
	var sp SigningPayload
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatal(err)
	}
	if err := VerifySigningPayload(&sp, payloadWant()); err != nil {
		t.Fatalf("rejected the gateway's own payload: %v", err)
	}
	key, _ := HexToPrivateKey(verifyKeyHex)
	if _, err := SignSigningPayloadFor(&sp, key, payloadWant()); err != nil {
		t.Fatalf("the checked signer refused the gateway's own payload: %v", err)
	}
	// No EIP712Domain is fine: a wallet derives it from the domain.
	delete(sp.Types.(map[string]any), "EIP712Domain")
	if err := VerifySigningPayload(&sp, payloadWant()); err != nil {
		t.Errorf("rejected a payload without EIP712Domain: %v", err)
	}
	// A caller-built Types of another Go shape is judged by its JSON, like a wallet would.
	typed := signingPayloadFor(func(sp *SigningPayload) {
		sp.Types = map[string][]map[string]string{
			PrimaryTypeReceive: {
				{"name": "from", "type": "address"}, {"name": "to", "type": "address"},
				{"name": "value", "type": "uint256"}, {"name": "validAfter", "type": "uint256"},
				{"name": "validBefore", "type": "uint256"}, {"name": "nonce", "type": "bytes32"},
			},
		}
	})
	if err := VerifySigningPayload(typed, payloadWant()); err != nil {
		t.Errorf("rejected a typed Types value: %v", err)
	}
}

// The typed-data envelope. SignSigningPayloadFor hashes with fixed typehashes, but a
// browser wallet hashes whatever `types` says: each tampering is refused before either.
func TestVerifySigningPayloadRejectsTamperedTypedData(t *testing.T) {
	fields := func(sp *SigningPayload, name string) []any { return sp.Types.(map[string]any)[name].([]any) }
	setFields := func(sp *SigningPayload, name string, f []any) { sp.Types.(map[string]any)[name] = f }
	cases := []struct {
		name    string
		mutate  func(*SigningPayload)
		wantErr string
	}{
		{"unknown primaryType", func(sp *SigningPayload) {
			sp.Types = gatewayTypes("Permit")
			sp.PrimaryType = "Permit"
		}, `primaryType "Permit" is not an EIP-3009 authorization type`},
		{"primaryType with no definition", func(sp *SigningPayload) { sp.PrimaryType = PrimaryTypeTransfer },
			`types define "ReceiveWithAuthorization", which an EIP-3009 authorization does not use`},
		{"reordered fields", func(sp *SigningPayload) {
			f := fields(sp, PrimaryTypeReceive)
			f[0], f[1] = f[1], f[0]
		}, "types.ReceiveWithAuthorization is (address to,address from,"},
		{"renamed field", func(sp *SigningPayload) {
			fields(sp, PrimaryTypeReceive)[1] = map[string]any{"name": "recipient", "type": "address"}
		}, "address recipient"},
		{"changed field type", func(sp *SigningPayload) {
			fields(sp, PrimaryTypeReceive)[2] = map[string]any{"name": "value", "type": "uint128"}
		}, "uint128 value"},
		{"extra field", func(sp *SigningPayload) {
			setFields(sp, PrimaryTypeReceive, append(fields(sp, PrimaryTypeReceive), map[string]any{"name": "spender", "type": "address"}))
		}, "address spender) but must be exactly (address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"},
		{"missing field", func(sp *SigningPayload) {
			setFields(sp, PrimaryTypeReceive, fields(sp, PrimaryTypeReceive)[:5])
		}, "types.ReceiveWithAuthorization is ("},
		{"extra key on a field", func(sp *SigningPayload) {
			fields(sp, PrimaryTypeReceive)[0] = map[string]any{"name": "from", "type": "address", "note": "x"}
		}, "types.ReceiveWithAuthorization is ("},
		{"extra type", func(sp *SigningPayload) {
			sp.Types.(map[string]any)["Transfer"] = []any{map[string]any{"name": "to", "type": "address"}}
		}, `types define "Transfer"`},
		{"wrong EIP712Domain", func(sp *SigningPayload) {
			setFields(sp, "EIP712Domain", append(fields(sp, "EIP712Domain"), map[string]any{"name": "salt", "type": "bytes32"}))
		}, "types.EIP712Domain is (string name,string version,uint256 chainId,address verifyingContract,bytes32 salt) but must be exactly (string name,string version,uint256 chainId,address verifyingContract)"},
		{"EIP712Domain without version", func(sp *SigningPayload) {
			f := fields(sp, "EIP712Domain")
			setFields(sp, "EIP712Domain", []any{f[0], f[2], f[3]})
		}, "types.EIP712Domain is (string name,uint256 chainId,address verifyingContract)"},
		{"no types", func(sp *SigningPayload) { sp.Types = nil }, "the signing payload has no types"},
		{"types not an object", func(sp *SigningPayload) { sp.Types = []any{} }, "the signing payload has no types"},
		{"empty types", func(sp *SigningPayload) { sp.Types = map[string]any{} }, "types.ReceiveWithAuthorization is (null)"},
		{"fields not a list", func(sp *SigningPayload) { sp.Types.(map[string]any)[PrimaryTypeReceive] = "address from" }, `types.ReceiveWithAuthorization is ("address from")`},
	}
	key, _ := HexToPrivateKey(verifyKeyHex)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySigningPayload(signingPayloadFor(tc.mutate), payloadWant())
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.HasPrefix(err.Error(), "rail0: refusing to sign: ") || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("want a refusal mentioning %q, got: %v", tc.wantErr, err)
			}
			if sig, err := SignSigningPayloadFor(signingPayloadFor(tc.mutate), key, payloadWant()); err == nil || sig != (Eip3009Signature{}) {
				t.Errorf("the checked signer must produce nothing, got %v / %+v", err, sig)
			}
		})
	}
}

// Fail closed: an expectation that cannot back a comparison refuses — never skips.
func TestVerifySigningPayloadFailsClosed(t *testing.T) {
	if err := VerifySigningPayload(nil, payloadWant()); err == nil || !strings.Contains(err.Error(), "no signing payload") {
		t.Errorf("a nil payload must be refused, got %v", err)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*PayloadExpectation)
		wantErr string
	}{
		{"no signer", func(w *PayloadExpectation) { w.Signer = "" }, "expected Signer"},
		{"no token", func(w *PayloadExpectation) { w.Token = "" }, "expected Token"},
		{"no to", func(w *PayloadExpectation) { w.To = "" }, "expected To"},
		{"malformed to", func(w *PayloadExpectation) { w.To = "0x2222" }, "expected To"},
		{"no chain", func(w *PayloadExpectation) { w.ChainID = 0 }, "chain id"},
		{"no value", func(w *PayloadExpectation) { w.Value = "" }, "without the amount you asked for"},
		{"human value", func(w *PayloadExpectation) { w.Value = "1.00" }, "not a base-unit"},
	} {
		want := payloadWant()
		tc.mutate(&want)
		err := VerifySigningPayload(signingPayloadFor(nil), want)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.HasPrefix(err.Error(), "rail0: refusing to sign: ") {
			t.Errorf("%s: want a refusal mentioning %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

// PayloadExpectationFor takes chain, token and contract off the payment, and the value
// either from the payment's stored amount or from a human amount scaled by Decimals.
func TestPayloadExpectationFor(t *testing.T) {
	p := verifyPayment()
	p.Token = Address(payloadToken)
	p.Amount = "1000000"

	stored, err := PayloadExpectationFor(p, "")
	if err != nil {
		t.Fatal(err)
	}
	stored.Signer = Address(verifySigner)
	if stored != payloadWant() {
		t.Errorf("PayloadExpectationFor(p, \"\") = %+v, want %+v", stored, payloadWant())
	}
	if err := VerifySigningPayload(signingPayloadFor(nil), stored); err != nil {
		t.Errorf("the stored-amount expectation must accept the payment's own payload: %v", err)
	}

	refund, err := PayloadExpectationFor(p, "0.25")
	if err != nil || refund.Value != "250000" {
		t.Errorf("PayloadExpectationFor(p, \"0.25\") = %+v, %v; want Value 250000", refund, err)
	}

	if _, err := PayloadExpectationFor(nil, ""); err == nil {
		t.Error("a nil payment must be refused")
	}
	if _, err := PayloadExpectationFor(p, "1.0000001"); err == nil || !strings.Contains(err.Error(), `amount "1.0000001"`) {
		t.Errorf("an amount finer than the token allows must be refused, got %v", err)
	}
	noDecimals := *p
	noDecimals.Decimals = nil
	if _, err := PayloadExpectationFor(&noDecimals, "1"); err == nil || !strings.Contains(err.Error(), "decimals are unknown") {
		t.Errorf("a human amount with unknown decimals must be refused, got %v", err)
	}
	// A sparse payment yields a sparse expectation, which the verifier then refuses.
	sparse, err := PayloadExpectationFor(&Payment{}, "")
	if err != nil {
		t.Fatal(err)
	}
	sparse.Signer = Address(verifySigner)
	if err := VerifySigningPayload(signingPayloadFor(nil), sparse); err == nil {
		t.Error("an expectation built from a sparse payment must not pass")
	}
}

func TestIsHexAddress(t *testing.T) {
	for _, ok := range []string{verifyContract, strings.ToUpper(verifyContract[:2]) + verifyContract[2:], "0x" + strings.Repeat("aB", 20)} {
		if !isHexAddress(ok) {
			t.Errorf("isHexAddress(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "0x", "0x2222", strings.TrimPrefix(verifyContract, "0x"), "0x" + strings.Repeat("g", 40), verifyContract + "00"} {
		if isHexAddress(bad) {
			t.Errorf("isHexAddress(%q) = true, want false", bad)
		}
	}
}
