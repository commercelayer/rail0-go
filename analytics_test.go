package rail0

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The analytics summary decodes into the shape the gateway actually sends, and the two gas
// cuts add back up to the per-chain row. The identity is the point: both cuts are the same
// rows regrouped, so a client that shows them side by side must never see them disagree.
func TestAnalyticsSummaryDecodesGasAndItsSlices(t *testing.T) {
	const body = `{
	  "orders": 3, "disputed": 1, "refund_rate": 0.3333, "dispute_rate": 0.3333,
	  "failed_rate": 0.25,
	  "failures": [{"code": "insufficient_gas_funds", "transactions": 2},
	               {"code": "nonce_too_low", "transactions": 1}],
	  "by_status": {"captured": 2, "refunded": 1},
	  "volume": [{"chain_id": 8453, "token": "0xtok", "symbol": "USDC", "decimals": 6,
	              "orders": 3, "gross": "3000000", "settled": "2000000",
	              "escrowed": "0", "captured": "3000000", "refunded": "1000000"}],
	  "gas": [{"chain_id": 8453, "chain_name": "Base", "symbol": "ETH", "decimals": 18,
	           "orders": 3, "spent": "72000", "wasted": "10000",
	           "confirmed": 3, "failed": 1, "confirmation_secs": 15}],
	  "gas_by_status": [
	    {"chain_id": 8453, "key": "captured", "orders": 2, "spent": "62000", "wasted": "0",
	     "confirmed": 2, "failed": 0},
	    {"chain_id": 8453, "key": "refunded", "orders": 1, "spent": "10000",
	     "wasted": "10000", "confirmed": 1, "failed": 1}
	  ],
	  "gas_by_operation": [
	    {"chain_id": 8453, "key": "charge", "orders": null, "spent": "52000", "wasted": "0",
	     "confirmed": 2, "failed": 0},
	    {"chain_id": 8453, "key": "refund", "orders": null, "spent": "20000",
	     "wasted": "10000", "confirmed": 1, "failed": 1}
	  ]
	}`

	var sum AnalyticsSummary
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if sum.FailedRate != 0.25 {
		t.Errorf("FailedRate = %v, want 0.25", sum.FailedRate)
	}
	// Settled and Escrowed answer "where is the money now" and were missing from this
	// struct entirely, so a caller could only read half the volume row.
	if got := sum.Volume[0].Settled; got != "2000000" {
		t.Errorf("Settled = %q, want 2000000", got)
	}
	if len(sum.Gas) != 1 || sum.Gas[0].Spent != "72000" || *sum.Gas[0].Symbol != "ETH" {
		t.Fatalf("gas row not decoded: %+v", sum.Gas)
	}
	// Per chain, and a pointer so "none confirmed" is nil rather than 0 — 0 reads as instant.
	if sum.Gas[0].ConfirmationSecs == nil || *sum.Gas[0].ConfirmationSecs != 15 {
		t.Errorf("ConfirmationSecs = %v, want 15", sum.Gas[0].ConfirmationSecs)
	}
	// failed_rate says how much fails; failures says what to act on, commonest first.
	if len(sum.Failures) != 2 || sum.Failures[0].Code != "insufficient_gas_funds" ||
		sum.Failures[0].Transactions != 2 {
		t.Errorf("failures not decoded: %+v", sum.Failures)
	}
	if sum.Gas[0].Orders == nil || *sum.Gas[0].Orders != 3 {
		t.Errorf("per-chain Orders = %v, want 3", sum.Gas[0].Orders)
	}

	// The embedded AnalyticsGas is reachable through the slice, and Key names the cut.
	if sum.GasByStatus[0].Key != "captured" || sum.GasByStatus[0].Spent != "62000" {
		t.Errorf("status slice not decoded: %+v", sum.GasByStatus[0])
	}
	// No order denominator on the operation cut: one order spans several operations, so a
	// count there would be the same order counted once per operation it happens to carry.
	if sum.GasByOperation[0].Orders != nil {
		t.Errorf("operation slice Orders = %v, want nil", sum.GasByOperation[0].Orders)
	}

	for name, cut := range map[string][]AnalyticsGasSlice{
		"gas_by_status":    sum.GasByStatus,
		"gas_by_operation": sum.GasByOperation,
	} {
		total := new(big.Int)
		for _, row := range cut {
			n, ok := new(big.Int).SetString(row.Spent, 10)
			if !ok {
				t.Fatalf("%s: %q is not an integer", name, row.Spent)
			}
			total.Add(total, n)
		}
		if want, _ := new(big.Int).SetString(sum.Gas[0].Spent, 10); total.Cmp(want) != 0 {
			t.Errorf("%s sums to %s, want the chain's %s", name, total, want)
		}
	}
}

// Payee narrows the rollups to one of the account's wallets and must reach the gateway on
// all three endpoints — it rides on the shared AnalyticsFilters, so a call that dropped it
// would silently report the whole account while the caller believes it sees one wallet.
// Empty stays off the wire: absent is what means "every wallet the account owns".
func TestAnalyticsPayeeFilterIsSentOnEveryEndpoint(t *testing.T) {
	const payee = "0x1111111111111111111111111111111111111111"
	queries := map[string]url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries[r.URL.Path] = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/analytics/summary" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	ctx := context.Background()
	f := AnalyticsFilters{Payee: payee, ChainID: 8453}

	if _, err := c.Analytics.Summary(ctx, f); err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if _, err := c.Analytics.Timeseries(ctx, TimeseriesParams{AnalyticsFilters: f}); err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
	if _, err := c.Analytics.Breakdown(ctx, BreakdownParams{AnalyticsFilters: f, By: "token"}); err != nil {
		t.Fatalf("Breakdown: %v", err)
	}

	for _, path := range []string{"/analytics/summary", "/analytics/timeseries", "/analytics/breakdown"} {
		q, ok := queries[path]
		if !ok {
			t.Fatalf("%s was not called", path)
		}
		if got := q.Get("payee"); got != payee {
			t.Errorf("%s payee = %q, want %q", path, got, payee)
		}
		if got := q.Get("chain_id"); got != "8453" {
			t.Errorf("%s chain_id = %q, want 8453 alongside payee", path, got)
		}
	}

	// Unset: no payee param at all, so the gateway keeps scoping to the whole account.
	if _, err := c.Analytics.Summary(ctx); err != nil {
		t.Fatalf("Summary without filters: %v", err)
	}
	if _, present := queries["/analytics/summary"]["payee"]; present {
		t.Error("payee sent without a Payee filter, want it absent")
	}
}
