package rail0

import (
	"context"
	"net/http"
	"testing"
)

// The configuration comes back whole: a group the SDK was not written for is not dropped.
func TestAdminConfigIsReturnedWhole(t *testing.T) {
	s := newAdminServer(t, 200, `{"build":{"version":{"value":"1.0.1","env":null,"overridden":false,"description":"…"}},
		"payouts":{"enabled":{"value":false,"env":"RAIL0_FEATURE_PAYOUTS","overridden":true,"description":"…"}},
		"a_group_added_later":{"x":{"value":1}},"generated_at":"2026-11-26T15:00:00Z"}`)
	cfg, err := s.client().Admin.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/config", nil)
	enabled := cfg["payouts"].(map[string]any)["enabled"].(map[string]any)
	if enabled["value"] != false || enabled["env"] != "RAIL0_FEATURE_PAYOUTS" || enabled["overridden"] != true {
		t.Errorf("payouts.enabled = %v", enabled)
	}
	if cfg["build"] == nil || cfg["a_group_added_later"] == nil || cfg["generated_at"] != "2026-11-26T15:00:00Z" {
		t.Errorf("groups were dropped: %v", cfg)
	}
}

func TestAdminCatalogueRows(t *testing.T) {
	ctx := context.Background()
	s := newAdminServer(t, 200, `[{"id":"b1","chain_id":5042002,"slug":"arc","active":true,
		"rpc_urls":["https://lb.example/rpc?dkey={RAIL0_RPC_KEY_DRPC}"]},{"id":"b2","chain_id":84532,"active":false}]`)
	chains, err := s.client().Admin.Blockchains(ctx)
	if err != nil || len(chains) != 2 || chains[1]["active"] != false || chains[0]["slug"] != "arc" {
		t.Errorf("Blockchains = %v, %v", chains, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/blockchains", nil)

	s = newAdminServer(t, 200, `[{"id":"t1","blockchain_id":"b1","symbol":"USDC","eip712_name":"USDC","eip712_version":"2","active":true}]`)
	tokens, err := s.client().Admin.Tokens(ctx)
	if err != nil || len(tokens) != 1 || tokens[0]["eip712_name"] != "USDC" {
		t.Errorf("Tokens = %v, %v", tokens, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/tokens", nil)

	s = newAdminServer(t, 200, `[{"id":"c1","blockchain_id":"b1","address":"0x13a4","version":"1.4.0","active":true,"archived":false}]`)
	contracts, err := s.client().Admin.Contracts(ctx)
	if err != nil || len(contracts) != 1 || contracts[0]["version"] != "1.4.0" {
		t.Errorf("Contracts = %v, %v", contracts, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/contracts", nil)
}

func TestAdminCatalogDiffAndReload(t *testing.T) {
	ctx := context.Background()
	s := newAdminServer(t, 200, `{"loaded_at":"2026-11-26T08:00:00Z","checked_at":"2026-11-26T15:00:00Z","stale":true,"pending":1,
		"blockchains":{"added":[],"removed":[],"changed":[{"chain_id":5042002,"fields":{"active":{"from":false,"to":true}}}]},
		"tokens":{"added":[],"removed":[],"changed":[]},"contracts":{"added":[],"removed":[],"changed":[]}}`)
	diff, err := s.client().Admin.CatalogDiff(ctx)
	if err != nil || !diff.Stale || diff.Pending != 1 || diff.Blockchains == nil {
		t.Errorf("CatalogDiff = %+v, %v", diff, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/catalog", nil)

	s = newAdminServer(t, 200, `{"reloaded":true,"blockchains":10,"tokens":14,"contracts":6,"payment_blockchains":1,
		"reloaded_at":"2026-11-26T15:00:01Z","applied":{"pending":1,"blockchains":{"changed":[{"chain_id":5042002}]}}}`)
	r, err := s.client().Admin.ReloadCatalog(ctx)
	if err != nil || !r.Reloaded || r.Blockchains != 10 || r.PaymentBlockchains != 1 || r.Applied["pending"] != float64(1) {
		t.Errorf("ReloadCatalog = %+v, %v", r, err)
	}
	s.wantRequest(t, http.MethodPost, "/admin/catalog/reload", nil)
	if len(s.body) != 0 {
		t.Errorf("the reload sent a body: %s", s.body)
	}
}

func TestAdminPayoutsAndDestinations(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 200, `[{"id":"po1","status":"failed","purpose":"payout","error_code":"dropped","error_reason":"replaced",
		"block_hash":"0xbeef","account_id":"acc1","account":{"id":"acc1","name":"Acme"}}]`)
	page, err := s.client().Admin.Payouts(ctx, ListAdminPayoutsParams{
		AccountID: "acc1", WalletID: "w1", DestinationID: "d1", ChainID: 5042, Statuses: []string{"failed", "submitted"},
		Purpose: "payout", CreatedFrom: "2026-11-01T00:00:00Z", CreatedTo: "2026-11-27T00:00:00Z", Sort: "-created_at", Page: 2, PerPage: 25,
	})
	if err != nil {
		t.Fatalf("Payouts: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payouts", map[string]string{
		"account_id": "acc1", "wallet_id": "w1", "destination_id": "d1", "chain_id": "5042", "status": "failed,submitted",
		"purpose": "payout", "created_from": "2026-11-01T00:00:00Z", "created_to": "2026-11-27T00:00:00Z",
		"sort": "-created_at", "page": "2", "per_page": "25",
	})
	if p := page.Data[0]; p.ID != "po1" || p.AccountID != "acc1" || p.ErrorReason != "replaced" || p.BlockHash != "0xbeef" || page.Meta.Total != 70 {
		t.Errorf("payout = %+v", p)
	}

	s = newAdminServer(t, 200, `{"id":"po1","status":"confirmed","account_id":"acc1"}`)
	if p, err := s.client().Admin.Payout(ctx, "po1"); err != nil || p.Status != "confirmed" || p.AccountID != "acc1" {
		t.Errorf("Payout = %+v, %v", p, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payouts/po1", nil)

	s = newAdminServer(t, 200, `[{"id":"e1","action":"payout.submitted","payout_id":"po1","account_id":"acc1"}]`)
	events, err := s.client().Admin.PayoutEvents(ctx, "po1", ListPayoutEventsParams{Page: 2, PerPage: 10})
	if err != nil || events.Data[0].Action != "payout.submitted" || events.Data[0].AccountID != "acc1" {
		t.Errorf("PayoutEvents = %+v, %v", events, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payouts/po1/events", map[string]string{"page": "2", "per_page": "10"})

	s = newAdminServer(t, 200, `[{"id":"d1","status":"verified","label":"Exchange","account_id":"acc1","created_by":"0x2d2b","verified_by":"0x2d2b"}]`)
	dests, err := s.client().Admin.PayoutDestinations(ctx, ListAdminPayoutDestinationsParams{
		AccountID: "acc1", WalletID: "w1", ChainID: 5042, Status: "verified", Sort: "label",
	})
	if err != nil || dests.Data[0].Label != "Exchange" || dests.Data[0].VerifiedBy != "0x2d2b" {
		t.Errorf("PayoutDestinations = %+v, %v", dests, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payout_destinations", map[string]string{
		"account_id": "acc1", "wallet_id": "w1", "chain_id": "5042", "status": "verified", "sort": "label",
	})

	s = newAdminServer(t, 200, `{"id":"d1","status":"disabled","account_id":"acc1"}`)
	if d, err := s.client().Admin.PayoutDestination(ctx, "d1"); err != nil || d.Status != "disabled" {
		t.Errorf("PayoutDestination = %+v, %v", d, err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payout_destinations/d1", nil)

	s = newAdminServer(t, 200, `[]`)
	if _, err := s.client().Admin.PayoutDestinationEvents(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/payout_destinations/d1/events", nil)
}
