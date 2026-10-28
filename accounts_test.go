package rail0

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The account read has one shape and one caller: the holder. Email is in the response
// because the gateway's ownership guard makes the holder its only possible caller, so the
// SDK does not narrow it away.
func TestAccountDecodesTheHoldersProfile(t *testing.T) {
	const body = `{
	  "id": "019f8a3d-b781-7b00-8b75-8427f7e591d2",
	  "name": "Test Merchant",
	  "email": "merchant@rail0.test",
	  "created_at": "2026-08-01T00:00:00Z",
	  "updated_at": "2026-08-20T00:00:00Z"
	}`

	var account Account
	if err := json.Unmarshal([]byte(body), &account); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if account.Email != "merchant@rail0.test" || account.Name != "Test Merchant" {
		t.Errorf("profile not decoded: %+v", account)
	}
}

// The sender is the field that makes a release attributable, and it is legitimately absent
// on a report-by-hash submit — so the zero value has to mean "not known", not "nobody".
func TestTransactionSenderIsOptional(t *testing.T) {
	var withSender, without Transaction
	if err := json.Unmarshal([]byte(`{"operation":"release","sender":"0xabc"}`), &withSender); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"operation":"release"}`), &without); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if withSender.Sender != "0xabc" {
		t.Errorf("Sender = %q, want 0xabc", withSender.Sender)
	}
	if without.Sender != "" {
		t.Errorf("absent Sender should be the zero value, got %q", without.Sender)
	}
}

// Update is a PATCH on the account's own path carrying only the fields being changed:
// an unset field must stay off the wire, because the gateway treats a present key as a
// change and the operator-only active flag must never be sent from here at all.
func TestAccountsUpdatePatchesOnlyTheGivenFields(t *testing.T) {
	const accountID = "019f8a3d-b781-7b00-8b75-8427f7e591d2"
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + accountID + `","name":"Renamed","email":"merchant@rail0.test"}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	account, err := c.Accounts.Update(context.Background(), accountID, UpdateAccountRequest{Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/accounts/"+accountID {
		t.Errorf("request = %s %s, want PATCH /accounts/%s", gotMethod, gotPath, accountID)
	}
	if gotBody["name"] != "Renamed" {
		t.Errorf("body name = %v, want Renamed", gotBody["name"])
	}
	for _, absent := range []string{"email", "active"} {
		if _, present := gotBody[absent]; present {
			t.Errorf("body carries %q, want it absent: %v", absent, gotBody)
		}
	}
	if account.Name != "Renamed" || account.ID != accountID {
		t.Errorf("response not decoded: %+v", account)
	}
}

// priority_tip is a pointer precisely so that false is sendable: `omitempty` on a plain
// bool would drop the one value that switches the fee setting off, and the PATCH would
// read as "nothing to change".
func TestAccountsUpdateSendsPriorityTipFalse(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"acc","name":"Merchant","email":"merchant@rail0.test","priority_tip":false}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	off := false
	account, err := c.Accounts.Update(context.Background(), "acc", UpdateAccountRequest{PriorityTip: &off})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if v, ok := gotBody["priority_tip"]; !ok || v != false {
		t.Errorf("body priority_tip = %v (present %v), want false", v, ok)
	}
	if len(gotBody) != 1 {
		t.Errorf("body = %v, want priority_tip alone", gotBody)
	}
	if account.PriorityTip {
		t.Errorf("response priority_tip not decoded: %+v", account)
	}
}

// A payment window is sent as seconds, or as null to go back to the default, and an
// unset one is not sent at all; the profile's windows and their rules decode.
func TestAccountsUpdateSendsPaymentWindows(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"acc","name":"Merchant","email":"merchant@rail0.test","priority_tip":true,` +
			`"authorization_ttl":172800,"refund_ttl":2592000,"payment_windows":{` +
			`"authorization_ttl":{"custom":true,"default":604800,"min":3600,"max":2592000},` +
			`"refund_ttl":{"custom":false,"default":2592000,"max":15552000},"min_refund_window":86400}}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	account, err := c.Accounts.Update(context.Background(), "acc", UpdateAccountRequest{
		AuthorizationTTL: WindowSeconds(172800),
		RefundTTL:        WindowDefault(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if v := gotBody["authorization_ttl"]; v != float64(172800) {
		t.Errorf("body authorization_ttl = %v, want 172800", v)
	}
	if v, ok := gotBody["refund_ttl"]; !ok || v != nil {
		t.Errorf("body refund_ttl = %v (present %v), want null", v, ok)
	}
	if len(gotBody) != 2 {
		t.Errorf("body = %v, want the two windows alone", gotBody)
	}
	if account.AuthorizationTTL != 172800 || account.PaymentWindows == nil ||
		!account.PaymentWindows.AuthorizationTTL.Custom || account.PaymentWindows.MinRefundWindow != 86400 {
		t.Errorf("windows not decoded: %+v %+v", account, account.PaymentWindows)
	}
}

// A taken name or email is the gateway's 409 conflict, and it must reach the caller as a
// typed APIError it can branch on rather than a decoded-but-empty account.
func TestAccountsUpdateSurfacesAConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict","title":"Conflict","detail":"Already exists."}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	_, err := c.Accounts.Update(context.Background(), "acc", UpdateAccountRequest{Email: "taken@rail0.test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "conflict" {
		t.Errorf("APIError = %d %q, want 409 conflict", apiErr.Status, apiErr.Code)
	}
}
