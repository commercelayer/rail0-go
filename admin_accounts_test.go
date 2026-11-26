package rail0

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAdminAccountsListAndRead(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 200, `[{"id":"acc1","name":"Acme","email":"ops@acme.example","active":false,"admin":false,"status":"deactivated","priority_tip":true}]`)
	no := false
	page, err := s.client().Admin.Accounts(ctx, ListAdminAccountsParams{
		Query: "0x2d2b", Status: "deactivated", Admin: &no, Sort: "-created_at", Page: 2, PerPage: 25,
	})
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/accounts", map[string]string{
		"q": "0x2d2b", "status": "deactivated", "admin": "false", "sort": "-created_at", "page": "2", "per_page": "25",
	})
	if a := page.Data[0]; a.ID != "acc1" || a.Active || a.Status != "deactivated" || page.Meta.Total != 70 {
		t.Errorf("accounts = %+v, meta %+v", page.Data, page.Meta)
	}

	// Without parameters nothing is filtered: Admin nil is "do not filter", not false.
	s = newAdminServer(t, 200, `[]`)
	if _, err := s.client().Admin.Accounts(ctx); err != nil {
		t.Fatal(err)
	}
	s.wantRequest(t, http.MethodGet, "/accounts", nil)

	s = newAdminServer(t, 200, `{"id":"acc1","name":"Acme","active":true,"admin":true,"status":"active"}`)
	one, err := s.client().Admin.Account(ctx, "acc1")
	if err != nil || !one.Active || !one.Admin {
		t.Errorf("Account = %+v, %v", one, err)
	}
	s.wantRequest(t, http.MethodGet, "/accounts/acc1", nil)
}

func TestAdminAccountExposure(t *testing.T) {
	s := newAdminServer(t, 200, `{"payments":{"in_flight":1,"in_escrow":2,"disputed":0,
		"by_token":[{"chain_id":5042002,"token":"0x3600","symbol":"USDC","count":3,"amount":"125500000","amount_formatted":"125.5"},
		            {"chain_id":null,"token":"0xdead","symbol":null,"count":1,"amount":"7","amount_formatted":"7"}]},
		"payouts":{"pending":0,"submitted":1},"empty":false}`)
	e, err := s.client().Admin.AccountExposure(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("AccountExposure: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/accounts/acc1/exposure", nil)
	if e.Empty || e.Payments.InFlight != 1 || e.Payments.InEscrow != 2 || e.Payouts.Submitted != 1 {
		t.Errorf("exposure = %+v", e)
	}
	known, unknown := e.Payments.ByToken[0], e.Payments.ByToken[1]
	if known.ChainID == nil || *known.ChainID != 5042002 || known.Symbol != "USDC" || known.AmountFormatted != "125.5" {
		t.Errorf("known token = %+v", known)
	}
	if unknown.ChainID != nil || unknown.Symbol != "" || unknown.Amount != "7" {
		t.Errorf("unknown token = %+v", unknown)
	}
}

// The switch sends only the operator's field, and the acknowledgement only when given.
func TestAdminAccountSwitch(t *testing.T) {
	ctx := context.Background()
	body := func(s *adminServer) map[string]any {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal(s.body, &got); err != nil {
			t.Fatalf("body %q: %v", s.body, err)
		}
		return got
	}

	s := newAdminServer(t, 200, `{"id":"acc1","active":false,"status":"deactivated"}`)
	a, err := s.client().Admin.DeactivateAccount(ctx, "acc1", false)
	if err != nil || a.Active {
		t.Errorf("DeactivateAccount = %+v, %v", a, err)
	}
	s.wantRequest(t, http.MethodPatch, "/accounts/acc1", nil)
	if got := body(s); len(got) != 1 || got["active"] != false {
		t.Errorf("body = %v, want only active:false", got)
	}

	s = newAdminServer(t, 200, `{"id":"acc1","active":false}`)
	if _, err := s.client().Admin.DeactivateAccount(ctx, "acc1", true); err != nil {
		t.Fatal(err)
	}
	if got := body(s); got["active"] != false || got["acknowledge_open_exposure"] != true {
		t.Errorf("body = %v", got)
	}

	s = newAdminServer(t, 200, `{"id":"acc1","active":true,"status":"active"}`)
	if a, err := s.client().Admin.ActivateAccount(ctx, "acc1"); err != nil || !a.Active {
		t.Errorf("ActivateAccount = %+v, %v", a, err)
	}
	if got := body(s); len(got) != 1 || got["active"] != true {
		t.Errorf("body = %v, want only active:true", got)
	}

	// An account with something in motion is refused with a code of its own.
	s = newAdminServer(t, 409, `{"code":"account_has_open_exposure","title":"Account has open exposure","detail":"…","exposure":{"empty":false}}`)
	_, err = s.client().Admin.DeactivateAccount(ctx, "acc1", false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "account_has_open_exposure" || apiErr.Hint() == "" {
		t.Errorf("err = %v", err)
	}
}

func TestAdminWalletUsage(t *testing.T) {
	s := newAdminServer(t, 200, `{"wallet_id":"w1","address":"0x2d2b","payments_as_payee":12,"payments_as_payer":1,"transactions_as_sender":30}`)
	u, err := s.client().Admin.WalletUsage(context.Background(), "acc1", "0x2d2b")
	if err != nil || u.PaymentsAsPayee != 12 || u.TransactionsAsSender != 30 {
		t.Errorf("WalletUsage = %+v, %v", u, err)
	}
	s.wantRequest(t, http.MethodGet, "/accounts/acc1/wallets/0x2d2b/usage", nil)
}
