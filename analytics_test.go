package rail0

import (
	"encoding/json"
	"math/big"
	"testing"
)

// The analytics summary decodes into the shape the gateway actually sends, and the two gas
// cuts add back up to the per-chain row. The identity is the point: both cuts are the same
// rows regrouped, so a client that shows them side by side must never see them disagree.
func TestAnalyticsSummaryDecodesGasAndItsSlices(t *testing.T) {
	const body = `{
	  "orders": 3, "disputed": 1, "refund_rate": 0.3333, "dispute_rate": 0.3333,
	  "failed_rate": 0.25,
	  "by_status": {"captured": 2, "refunded": 1},
	  "volume": [{"chain_id": 8453, "token": "0xtok", "symbol": "USDC", "decimals": 6,
	              "orders": 3, "gross": "3000000", "settled": "2000000",
	              "escrowed": "0", "captured": "3000000", "refunded": "1000000"}],
	  "gas": [{"chain_id": 8453, "chain_name": "Base", "symbol": "ETH", "decimals": 18,
	           "orders": 3, "spent": "72000", "wasted": "10000",
	           "confirmed": 3, "failed": 1}],
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
