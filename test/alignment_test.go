package rail0_test

// Fills the coverage gaps flagged by the SDK alignment review: the catalog
// services (Chains/Tokens) and their filters, Payments.Get, the receive
// (refund) EIP-3009 signers, the stablecoin registry, and Auth.Login's
// configurable SIWE chain id. Uses the same mock transport as client_test.go.

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"testing"

	rail0 "github.com/rail0/go-sdk"
)

func TestChainsListFilters(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []map[string]any{{"chain_id": 84532, "native_symbol": "ETH"}}))
	client := newMockClient(t, mt)

	chains, err := client.Chains.List(context.Background(), rail0.ListChainsParams{NetworkType: "testnet", Symbol: "ETH"})
	if err != nil {
		t.Fatalf("Chains.List: %v", err)
	}
	if len(chains) != 1 || chains[0].ChainID != 84532 {
		t.Fatalf("unexpected chains: %+v", chains)
	}
	q := mt.recorded[0].URL.RawQuery
	if !strings.Contains(q, "network_type=testnet") || !strings.Contains(q, "symbol=ETH") {
		t.Errorf("query missing filters: %s", q)
	}
	if mt.recorded[0].URL.Path != "/blockchains" {
		t.Errorf("path: got %s, want /blockchains", mt.recorded[0].URL.Path)
	}
}

func TestTokensListFilters(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []map[string]any{{"symbol": "USDC", "chain_id": 84532}}))
	mt.push(jsonResp(200, []map[string]any{}))
	client := newMockClient(t, mt)

	if _, err := client.Tokens.List(context.Background(), rail0.ListTokensParams{ChainID: 84532, Symbol: "USDC"}); err != nil {
		t.Fatalf("Tokens.List: %v", err)
	}
	q := mt.recorded[0].URL.RawQuery
	if !strings.Contains(q, "chain_id=84532") || !strings.Contains(q, "symbol=USDC") {
		t.Errorf("query missing filters: %s", q)
	}

	// No params → no query string.
	if _, err := client.Tokens.List(context.Background()); err != nil {
		t.Fatalf("Tokens.List (no params): %v", err)
	}
	if mt.recorded[1].URL.RawQuery != "" {
		t.Errorf("expected no query, got %s", mt.recorded[1].URL.RawQuery)
	}
}

func TestPaymentsGet(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"rail0_id": mockPaymentID, "status": "authorized", "capturable_amount": "100000000",
	}))
	client := newMockClient(t, mt)

	p, err := client.Payments.Get(context.Background(), mockPaymentID)
	if err != nil {
		t.Fatalf("Payments.Get: %v", err)
	}
	if p.Status != "authorized" || p.CapturableAmount != "100000000" {
		t.Fatalf("unexpected payment: %+v", p)
	}
	if mt.recorded[0].URL.Path != "/payments/"+mockPaymentID {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
}

func TestSignReceiveWithAuthorization(t *testing.T) {
	key, err := rail0.HexToPrivateKey(privateKeyHex)
	if err != nil {
		t.Fatalf("HexToPrivateKey: %v", err)
	}

	// Receive uses a different typehash than transfer, so the same inputs must
	// produce a different signature.
	transfer, err := rail0.SignTransferWithAuthorization(key, testDomain, rail0.SignTransferParams{
		From: payerAddress, To: contractAddress, Value: big.NewInt(100000000),
		ValidAfter: big.NewInt(0), ValidBefore: big.NewInt(9999999999), Nonce: testNonce,
	})
	if err != nil {
		t.Fatalf("SignTransfer: %v", err)
	}
	receive, err := rail0.SignReceiveWithAuthorizationRaw(rail0.SignReceiveParams{
		PrivateKey: key, TokenDomain: testDomain, From: payerAddress, To: contractAddress,
		Value: big.NewInt(100000000), ValidAfter: big.NewInt(0), ValidBefore: big.NewInt(9999999999), Nonce: testNonce,
	})
	if err != nil {
		t.Fatalf("SignReceiveRaw: %v", err)
	}
	if receive.V != 27 && receive.V != 28 {
		t.Errorf("V: got %d, want 27/28", receive.V)
	}
	if receive.R == transfer.R && receive.S == transfer.S {
		t.Error("receive signature must differ from transfer (different typehash)")
	}

	// The high-level SignReceiveWithAuthorization (from a PaymentConfig) must be
	// deterministic and produce a valid VRS.
	p := rail0.SignPaymentParams{
		PrivateKey: key, Payment: testPayment, Amount: big.NewInt(100000000),
		Nonce: testNonce, ContractAddress: contractAddress, TokenDomain: testDomain,
	}
	a, err := rail0.SignReceiveWithAuthorization(p)
	if err != nil {
		t.Fatalf("SignReceive: %v", err)
	}
	b, _ := rail0.SignReceiveWithAuthorization(p)
	if a.R != b.R || a.S != b.S || a.V != b.V {
		t.Error("SignReceiveWithAuthorization must be deterministic")
	}
}

func TestStablecoinRegistry(t *testing.T) {
	if _, ok := rail0.ChainInfo("nope"); ok {
		t.Error("unknown chain should return ok=false")
	}
	base, ok := rail0.ChainInfo("base")
	if !ok || base.ChainID != 8453 {
		t.Fatalf("base chain info: %+v ok=%v", base, ok)
	}
	eip3009 := rail0.EIP3009Tokens("base")
	if len(eip3009) == 0 {
		t.Error("base must expose at least one EIP-3009 token")
	}
	for _, tok := range eip3009 {
		if tok.Symbol == "" || tok.Address == "" {
			t.Errorf("malformed token: %+v", tok)
		}
	}
	// Every RAIL0-compatible token is EIP-3009; the registry must agree.
	if rail0.EIP3009Tokens("ethereum") == nil {
		t.Error("ethereum EIP-3009 tokens should not be nil")
	}
	_ = rail0.EIP2612Tokens("ethereum") // exercised; may be empty on some chains
}

func TestAuthLoginChainID(t *testing.T) {
	key, err := rail0.HexToPrivateKey(privateKeyHex)
	if err != nil {
		t.Fatalf("HexToPrivateKey: %v", err)
	}
	nonce := func() map[string]any {
		return map[string]any{"nonce": "testNonce123", "expires_at": "2099-01-01T00:00:00Z"}
	}
	session := func() map[string]any {
		return map[string]any{"token": "jwt", "address": payerAddress, "account_id": "acct", "expires_at": "2099-01-02T00:00:00Z"}
	}

	// authMessage reads the {message} field of the last recorded POST /auth body.
	authMessage := func(mt *mockTransport) string {
		body, _ := io.ReadAll(mt.recorded[1].Body)
		var b struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &b)
		return b.Message
	}

	// Default chain id → 1.
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, nonce()))
	mt.push(jsonResp(201, session()))
	client := newMockClient(t, mt)
	if _, err := client.Auth.Login(context.Background(), key, "localhost"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.Contains(authMessage(mt), "Chain ID: 1") {
		t.Errorf("default login must embed Chain ID: 1, got:\n%s", authMessage(mt))
	}

	// Custom chain id.
	mt2 := &mockTransport{t: t}
	mt2.push(jsonResp(201, nonce()))
	mt2.push(jsonResp(201, session()))
	client2 := newMockClient(t, mt2)
	if _, err := client2.Auth.Login(context.Background(), key, "localhost", 5042002); err != nil {
		t.Fatalf("Login (custom chain): %v", err)
	}
	if !strings.Contains(authMessage(mt2), "Chain ID: 5042002") {
		t.Errorf("custom login must embed Chain ID: 5042002, got:\n%s", authMessage(mt2))
	}
}
