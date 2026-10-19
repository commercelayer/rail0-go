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

// isHexAddress reports whether s is a 0x-prefixed, 20-byte hex address. Strict on
// purpose: the comparisons it guards are case-insensitive string equality, and a
// value that is not an address must never reach them as "" or as a lookalike.
func isHexAddress(s string) bool {
	if len(s) != 42 || (s[:2] != "0x" && s[:2] != "0X") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
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
//  5. `to` is a well-formed address equal to the payment's rail0_contract — required
//     on both sides, since an absent `to` is a contract creation. A consistency check
//     (the address comes from the same gateway), but it catches a response stitched
//     from two payments;
//  6. the 4-byte selector is the RAIL0 function for the operation, and for capture
//     and refund the amount word equals Amount scaled by the payment's Decimals;
//  7. the paymentId word is the payment's rail0_id.
//
// Every right-hand side is something the caller supplied or derived locally — never
// the prepare response itself. FAIL-CLOSED on what cannot be compared: an unknown
// operation, a capture/refund without an Amount, a payment with unknown Decimals, or
// a missing `to` / rail0_contract refuses rather than skips. Only the chain (payment
// with no chain_id), `from` (no signer given or no `from` in the transaction) and
// paymentId (no usable rail0_id) comparisons are skipped.
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

	// `to` is never optional: an empty one is a contract CREATION, and the signer would
	// deploy whatever the calldata holds. SignTransaction refuses it too, but the check
	// must not depend on which signer runs next (a KMS or hardware wallet may not).
	if !isHexAddress(tx.To) {
		return refuse("transaction has no valid `to` address (%q); it must be this payment's RAIL0 contract", tx.To)
	}
	if !isHexAddress(string(p.Rail0Contract)) {
		return refuse("the payment carries no valid rail0_contract (%q), so the transaction's `to` cannot be checked (fetch the payment with Get first)", p.Rail0Contract)
	}
	if !strings.EqualFold(tx.To, string(p.Rail0Contract)) {
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

// ================================================================
//  Pre-sign verification of a gateway-built EIP-3009 signing payload
// ================================================================
//
// The unsigned transaction is the LESS dangerous of the two artifacts the gateway asks
// a key to sign. A malicious transaction costs gas and goes through RAIL0; an EIP-3009
// authorization is a bearer instrument the TOKEN honours directly, with no RAIL0
// involvement, for up to message.value. An endpoint that answered a refund prepare
// with {from: <payee>, to: <attacker>, value: <the payee's whole balance>} would get
// that signed verbatim by SignSigningPayload and could replay it against the token.
//
// Ported from the rail0 CLI's verifySigningPayload (cmd/tx_verify.go), which is the
// reference for the check order and the wording. Asserting here does NOT break the
// sign-the-payload-verbatim rule (#27): the typehash still comes from the payload's
// own primaryType and the digest from its own message; this only refuses a message
// whose fields contradict what the caller asked for.

// PayloadExpectation is what the caller independently knows about the EIP-3009
// authorization it asked the gateway to build. Unlike the CLI's internal copy, every
// field is REQUIRED: VerifySigningPayload refuses rather than skips a check it cannot
// make, since the value check skipped on an unknown is exactly the drain this guards
// against.
//
// Build it from data the gateway did not just hand back where you can: for a create,
// the request you sent (ChainID, Token, and Value = your human amount scaled by the
// token's decimals); for a payment already fetched, PayloadExpectationFor.
type PayloadExpectation struct {
	// Signer is the address of the key about to sign: message.from must equal it.
	// SignSigningPayloadFor fills it from the key; VerifySigningPayload requires it
	// (SignerAddress derives it).
	Signer Address
	// ChainID is the chain the authorization must be scoped to: domain.chainId.
	ChainID int
	// Token is the token the authorization must be against. For EIP-3009 the EIP-712
	// domain's verifyingContract IS the token.
	Token Address
	// To is who the authorization must pay: message.to, the payment's RAIL0 contract
	// (Payment.Rail0Contract). A consistency check only — that address comes from the
	// same gateway.
	To Address
	// Value is the exact amount, in TOKEN BASE UNITS, message.value must equal. Exact,
	// not a ceiling: the gateway always asks for the operation's full amount.
	Value Uint256String
}

// PayloadExpectationFor builds the expectation for a payment already fetched from the
// gateway (Payments.Get), the way the CLI's `payments sign` and `payments refund` do:
// chain, token and RAIL0 contract from the payment record, and the value from
//
//   - amount == "": the payment's own stored base-unit Amount — for re-signing an
//     unsigned payment, where the payment is what the payer is agreeing to;
//   - otherwise: amount, a HUMAN decimal you chose (e.g. the refund amount you pass to
//     RefundPrepare), scaled by the payment's Decimals.
//
// Signer is left empty: SignSigningPayloadFor fills it from the key. It fails closed —
// a nil payment, a missing chain_id, token or rail0_contract, unknown Decimals for a
// human amount, or an amount finer than the token allows is an error, not a skipped
// check.
func PayloadExpectationFor(p *Payment, amount string) (PayloadExpectation, error) {
	if p == nil {
		return PayloadExpectation{}, refuse("cannot check the authorization without the payment it is for")
	}
	want := PayloadExpectation{ChainID: p.ChainID, Token: p.Token, To: p.Rail0Contract}
	if amount == "" {
		want.Value = p.Amount
		return want, nil
	}
	if p.Decimals == nil {
		return PayloadExpectation{}, refuse("the payment's token decimals are unknown, so the authorization's amount cannot be checked (fetch the payment with Get first)")
	}
	units, err := ToBaseUnits(amount, *p.Decimals)
	if err != nil {
		return PayloadExpectation{}, refuse("amount %q: %w", amount, err)
	}
	want.Value = units
	return want, nil
}

// VerifySigningPayload checks a gateway-built EIP-3009 signing payload against what
// the caller asked for, BEFORE any key touches it. It returns an error naming the first
// field that disagrees — always starting "rail0: refusing to sign:" — and nil otherwise.
//
// In order (the CLI's order):
//
//  1. message.from is want.Signer — the one right-hand side derived locally from the
//     private key, so it holds whatever the endpoint claims;
//  2. domain.chainId is want.ChainID;
//  3. domain.verifyingContract is want.Token — this stops an authorization minted
//     against another token the signer happens to hold;
//  4. message.to is want.To, the payment's RAIL0 contract;
//  5. message.value is exactly want.Value, in base units.
//
// It also refuses a payload SignSigningPayloadFor could not sign faithfully: an unknown
// primaryType, or a value/validAfter/validBefore that is not a uint256. validAfter and
// validBefore are not compared (the CLI does not either): the gateway derives them from
// the payment's expiries, which a caller does not hold independently.
//
// FAIL-CLOSED: a nil payload, or an expectation with any field empty, is refused
// rather than skipped — the CLI skips an unknown field, but its callers always supply
// every one, and the value check skipped is the unbounded drain this exists to stop.
//
// What it does NOT establish: message.nonce is not recomputed. For authorize/charge it
// binds the signature to the payment terms on-chain, but recomputing it needs the
// deployment's contract version, which no payment response carries; so a nonce for
// different terms (another payee) is not detectable here. The bounded residual is an
// authorization for the right token, chain and signer, for exactly the amount asked —
// a loss of at most the intended amount instead of a drain of the balance.
func VerifySigningPayload(sp *SigningPayload, want PayloadExpectation) error {
	if sp == nil {
		return refuse("there is no signing payload to check (Create answers 502 signing_payload_unavailable when it cannot build one)")
	}
	if err := checkPayloadExpectation(want); err != nil {
		return err
	}

	if sp.PrimaryType != PrimaryTypeTransfer && sp.PrimaryType != PrimaryTypeReceive {
		return refuse("the authorization's primaryType %q is not one this SDK knows (%s or %s): it is older than the gateway it is talking to",
			sp.PrimaryType, PrimaryTypeTransfer, PrimaryTypeReceive)
	}

	if !strings.EqualFold(string(sp.Message.From), string(want.Signer)) {
		return refuse("the authorization is from %s (message.from) but the signing key is %s", sp.Message.From, want.Signer)
	}
	if sp.Domain.ChainId != want.ChainID {
		return refuse("the authorization is scoped to chain %d (domain.chainId) but this payment is on chain %d", sp.Domain.ChainId, want.ChainID)
	}
	if !strings.EqualFold(string(sp.Domain.VerifyingContract), string(want.Token)) {
		return refuse("the authorization is against token %s (domain.verifyingContract) but this payment's token is %s", sp.Domain.VerifyingContract, want.Token)
	}
	if !strings.EqualFold(string(sp.Message.To), string(want.To)) {
		return refuse("the authorization pays %s (message.to) but this payment's RAIL0 contract is %s", sp.Message.To, want.To)
	}

	// Range-checked parse first, so a hostile value is named as malformed rather than
	// as merely "different", then an exact base-unit comparison.
	value, err := payloadInt("value", sp.Message.Value)
	if err != nil {
		return refuse("%s", strings.TrimPrefix(err.Error(), "rail0: "))
	}
	wantValue, _ := new(big.Int).SetString(string(want.Value), 10) // validated above
	if value.Cmp(wantValue) != 0 {
		return refuse("the authorization is for %s base units (message.value) but you asked for %s", sp.Message.Value, want.Value)
	}
	for _, f := range []struct {
		name string
		raw  Uint256String
	}{{"validAfter", sp.Message.ValidAfter}, {"validBefore", sp.Message.ValidBefore}} {
		if _, err := payloadInt(f.name, f.raw); err != nil {
			return refuse("%s", strings.TrimPrefix(err.Error(), "rail0: "))
		}
	}
	return nil
}

// checkPayloadExpectation refuses an expectation that cannot back every comparison:
// each field missing, or not the shape it must have, names itself.
func checkPayloadExpectation(want PayloadExpectation) error {
	for _, a := range []struct{ field, value string }{
		{"Signer", string(want.Signer)}, {"Token", string(want.Token)}, {"To", string(want.To)},
	} {
		if !isHexAddress(a.value) {
			return refuse("cannot check the authorization without a valid expected %s address (got %q)", a.field, a.value)
		}
	}
	if want.ChainID <= 0 {
		return refuse("cannot check the authorization without the expected chain id")
	}
	if want.Value == "" {
		return refuse("cannot check the authorization's amount without the amount you asked for")
	}
	n, ok := new(big.Int).SetString(string(want.Value), 10)
	if !ok || checkUint256(uintArg{"expected Value", n}) != nil {
		return refuse("expected Value %q is not a base-unit uint256 integer", want.Value)
	}
	return nil
}

// SignSigningPayloadFor verifies the gateway's EIP-3009 signing payload against what
// the caller asked for (VerifySigningPayload, with the key's own address as the
// expected signer) and only then signs it verbatim — the signer every RAIL0 flow
// should use: authorize, charge and refund alike. A payload that fails the check
// leaves no signature behind; one that passes is signed byte-identically to the
// deprecated SignSigningPayload.
//
// want.Signer may be left empty (it is filled from the key); when set it must be the
// key's address, or the call is refused.
//
//	// create: the expectation is the request you sent, not the response
//	units, _ := rail0.ToBaseUnits(req.Amount, decimals) // decimals of req.Token, e.g. 6 for USDC
//	created, _ := client.Payments.Create(ctx, req)
//	sig, err := rail0.SignSigningPayloadFor(created.SigningPayload, payerKey, rail0.PayloadExpectation{
//	    ChainID: req.ChainID, Token: req.Token, To: created.Rail0Contract, Value: units,
//	})
//	if err != nil { /* "rail0: refusing to sign: …" — nothing was signed */ }
//	client.Payments.Sign(ctx, created.Rail0Id, rail0.PayerSignatureRequest{Signature: rail0.PackSignature(sig)})
//
//	// refund phase 1: the payee signs for the amount it chose
//	want, _ := rail0.PayloadExpectationFor(payment, "10.00")
//	sig, err = rail0.SignSigningPayloadFor(phase1.SigningPayload, payeeKey, want)
func SignSigningPayloadFor(sp *SigningPayload, privateKey []byte, want PayloadExpectation) (Eip3009Signature, error) {
	signer, err := SignerAddress(privateKey)
	if err != nil {
		return Eip3009Signature{}, err
	}
	if want.Signer != "" && !strings.EqualFold(string(want.Signer), signer) {
		return Eip3009Signature{}, refuse("the expected signer %s is not the signing key's address %s", want.Signer, signer)
	}
	want.Signer = Address(signer)
	if err := VerifySigningPayload(sp, want); err != nil {
		return Eip3009Signature{}, err
	}
	return signSigningPayload(privateKey, sp)
}
