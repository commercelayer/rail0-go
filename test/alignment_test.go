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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rail0 "github.com/commercelayer/rail0-go"
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

// The testnet entries, pinned because both have been wrong in ways that are
// invisible from Go: celo-sepolia carried Alfajores' chain id (44787) and a USDC
// address with no code on the chain it names, and a caller looking up an address
// to sign against gets a plausible-looking answer either way. Asserting the chain
// ids and the exact addresses is what turns that into a test failure.
func TestStablecoinRegistryTestnets(t *testing.T) {
	for _, tc := range []struct {
		chain   string
		chainID int
		tokens  map[string]string
	}{
		{"arc-testnet", 5042002, map[string]string{
			"USDC": "0x3600000000000000000000000000000000000000",
			"EURC": "0x89B50855Aa3bE2F677cD6303Cec089B5F319D72a",
		}},
		{"celo-sepolia", 11142220, map[string]string{
			"USDC": "0x01C5C0122039549AD1493B8220cABEdD739BC44E",
			"USD₮": "0xd077A400968890Eacc75cdc901F0356c943e4fDb",
		}},
		{"ethereum-sepolia", 11155111, map[string]string{
			"USDC":  "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
			"PYUSD": "0xCaC524BcA292aaade2DF8A05cC58F0a65B1B3bB9",
		}},
	} {
		info, ok := rail0.ChainInfo(tc.chain)
		if !ok {
			t.Errorf("%s: missing from the registry", tc.chain)
			continue
		}
		if info.ChainID != tc.chainID {
			t.Errorf("%s: chain id %d, want %d", tc.chain, info.ChainID, tc.chainID)
		}
		if len(info.Tokens) != len(tc.tokens) {
			t.Errorf("%s: %d tokens, want %d", tc.chain, len(info.Tokens), len(tc.tokens))
		}
		for sym, addr := range tc.tokens {
			tok, ok := info.Tokens[sym]
			if !ok {
				t.Errorf("%s: %s missing", tc.chain, sym)
				continue
			}
			if tok.Address != addr {
				t.Errorf("%s/%s: address %s, want %s", tc.chain, sym, tok.Address, addr)
			}
			if tok.Decimals != 6 {
				t.Errorf("%s/%s: decimals %d, want 6", tc.chain, sym, tok.Decimals)
			}
			// RAIL0 pulls funds with receiveWithAuthorization, so a token that is
			// not EIP-3009 cannot be paid with and must never be listed as payable.
			if !tok.EIP3009 {
				t.Errorf("%s/%s: not marked EIP-3009", tc.chain, sym)
			}
		}
	}

	// PYUSD is the first non-Circle token served, so it is the one that proves the
	// registry is not implicitly a USDC list.
	syms := map[string]bool{}
	for _, tok := range rail0.EIP3009Tokens("ethereum-sepolia") {
		syms[tok.Symbol] = true
	}
	if !syms["PYUSD"] {
		t.Error("ethereum-sepolia must expose PYUSD as an EIP-3009 token")
	}
}

func TestDisputesList(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []map[string]any{
		{
			"id":         "d1",
			"payment_id": "pay_1",
			"status":     "closed",
			"closed_by":  "payee",
			"payment":    map[string]any{"rail0_id": mockPaymentID, "amount": "1000000", "status": "refunded"},
		},
	}, "1"))
	client := newMockClient(t, mt)

	closed := "closed"
	res, err := client.Disputes.List(context.Background(), rail0.ListDisputesParams{Status: closed})
	if err != nil {
		t.Fatalf("Disputes.List: %v", err)
	}
	if len(res.Data) != 1 {
		t.Fatalf("want 1 dispute, got %d", len(res.Data))
	}
	d := res.Data[0]
	if d.Status != "closed" || d.ClosedBy != "payee" {
		t.Errorf("unexpected dispute: %+v", d)
	}
	// The embedded payment is what makes the account-level list renderable.
	if d.Payment == nil || d.Payment.Rail0Id != mockPaymentID {
		t.Errorf("expected embedded payment with rail0_id, got %+v", d.Payment)
	}
	if req := mt.recorded[0]; req.URL.Path != "/disputes" || req.URL.RawQuery != "status=closed" {
		t.Errorf("unexpected request: %s?%s", req.URL.Path, req.URL.RawQuery)
	}
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

// Redrive addresses the PAYMENT-SCOPED path, and that is the property worth pinning: the
// gateway resolves the transaction id through the payment, so an id belonging to another
// payment answers 404 rather than redriving someone else's row. A client that built the
// path from the transaction id alone would look correct until it retried a stranger's
// broadcast.
func TestPaymentsRedriveUsesThePaymentScopedPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tx-1","operation":"capture","status":"pending"}`))
	}))
	defer srv.Close()

	c := rail0.NewClient(rail0.ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	pid := "0x" + strings.Repeat("ab", 32)

	tx, err := c.Payments.Redrive(context.Background(), pid, "tx-1")
	if err != nil {
		t.Fatalf("Redrive: %v", err)
	}
	if tx.ID != "tx-1" {
		t.Errorf("ID = %q, want the redriven row", tx.ID)
	}
	want := "/payments/" + pid + "/transactions/tx-1/redrive"
	if gotMethod != http.MethodPost || gotPath != want {
		t.Errorf("called %s %s, want POST %s", gotMethod, gotPath, want)
	}
}

// TestSIWEStatementsArePurposeBound pins the one property that separates a login
// proof from a wallet-link proof: the statement text.
//
// The gateway binds each endpoint to exactly one statement
// (Policy::SIWE_LOGIN_STATEMENT / SIWE_WALLET_LINK_STATEMENT) and answers the
// other with 422 siwe_purpose_mismatch. ProveAddress used to reuse Login's
// message verbatim, so `wallets create` was rejected by every gateway carrying
// the purpose check — and nothing here noticed, because no test had ever
// asserted the statement. A login signature is handed out on every sign-in; if
// the wallet-link endpoint accepted one, anyone holding a captured login proof
// could bind that address to their own account. That is why these two strings
// must never converge again.
func TestSIWEStatementsArePurposeBound(t *testing.T) {
	const (
		loginStatement      = "Sign in to RAIL0"
		walletLinkStatement = "Add this wallet to your RAIL0 account"
	)

	key, err := rail0.HexToPrivateKey(privateKeyHex)
	if err != nil {
		t.Fatalf("HexToPrivateKey: %v", err)
	}
	nonce := map[string]any{"nonce": "purposeNonce1", "expires_at": "2099-01-01T00:00:00Z"}

	// Login signs the LOGIN statement.
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, nonce))
	mt.push(jsonResp(201, map[string]any{
		"token": "jwt", "address": payerAddress, "account_id": "acct", "expires_at": "2099-01-02T00:00:00Z",
	}))
	if _, err := newMockClient(t, mt).Auth.Login(context.Background(), key, "localhost"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	body, _ := io.ReadAll(mt.recorded[1].Body)
	var sent struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &sent)
	if !strings.Contains(sent.Message, loginStatement) {
		t.Errorf("login message must carry %q, got:\n%s", loginStatement, sent.Message)
	}
	if strings.Contains(sent.Message, walletLinkStatement) {
		t.Errorf("login message must NOT carry the wallet-link statement, got:\n%s", sent.Message)
	}

	// ProveAddress signs the WALLET-LINK statement.
	mt2 := &mockTransport{t: t}
	mt2.push(jsonResp(201, nonce))
	proof, err := newMockClient(t, mt2).Auth.ProveAddress(context.Background(), key, "localhost")
	if err != nil {
		t.Fatalf("ProveAddress: %v", err)
	}
	if !strings.Contains(proof.Message, walletLinkStatement) {
		t.Errorf("wallet proof must carry %q, got:\n%s", walletLinkStatement, proof.Message)
	}
	if strings.Contains(proof.Message, loginStatement) {
		t.Errorf("wallet proof must NOT carry the login statement, got:\n%s", proof.Message)
	}
}

// A false on a response is an answer, not an absence. With `,omitempty` the booleans
// vanished from any re-encoded value — `jq .disputed` read null, an inactive wallet
// showed no `active` at all — so the generator now emits them unconditionally.
func TestFalseBooleansSurviveReencoding(t *testing.T) {
	for _, c := range []struct {
		value any
		want  []string
	}{
		{rail0.Payment{}, []string{`"disputed":false`, `"escrow_stranded":false`}},
		{rail0.Transaction{}, []string{`"redrivable":false`}},
		{rail0.WalletWithTokens{}, []string{`"active":false`}},
		{rail0.WalletTokenHolding{}, []string{`"active":false`, `"default":false`}},
		{rail0.Webhook{}, []string{`"active":false`}},
	} {
		b, err := json.Marshal(c.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%T: %s missing from %s", c.value, w, b)
			}
		}
	}
}
