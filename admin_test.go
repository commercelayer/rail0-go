package rail0

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// adminServer answers every request with status and body, records what it was asked,
// and sends the paging headers a collection carries.
type adminServer struct {
	*httptest.Server
	method string
	path   string
	query  url.Values
	body   []byte
	auth   string
}

func newAdminServer(t *testing.T, status int, body string) *adminServer {
	t.Helper()
	s := &adminServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.method, s.path, s.query = r.Method, r.URL.Path, r.URL.Query()
		s.auth = r.Header.Get("Authorization")
		s.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "2")
		w.Header().Set("X-Per-Page", "25")
		w.Header().Set("X-Total-Count", "70")
		w.Header().Set("X-Total-Pages", "3")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *adminServer) client() *Client {
	return NewClient(ClientOptions{BaseURL: s.URL, Token: "session-jwt"})
}

func (s *adminServer) wantRequest(t *testing.T, method, path string, query map[string]string) {
	t.Helper()
	if s.method != method || s.path != path {
		t.Errorf("request = %s %s, want %s %s", s.method, s.path, method, path)
	}
	if len(s.query) != len(query) {
		t.Errorf("query = %v, want exactly %v", s.query, query)
	}
	for k, v := range query {
		if got := s.query.Get(k); got != v {
			t.Errorf("query %s = %q, want %q", k, got, v)
		}
	}
}

// The three write-mode calls: the status of the presented token, a write-mode token for
// the session, and the revocation of the one presented.
func TestAdminWriteMode(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 200, `{"active":false,"expires_at":null}`)
	st, err := s.client().Admin.WriteMode(ctx)
	if err != nil || st.Active || st.ExpiresAt != "" {
		t.Errorf("WriteMode = %+v, %v", st, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/write_mode", nil)

	s = newAdminServer(t, 201, `{"token":"write-jwt","expires_at":"2026-11-26T11:00:00Z"}`)
	tok, err := s.client().Admin.EnableWriteMode(ctx)
	if err != nil || tok.Token != "write-jwt" || tok.ExpiresAt != "2026-11-26T11:00:00Z" {
		t.Errorf("EnableWriteMode = %+v, %v", tok, err)
	}
	s.wantRequest(t, http.MethodPost, "/admin/write_mode", nil)
	if len(s.body) != 0 {
		t.Errorf("the switch asks for nothing, sent body %q", s.body)
	}
	if s.auth != "Bearer session-jwt" {
		t.Errorf("Authorization = %q, want the session token", s.auth)
	}

	// The write-mode token is presented like any token: the caller put it on the client.
	s = newAdminServer(t, 200, `{"revoked":true}`)
	c := s.client()
	c.SetToken("write-jwt")
	revoked, err := c.Admin.DisableWriteMode(ctx)
	if err != nil || !revoked {
		t.Errorf("DisableWriteMode = %v, %v", revoked, err)
	}
	s.wantRequest(t, http.MethodDelete, "/admin/write_mode", nil)
	if s.auth != "Bearer write-jwt" {
		t.Errorf("Authorization = %q, want the write-mode token", s.auth)
	}
}

// A degraded system is the content of the answer, not an error.
func TestAdminHealthReportsAVerdictNotAnError(t *testing.T) {
	s := newAdminServer(t, 200, `{"status":"degraded","timestamp":"2026-11-26T10:00:00Z",
		"checks":{"workers":{"status":"degraded","describes":"no live worker"}}}`)
	h, err := s.client().Admin.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Status != "degraded" {
		t.Errorf("Status = %q", h.Status)
	}
	checks, _ := h.Checks.(map[string]any)
	if w, _ := checks["workers"].(map[string]any); w["status"] != "degraded" {
		t.Errorf("checks = %v", h.Checks)
	}
	s.wantRequest(t, http.MethodGet, "/admin/health", nil)
}

func TestAdminRPCHealth(t *testing.T) {
	s := newAdminServer(t, 200, `{"status":"ok","checked_at":"2026-11-26T10:00:00Z","chains":[
		{"chain_id":5042002,"name":"Arc Testnet","ok":true,"status":"ok","primary_status":"ok",
		 "endpoints":[{"url":"https://rpc.example","ok":true,"status":"ok","head":123,"latency_ms":41}]}]}`)
	h, err := s.client().Admin.RPCHealth(context.Background())
	if err != nil {
		t.Fatalf("RPCHealth: %v", err)
	}
	if h.Status != "ok" || len(h.Chains) != 1 || h.Chains[0].Endpoints[0].LatencyMs != 41 {
		t.Errorf("got %+v", h)
	}
	s.wantRequest(t, http.MethodGet, "/admin/rpc_health", nil)
}

// Every filter reaches the query under the gateway's name, several statuses as one
// comma-separated value, and the page comes back from the headers.
func TestAdminTransactionsFiltersAndPage(t *testing.T) {
	s := newAdminServer(t, 200, `[{"id":"tx1","operation":"capture","status":"pending","redrivable":true,
		"error_reason":"nonce too low",
		"payment":{"id":"p1","rail0_id":"0xabc","chain_id":5042002,"amount":"2500000","decimals":6,"payee":"0x2d2b"}}]`)
	res, err := s.client().Admin.Transactions(context.Background(), ListAdminTransactionsParams{
		Stuck: "unbroadcast", Statuses: []string{"pending", "submitted"}, Operation: "capture",
		ChainID: 5042002, AccountID: "019e748b-da9a-7c3f-ba32-50572ffd5388",
		Hash: "0x" + "ab12", Sort: "-created_at", Page: 2, PerPage: 25,
	})
	if err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/transactions", map[string]string{
		"stuck": "unbroadcast", "status": "pending,submitted", "operation": "capture",
		"chain_id": "5042002", "account_id": "019e748b-da9a-7c3f-ba32-50572ffd5388",
		"hash": "0xab12", "sort": "-created_at", "page": "2", "per_page": "25",
	})
	tx := res.Data[0]
	if tx.ID != "tx1" || !tx.Redrivable || tx.ErrorReason != "nonce too low" {
		t.Errorf("transaction = %+v", tx)
	}
	if tx.Payment == nil || tx.Payment.Rail0ID != "0xabc" || tx.Payment.Decimals == nil || *tx.Payment.Decimals != 6 {
		t.Errorf("payment summary = %+v", tx.Payment)
	}
	if res.Meta.Page != 2 || res.Meta.Total != 70 || res.Meta.TotalPages != 3 {
		t.Errorf("meta = %+v", res.Meta)
	}

	// No params: no query at all.
	s = newAdminServer(t, 200, `[]`)
	if _, err := s.client().Admin.Transactions(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/transactions", nil)
}

func TestAdminSyncErrorsList(t *testing.T) {
	s := newAdminServer(t, 200, `[{"id":"e1","reason":"not_found","outcome":"confirmed",
		"transaction_hash":"0xfeed","payment_id":"0xabc","chain_id":5042002,"block_number":68717171,
		"event_type":"PaymentCaptured","created_at":"2026-11-26T09:00:00.000Z"}]`)
	res, err := s.client().Admin.SyncErrors(context.Background(), ListSyncErrorsParams{
		SyncErrorFilter: SyncErrorFilter{Reason: "not_found", Outcome: "confirmed", TxHash: "0xFEED", PaymentID: "p1"},
		Sort:            "reason", PerPage: 50,
	})
	if err != nil {
		t.Fatalf("SyncErrors: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/sync_errors", map[string]string{
		"reason": "not_found", "outcome": "confirmed", "tx_hash": "0xFEED", "payment_id": "p1",
		"sort": "reason", "per_page": "50",
	})
	e := res.Data[0]
	if e.Reason != "not_found" || e.PaymentID != "0xabc" || e.ChainID == nil || *e.ChainID != 5042002 ||
		e.BlockNumber == nil || *e.BlockNumber != 68717171 || e.EventType != "PaymentCaptured" {
		t.Errorf("sync error = %+v", e)
	}
}

// The clear sends the same filters as the listing, as a DELETE, and reports the count
// the gateway deleted. An empty filter sends no query: it empties the ledger, and the
// SDK does not second-guess the caller (the confirmation belongs to whoever calls).
func TestAdminClearSyncErrors(t *testing.T) {
	s := newAdminServer(t, 200, `{"deleted":3}`)
	n, err := s.client().Admin.ClearSyncErrors(context.Background(), SyncErrorFilter{Reason: "not_found", PaymentID: "0xabc"})
	if err != nil || n != 3 {
		t.Errorf("ClearSyncErrors = %d, %v", n, err)
	}
	s.wantRequest(t, http.MethodDelete, "/admin/sync_errors", map[string]string{"reason": "not_found", "payment_id": "0xabc"})

	s = newAdminServer(t, 200, `{"deleted":0}`)
	if _, err := s.client().Admin.ClearSyncErrors(context.Background(), SyncErrorFilter{}); err != nil {
		t.Fatal(err)
	}
	s.wantRequest(t, http.MethodDelete, "/admin/sync_errors", nil)
}

// A write without write mode is the gateway's 403 with a code of its own, which the
// caller can tell from "not an operator".
func TestAdminWriteWithoutWriteModeIsRefusedWithItsOwnCode(t *testing.T) {
	s := newAdminServer(t, 403, `{"code":"write_mode_required","title":"Write mode required","detail":"Switch write mode on."}`)
	_, err := s.client().Admin.ClearSyncErrors(context.Background(), SyncErrorFilter{Reason: "not_found"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != "write_mode_required" {
		t.Errorf("err = %v", err)
	}
}

func TestAdminEventCallbacksDisputesAndActions(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 200, `[{"id":"cb1","topic":"payments.captured","status":"failed","response_code":500,
		"webhook":{"id":"wh1","name":"Shop","account_id":"acc1","circuit_state":"open","last_attempt_status":"failed"}}]`)
	cbs, err := s.client().Admin.EventCallbacks(ctx, ListAdminEventCallbacksParams{
		PaymentID: "0xabc", WebhookID: "wh1", Status: "failed", Topic: "payments.captured",
		ResponseCode: "500", Since: "2026-11-01T00:00:00Z", Until: "2026-11-26T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("EventCallbacks: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/event_callbacks", map[string]string{
		"payment_id": "0xabc", "webhook_id": "wh1", "status": "failed", "topic": "payments.captured",
		"response_code": "500", "since": "2026-11-01T00:00:00Z", "until": "2026-11-26T00:00:00Z",
	})
	if cb := cbs.Data[0]; cb.Webhook == nil || cb.Webhook.AccountID != "acc1" || cb.Webhook.CircuitState != "open" {
		t.Errorf("delivery = %+v", cb)
	}

	s = newAdminServer(t, 200, `[{"id":"d1","status":"open","reason_code":"not_received","payment":{"id":"p1","payer":"0xe3dd"}}]`)
	ds, err := s.client().Admin.Disputes(ctx, ListAdminDisputesParams{
		Status: "open", PaymentID: "p1", ChainID: 5042002, ReasonCode: "not_received",
		From: "2026-11-01T00:00:00Z", To: "2026-11-26T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Disputes: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/disputes", map[string]string{
		"status": "open", "payment_id": "p1", "chain_id": "5042002", "reason_code": "not_received",
		"from": "2026-11-01T00:00:00Z", "to": "2026-11-26T00:00:00Z",
	})
	if d := ds.Data[0]; d.ReasonCode != "not_received" || d.Payment == nil || d.Payment.Payer != "0xe3dd" {
		t.Errorf("dispute = %+v", d)
	}

	s = newAdminServer(t, 200, `[{"id":"a1","action":"sync_errors.clear","actor_address":"0x2d2b","detail":{"deleted":3}}]`)
	as, err := s.client().Admin.Actions(ctx, ListAdminActionsParams{
		Action: "sync_errors.clear", ActorAccountID: "acc1", TargetAccountID: "acc2", ResourceType: "sync_errors",
		PaymentID: "p1", From: "2026-11-01T00:00:00Z", To: "2026-11-26T00:00:00Z", Page: 1,
	})
	if err != nil {
		t.Fatalf("Actions: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/actions", map[string]string{
		"action": "sync_errors.clear", "actor_account_id": "acc1", "target_account_id": "acc2",
		"resource_type": "sync_errors", "payment_id": "p1",
		"from": "2026-11-01T00:00:00Z", "to": "2026-11-26T00:00:00Z", "page": "1",
	})
	if a := as.Data[0]; a.Action != "sync_errors.clear" || a.ActorAddress != "0x2d2b" {
		t.Errorf("action = %+v", a)
	}
}
