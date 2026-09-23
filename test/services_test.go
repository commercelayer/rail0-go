package rail0_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	rail0 "github.com/commercelayer/rail0-go"
)

// ================================================================
//  Auth
// ================================================================

func TestGetNonce_PostsToAuthNonces(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"nonce": "abc123", "expires_at": "2026-01-01T00:00:00Z",
	}))
	client := newMockClient(t, mt)

	res, err := client.Auth.GetNonce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	if req.URL.Path != "/auth/nonces" {
		t.Errorf("path: got %s, want /auth/nonces", req.URL.Path)
	}
	if res.Nonce != "abc123" {
		t.Errorf("nonce: got %s, want abc123", res.Nonce)
	}
}

func TestVerify_PostsToAuth(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"token": "jwt-token", "address": "0xabc", "account_id": "acc_1",
		"expires_at": "2026-01-01T00:00:00Z",
	}))
	client := newMockClient(t, mt)

	res, err := client.Auth.Verify(context.Background(), rail0.AuthRequest{
		Message: "msg", Signature: "0xsig",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/auth" {
		t.Errorf("path: got %s, want /auth", mt.recorded[0].URL.Path)
	}
	if res.Token != "jwt-token" {
		t.Errorf("token: got %s", res.Token)
	}
}

// ================================================================
//  Health
// ================================================================

func TestHealth_GetsHealth(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"status": "degraded", "api_version": "v1", "contract_version": "1.2.1",
		"db": "ok", "active_chains": 2, "active_contracts": 1,
		"sidekiq":   map[string]any{"status": "degraded", "redis": "ok", "processes": 0, "enqueued": 12},
		"timestamp": "2026-01-01T00:00:00Z",
	}))
	client := newMockClient(t, mt)

	res, err := client.Health.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/health" {
		t.Errorf("path: got %s, want /health", mt.recorded[0].URL.Path)
	}
	if res.Status != "degraded" || res.ActiveChains != 2 {
		t.Errorf("unexpected health: %+v", res)
	}
	// The sidekiq section is the only thing that says WHY the status is degraded.
	if res.Sidekiq == nil {
		t.Fatal("sidekiq must be decoded when the gateway reports it")
	}
	if res.Sidekiq.Status != "degraded" || res.Sidekiq.Redis != "ok" || res.Sidekiq.Enqueued != 12 {
		t.Errorf("unexpected sidekiq: %+v", res.Sidekiq)
	}
}

// An anonymous caller gets liveness only: the session-gated detail is absent, and
// Sidekiq stays nil rather than looking like a reported-but-empty fleet.
func TestHealth_AnonymousResponseOmitsGatedDetail(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"status": "ok", "db": "ok", "timestamp": "2026-01-01T00:00:00Z",
	}))
	client := newMockClient(t, mt)

	res, err := client.Health.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || res.DB != "ok" {
		t.Errorf("unexpected health: %+v", res)
	}
	if res.Sidekiq != nil {
		t.Errorf("Sidekiq must be nil when not reported, got %+v", res.Sidekiq)
	}
}

// ================================================================
//  Accounts / wallets
// ================================================================

func TestWallets_ListWithFilters(t *testing.T) {
	mt := &mockTransport{t: t}
	// The gateway returns a bare array + pagination headers (no envelope).
	mt.push(listResp(200, []any{map[string]any{
		"id": "w_1", "address": "0xabc", "active": true,
		"tokens": []any{map[string]any{
			"active": true, "default": false,
			"token": map[string]any{"symbol": "USDC", "chain_id": 8453, "address": "0xtok"},
		}},
	}}, "1"))
	client := newMockClient(t, mt)

	active := true
	res, err := client.Wallets.List(context.Background(), "acc_1", rail0.ListWalletsParams{
		ChainID: 8453, TokenSymbol: "USDC", Active: &active,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.URL.Path != "/accounts/acc_1/wallets" {
		t.Errorf("path: got %s", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("chain_id") != "8453" || q.Get("token_symbol") != "USDC" || q.Get("active") != "true" {
		t.Errorf("query: got %s", req.URL.RawQuery)
	}
	// Wallet-centric shape: one wallet, its tokens nested under Tokens.
	if len(res.Data) != 1 || len(res.Data[0].Tokens) != 1 || res.Data[0].Tokens[0].Token.Symbol != "USDC" {
		t.Errorf("unexpected data: %+v", res.Data)
	}
	// Pagination meta comes from headers.
	if res.Meta.Total != 1 {
		t.Errorf("meta.total: got %d, want 1", res.Meta.Total)
	}
}

// PaymentMethods by account_id → all the merchant's active wallets, each with
// its active token holdings nested (public endpoint, bare array, no JWT).
func TestPaymentMethods_ByAccount(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []any{
		map[string]any{
			"id": "w_1", "address": "0xpayee1", "active": true,
			"tokens": []any{
				map[string]any{"active": true, "default": true, "token": map[string]any{"symbol": "USDC", "chain_id": 8453, "address": "0xusdc"}},
			},
		},
		map[string]any{
			"id": "w_2", "address": "0xpayee2", "active": true, "tokens": []any{},
		},
	}))
	client := newMockClient(t, mt)

	wallets, err := client.PaymentMethods.List(context.Background(), rail0.PaymentMethodsQuery{AccountID: "acc_1"})
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/payment_methods" || mt.recorded[0].URL.Query().Get("account_id") != "acc_1" {
		t.Errorf("request: got %s?%s", mt.recorded[0].URL.Path, mt.recorded[0].URL.RawQuery)
	}
	if len(wallets) != 2 {
		t.Fatalf("wallets: got %+v", wallets)
	}
	if wallets[0].Address != "0xpayee1" || len(wallets[0].Tokens) != 1 || wallets[0].Tokens[0].Token.Symbol != "USDC" || wallets[0].Tokens[0].Token.ChainID != 8453 {
		t.Errorf("wallet[0]: %+v", wallets[0])
	}
}

// PaymentMethods by address → sends the address handle instead of account_id.
func TestPaymentMethods_ByAddress(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []any{
		map[string]any{"id": "w_1", "address": "0xabc", "active": true, "tokens": []any{}},
	}))
	client := newMockClient(t, mt)

	wallets, err := client.PaymentMethods.List(context.Background(), rail0.PaymentMethodsQuery{Address: "0xABC"})
	if err != nil {
		t.Fatal(err)
	}
	q := mt.recorded[0].URL.Query()
	if q.Get("address") != "0xABC" || q.Has("account_id") {
		t.Errorf("query: got %s", mt.recorded[0].URL.RawQuery)
	}
	if len(wallets) != 1 || wallets[0].Address != "0xabc" {
		t.Errorf("wallets: %+v", wallets)
	}
}

func TestWallets_GetSingle(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"id": "w_1", "account_id": "acc_1", "address": "0xabc", "active": true,
	}))
	client := newMockClient(t, mt)

	// A wallet can be fetched by id or by its 0x address (unique per account).
	res, err := client.Wallets.Get(context.Background(), "acc_1", "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/accounts/acc_1/wallets/0xabc" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
	if res.ID != "w_1" || res.Address != "0xabc" {
		t.Errorf("wallet: got %+v", res)
	}
}

func TestWallets_Create(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"id": "w_1", "account_id": "acc_1", "address": "0xabc", "active": true,
	}))
	client := newMockClient(t, mt)

	res, err := client.Wallets.Create(context.Background(), "acc_1", rail0.CreateWalletRequest{
		Address: "0xabc", Message: "siwe-msg", Signature: "0xproof", Label: "Treasury",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodPost || req.URL.Path != "/accounts/acc_1/wallets" {
		t.Errorf("got %s %s", req.Method, req.URL.Path)
	}
	// The SIWE proof-of-ownership (message + signature) must be sent in the body.
	var body struct {
		Address   string `json:"address"`
		Message   string `json:"message"`
		Signature string `json:"signature"`
	}
	raw, _ := io.ReadAll(req.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Address != "0xabc" || body.Message != "siwe-msg" || body.Signature != "0xproof" {
		t.Errorf("create body missing proof-of-ownership: %+v", body)
	}
	if res.ID != "w_1" || res.Address != "0xabc" {
		t.Errorf("unexpected wallet: %+v", res)
	}
}

func TestProveAddress_SignsNonceWithoutAuthenticating(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"nonce": "walletNonce9", "expires_at": "2099-01-01T00:00:00Z",
	}))
	client := newMockClient(t, mt)

	key, err := rail0.HexToPrivateKey("0x1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("HexToPrivateKey: %v", err)
	}

	proof, err := client.Auth.ProveAddress(context.Background(), key, "localhost")
	if err != nil {
		t.Fatalf("ProveAddress: %v", err)
	}

	// Only the nonce endpoint is hit — ProveAddress must NOT POST /auth (it does
	// not authenticate the client; the wallet add is authorized by the caller's
	// own session, which may be a different address).
	if len(mt.recorded) != 1 || mt.recorded[0].URL.Path != "/auth/nonces" {
		t.Fatalf("expected exactly one POST /auth/nonces, got %d requests", len(mt.recorded))
	}
	if proof.Message == "" || proof.Signature == "" {
		t.Errorf("proof must carry message + signature, got %+v", proof)
	}
	// The signed message embeds the fetched nonce and the default login chain id.
	if !strings.Contains(proof.Message, "walletNonce9") {
		t.Errorf("proof message must embed the fetched nonce, got:\n%s", proof.Message)
	}
	if !strings.Contains(proof.Message, "Chain ID: 1") {
		t.Errorf("proof message must embed default Chain ID: 1, got:\n%s", proof.Message)
	}
}

func TestWallets_Update_UsesPATCH(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{"id": "w_1", "account_id": "acc_1", "active": false}))
	client := newMockClient(t, mt)

	active := false
	if _, err := client.Wallets.Update(context.Background(), "acc_1", "w_1", rail0.UpdateWalletRequest{
		Label: "Main", Active: &active,
	}); err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodPatch {
		t.Errorf("method: got %s, want PATCH", req.Method)
	}
	if req.URL.Path != "/accounts/acc_1/wallets/w_1" {
		t.Errorf("path: got %s", req.URL.Path)
	}
}

func TestWallets_Delete(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(204, nil))
	client := newMockClient(t, mt)

	if err := client.Wallets.Delete(context.Background(), "acc_1", "w_1"); err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodDelete || req.URL.Path != "/accounts/acc_1/wallets/w_1" {
		t.Errorf("got %s %s", req.Method, req.URL.Path)
	}
}

func TestWallets_Balances(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"wallet_id": "wt_1",
		"address":   "0xabc",
		"balances": []any{map[string]any{
			"chain_id":     84532,
			"network_type": "testnet",
			"native":       map[string]any{"symbol": "ETH", "decimals": 18, "raw": "482000000000000000", "amount": "0.482"},
			"tokens": []any{map[string]any{
				"symbol": "USDC", "address": "0xtok", "decimals": 6, "raw": "120500000", "amount": "120.50",
			}},
		}},
	}))
	client := newMockClient(t, mt)

	res, err := client.Wallets.Balances(context.Background(), "acc_1", "wt_1", rail0.WalletBalancesParams{
		ChainID: 84532, TokenSymbol: "USDC",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.URL.Path != "/accounts/acc_1/wallets/wt_1/balances" {
		t.Errorf("path: got %s", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("chain_id") != "84532" || q.Get("token_symbol") != "USDC" {
		t.Errorf("query: got %s", req.URL.RawQuery)
	}
	if res.WalletID != "wt_1" || len(res.Balances) != 1 {
		t.Fatalf("unexpected balances: %+v", res)
	}
	chain := res.Balances[0]
	if chain.ChainID != 84532 || chain.Native.Symbol != "ETH" {
		t.Errorf("chain: got %+v", chain)
	}
	if len(chain.Tokens) != 1 || chain.Tokens[0].Symbol != "USDC" || chain.Tokens[0].Raw != "120500000" {
		t.Errorf("tokens: got %+v", chain.Tokens)
	}
}

// ================================================================
//  Webhooks
// ================================================================

func webhookFixture() map[string]any {
	return map[string]any{
		"id": "wh_1", "name": "n", "callback_url": "https://x",
		"topics": []any{"payments.created", "payments.captured"},
		"active": true, "circuit_state": "closed", "circuit_failure_count": 0,
		"created_at": "2026-01-01T00:00:00Z",
	}
}

func TestWebhooks_Create_ReturnsSecret(t *testing.T) {
	mt := &mockTransport{t: t}
	f := webhookFixture()
	f["shared_secret"] = "whsec_abc"
	mt.push(jsonResp(201, f))
	client := newMockClient(t, mt)

	res, err := client.Webhooks.Create(context.Background(), rail0.CreateWebhookRequest{
		Name:        "n",
		CallbackURL: "https://x",
		Topics:      []string{rail0.TopicPaymentsCreated, rail0.TopicPaymentsCaptured},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/webhooks" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
	if res.SharedSecret != "whsec_abc" {
		t.Errorf("shared_secret: got %s", res.SharedSecret)
	}
	// The whole point of gateway #231: several topics, ONE secret. Read back as a set,
	// and sent as one — a body still carrying `topic` would be a different subscription.
	if len(res.Topics) != 2 || res.Topics[0] != rail0.TopicPaymentsCreated {
		t.Errorf("topics: got %v", res.Topics)
	}
	// The mock clones the request before the body is consumed, so the body is readable.
	body, err := io.ReadAll(mt.recorded[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if _, ok := sent["topic"]; ok {
		t.Errorf("body must not carry the deprecated singular: %v", sent)
	}
	if got, ok := sent["topics"].([]any); !ok || len(got) != 2 {
		t.Errorf("topics in body: got %v", sent["topics"])
	}
}

func TestWebhooks_ListWithFilters(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []any{webhookFixture()}, "1"))
	client := newMockClient(t, mt)

	active := true
	if _, err := client.Webhooks.List(context.Background(), rail0.ListWebhooksParams{
		Topic: rail0.TopicPaymentsCharged, Active: &active, CircuitState: "closed",
	}); err != nil {
		t.Fatal(err)
	}
	q := mt.recorded[0].URL.Query()
	if q.Get("topic") != "payments.charged" || q.Get("active") != "true" || q.Get("circuit_state") != "closed" {
		t.Errorf("query: got %s", mt.recorded[0].URL.RawQuery)
	}
}

func TestWebhooks_StateTransitions(t *testing.T) {
	cases := []struct {
		name string
		call func(*rail0.Client) error
		path string
	}{
		{"enable", func(c *rail0.Client) error { _, e := c.Webhooks.Enable(context.Background(), "wh_1"); return e }, "/webhooks/wh_1/enable"},
		{"disable", func(c *rail0.Client) error { _, e := c.Webhooks.Disable(context.Background(), "wh_1"); return e }, "/webhooks/wh_1/disable"},
		{"rotate", func(c *rail0.Client) error { _, e := c.Webhooks.RotateSecret(context.Background(), "wh_1"); return e }, "/webhooks/wh_1/rotate_secret"},
		{"reset", func(c *rail0.Client) error { _, e := c.Webhooks.ResetCircuit(context.Background(), "wh_1"); return e }, "/webhooks/wh_1/reset_circuit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mt := &mockTransport{t: t}
			mt.push(jsonResp(200, webhookFixture()))
			client := newMockClient(t, mt)
			if err := tc.call(client); err != nil {
				t.Fatal(err)
			}
			req := mt.recorded[0]
			if req.Method != http.MethodPut {
				t.Errorf("method: got %s, want PUT", req.Method)
			}
			if req.URL.Path != tc.path {
				t.Errorf("path: got %s, want %s", req.URL.Path, tc.path)
			}
		})
	}
}

func TestWebhooks_EventCallbacks(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []any{map[string]any{"id": "ec_1", "webhook_id": "wh_1", "status": "delivered"}}, "1"))
	client := newMockClient(t, mt)

	res, err := client.Webhooks.EventCallbacks(context.Background(), "wh_1", rail0.ListEventCallbacksParams{Status: "delivered"})
	if err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/webhooks/wh_1/event_callbacks" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
	if len(res.Data) != 1 || res.Data[0].Status != "delivered" {
		t.Errorf("unexpected data: %+v", res.Data)
	}
}

func TestWebhooks_Delete(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(204, nil))
	client := newMockClient(t, mt)

	if err := client.Webhooks.Delete(context.Background(), "wh_1"); err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodDelete || req.URL.Path != "/webhooks/wh_1" {
		t.Errorf("got %s %s", req.Method, req.URL.Path)
	}
}

// ================================================================
//  Webhook delivery signature verification
// ================================================================

const webhookSecret = "shhh-a-shared-secret"

// webhookSignature is an independent implementation of the gateway's scheme
// (OpenSSL::HMAC.hexdigest("SHA256", secret, "#{timestamp}.#{body}")), so the
// tests cross-check the SDK rather than reuse its code.
func webhookSignature(t *testing.T, secret, timestamp, body string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature_AcceptsAGenuineDelivery(t *testing.T) {
	body := `{"topic":"payments.captured","payment":{"rail0_id":"0xabc"}}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	if !rail0.VerifyWebhookSignature(webhookSecret, body, ts, webhookSignature(t, webhookSecret, ts, body)) {
		t.Error("a correctly signed, fresh delivery must verify")
	}
	// Hex case carries no meaning — an upper-case digest is the same digest.
	upper := strings.ToUpper(webhookSignature(t, webhookSecret, ts, body))
	if !rail0.VerifyWebhookSignature(webhookSecret, body, ts, upper) {
		t.Error("an upper-case hex signature must verify")
	}
}

func TestVerifyWebhookSignature_RejectsForgeriesAndTampering(t *testing.T) {
	body := `{"topic":"payments.captured"}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := webhookSignature(t, webhookSecret, ts, body)

	cases := []struct {
		name                          string
		secret, body, timestamp, sign string
	}{
		{"wrong secret", "other-secret", body, ts, sig},
		{"tampered body", webhookSecret, `{"topic":"payments.voided"}`, ts, sig},
		// The timestamp is inside the preimage, so it cannot be moved forward to
		// refresh a captured delivery — that is what bounds the replay.
		{"tampered timestamp", webhookSecret, body, strconv.FormatInt(time.Now().Unix()+1, 10), sig},
		{"empty secret", "", body, ts, webhookSignature(t, "", ts, body)},
		{"missing signature", webhookSecret, body, ts, ""},
		{"missing timestamp", webhookSecret, body, "", sig},
		{"non-numeric timestamp", webhookSecret, body, "not-a-timestamp", sig},
		{"non-hex signature", webhookSecret, body, ts, "zzzz"},
		{"truncated signature", webhookSecret, body, ts, sig[:32]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rail0.VerifyWebhookSignature(tc.secret, tc.body, tc.timestamp, tc.sign) {
				t.Error("expected verification to fail")
			}
		})
	}
}

// A delivery outside the tolerance window is refused even though its signature is
// valid — symmetrically, so a timestamp far in the future is no proof of freshness.
func TestVerifyWebhookSignature_RejectsOutsideToleranceWindow(t *testing.T) {
	body := `{"topic":"payments.captured"}`

	for _, offset := range []int64{-(rail0.WebhookToleranceSecs + 60), rail0.WebhookToleranceSecs + 60} {
		ts := strconv.FormatInt(time.Now().Unix()+offset, 10)
		if rail0.VerifyWebhookSignature(webhookSecret, body, ts, webhookSignature(t, webhookSecret, ts, body)) {
			t.Errorf("a delivery %ds away must be rejected", offset)
		}
	}

	// Just inside the window is still accepted.
	ts := strconv.FormatInt(time.Now().Unix()-(rail0.WebhookToleranceSecs-10), 10)
	if !rail0.VerifyWebhookSignature(webhookSecret, body, ts, webhookSignature(t, webhookSecret, ts, body)) {
		t.Error("a delivery inside the tolerance window must verify")
	}
}

// ================================================================
//  Payments — idempotency + unified prepare/submit
// ================================================================

func TestCreatePayment_SetsIdempotencyKey(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.Create(context.Background(), mockCreateReq, "idem-123"); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[0].Header.Get("Idempotency-Key"); got != "idem-123" {
		t.Errorf("Idempotency-Key: got %q, want idem-123", got)
	}
}

func TestRefundPrepare_Phase1_ParsesSigningPayload(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"id": "tx_1", "operation": "refund", "status": "pending",
		"signing_payload": map[string]any{
			"primaryType": "TransferWithAuthorization",
			"domain":      map[string]any{},
			"types":       map[string]any{},
			"message":     map[string]any{"nonce": "0xabc"},
		},
	}))
	client := newMockClient(t, mt)

	res, err := client.Payments.RefundPrepare(context.Background(), mockPaymentID, rail0.PrepareRequest{Amount: "5"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SigningPayload == nil || res.SigningPayload.Message.Nonce != "0xabc" {
		t.Errorf("expected signing payload nonce 0xabc, got %+v", res.SigningPayload)
	}
}

func TestPrepare_BuildsOpPath(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockTxResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.Prepare(context.Background(), mockPaymentID, rail0.OpAuthorize); err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/payments/"+mockPaymentID+"/authorize/prepare" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
}

// sanity: response body of a submit decodes into Transaction
func TestSubmit_DecodesTransaction(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmittedTxResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Submit(context.Background(), mockPaymentID, rail0.OpAuthorize, rail0.SubmitTransactionRequest{SignedTransaction: "0x1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s", res.Status)
	}
	body, _ := io.ReadAll(mt.recorded[0].Body)
	var decoded rail0.SubmitTransactionRequest
	_ = json.Unmarshal(body, &decoded)
	if decoded.SignedTransaction != "0x1" {
		t.Errorf("signed_transaction: got %s", decoded.SignedTransaction)
	}
}

// sanity: SubmitByHash sends transaction_hash and decodes the Transaction reply
func TestSubmitByHash_DecodesTransaction(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmittedTxResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.SubmitByHash(context.Background(), mockPaymentID, rail0.OpCapture, rail0.SubmitByHashRequest{TransactionHash: "0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s", res.Status)
	}
	body, _ := io.ReadAll(mt.recorded[0].Body)
	var decoded rail0.SubmitByHashRequest
	_ = json.Unmarshal(body, &decoded)
	if decoded.TransactionHash != "0xabc" {
		t.Errorf("transaction_hash: got %s", decoded.TransactionHash)
	}
}

// The payer's MetaMask report-by-hash endpoints post the hash to the
// dispute/close-dispute submitted paths.
func TestDisputeSubmitByHash_PostsToDisputeSubmitted(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmittedTxResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.DisputeSubmitByHash(context.Background(), mockPaymentID, rail0.SubmitByHashRequest{TransactionHash: "0xdisp"}); err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/payments/"+mockPaymentID+"/dispute/submitted" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
	body, _ := io.ReadAll(mt.recorded[0].Body)
	var decoded rail0.SubmitByHashRequest
	_ = json.Unmarshal(body, &decoded)
	if decoded.TransactionHash != "0xdisp" {
		t.Errorf("transaction_hash: got %s", decoded.TransactionHash)
	}
}

func TestCloseDisputeSubmitByHash_PostsToCloseSubmitted(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmittedTxResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.CloseDisputeSubmitByHash(context.Background(), mockPaymentID, rail0.SubmitByHashRequest{TransactionHash: "0xclose"}); err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Path != "/payments/"+mockPaymentID+"/dispute/close/submitted" {
		t.Errorf("path: got %s", mt.recorded[0].URL.Path)
	}
}

// ── Wallet token management (#33) ────────────────────────────────────────────
//
// What a wallet ACCEPTS is what drives the public GET /payment_methods and gates
// payment creation, so these four are not conveniences: without them a Go integration
// could create a wallet and then had to leave the SDK to make it usable as a payee.
// rail0-ts and rail0-ruby have had them all along.
//
// The assertions are on method + path + body, because that is the whole surface — a
// wrong verb or a `token_id` sent as the holding's id rather than the token's fails at
// the gateway as a 404 or a 405, which is exactly the class of mistake a mock transport
// catches and a live call reports too late.

func TestWallets_AddToken_PostsThePair(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"token_id": "tok_1", "active": true, "default": true,
	}))
	client := newMockClient(t, mt)

	isDefault := true
	holding, err := client.Wallets.AddToken(context.Background(), "acc_1", "w_1", rail0.AddWalletTokenRequest{
		ChainID: 84532,
		Token:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Default: &isDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	if holding.TokenID != "tok_1" || !holding.Default {
		t.Errorf("holding: got %+v", holding)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	if req.URL.Path != "/accounts/acc_1/wallets/w_1/tokens" {
		t.Errorf("path: got %s", req.URL.Path)
	}
	body, _ := io.ReadAll(req.Body)
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["chain_id"] != float64(84532) {
		t.Errorf("chain_id: got %v", sent["chain_id"])
	}
	if sent["default"] != true {
		t.Errorf("default must be sent when set: got %v", sent["default"])
	}
}

func TestWallets_AddToken_OmitsDefaultWhenUnset(t *testing.T) {
	// Default is a pointer so `false` can be sent explicitly; unset must not appear at
	// all, or every AddToken would silently clear the wallet's default token.
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{"token_id": "tok_1", "active": true}))
	client := newMockClient(t, mt)

	if _, err := client.Wallets.AddToken(context.Background(), "acc_1", "w_1", rail0.AddWalletTokenRequest{
		ChainID: 84532, Token: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(mt.recorded[0].Body)
	if strings.Contains(string(body), "default") {
		t.Errorf("unset Default must be omitted, got body: %s", body)
	}
}

func TestWallets_RemoveToken_DeletesByTokenID(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(204, nil))
	client := newMockClient(t, mt)

	if err := client.Wallets.RemoveToken(context.Background(), "acc_1", "w_1", "tok_1"); err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodDelete {
		t.Errorf("method: got %s, want DELETE", req.Method)
	}
	if req.URL.Path != "/accounts/acc_1/wallets/w_1/tokens/tok_1" {
		t.Errorf("path: got %s", req.URL.Path)
	}
}

func TestWallets_EnableAndDisableToken_PatchTheirSubAction(t *testing.T) {
	for _, tc := range []struct {
		action string
		call   func(c *rail0.Client) (*rail0.WalletTokenHolding, error)
	}{
		{"enable", func(c *rail0.Client) (*rail0.WalletTokenHolding, error) {
			return c.Wallets.EnableToken(context.Background(), "acc_1", "w_1", "tok_1")
		}},
		{"disable", func(c *rail0.Client) (*rail0.WalletTokenHolding, error) {
			return c.Wallets.DisableToken(context.Background(), "acc_1", "w_1", "tok_1")
		}},
	} {
		mt := &mockTransport{t: t}
		mt.push(jsonResp(200, map[string]any{"token_id": "tok_1", "active": tc.action == "enable"}))
		client := newMockClient(t, mt)

		holding, err := tc.call(client)
		if err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if holding.Active != (tc.action == "enable") {
			t.Errorf("%s: active got %v", tc.action, holding.Active)
		}
		req := mt.recorded[0]
		if req.Method != http.MethodPatch {
			t.Errorf("%s method: got %s, want PATCH", tc.action, req.Method)
		}
		if want := "/accounts/acc_1/wallets/w_1/tokens/tok_1/" + tc.action; req.URL.Path != want {
			t.Errorf("%s path: got %s, want %s", tc.action, req.URL.Path, want)
		}
	}
}

// The filters the gateway accepts and the SDK used to drop: payments carrying an
// operation, wallets' nested holdings by active flag, and deliveries by the
// subscriber's exact response code.
func TestListFiltersTheGatewayAcceptsReachTheQuery(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []any{}, "0"))
	mt.push(listResp(200, []any{}, "0"))
	mt.push(listResp(200, []any{}, "0"))
	client := newMockClient(t, mt)
	ctx := context.Background()

	if _, err := client.Payments.List(ctx, rail0.ListPaymentsParams{Operation: "dispute"}); err != nil {
		t.Fatal(err)
	}
	inactive := false
	if _, err := client.Wallets.List(ctx, "acc_1", rail0.ListWalletsParams{TokenActive: &inactive}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Webhooks.EventCallbacks(ctx, "wh_1", rail0.ListEventCallbacksParams{ResponseCode: "500"}); err != nil {
		t.Fatal(err)
	}

	for i, want := range []struct{ key, value string }{
		{"operation", "dispute"}, {"token_active", "false"}, {"response_code", "500"},
	} {
		if got := mt.recorded[i].URL.Query().Get(want.key); got != want.value {
			t.Errorf("request %d: %s = %q, want %q (query %s)", i, want.key, got, want.value, mt.recorded[i].URL.RawQuery)
		}
	}
}
