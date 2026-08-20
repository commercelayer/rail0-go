package rail0

import (
	"context"
	"net/url"
	"strconv"
)

// AnalyticsService exposes the merchant sales analytics rollups under /analytics.
// Every endpoint requires a JWT bound to a non-null account (a merchant session):
// the gateway answers 401 without a token and 403 for an account-less (buyer)
// session, and always scopes results to the account's own wallets as payee — a
// merchant only ever sees its own sales. Reads are aggregate (counts / SUMs), not
// payment records.
//
// Amounts are token base-unit integer strings and are only ever summed WITHIN a
// single (token, chain): different tokens/decimals are never added together, so
// per-token volume comes with its decimals for display.
type AnalyticsService struct {
	http *httpClient
}

// AnalyticsFilters are the shared query filters accepted by all three endpoints.
// All are optional; the zero value applies no filtering. From/To are ISO-8601
// timestamps (e.g. "2026-01-01T00:00:00Z"). Token is a 0x token address and, paired
// with ChainID, is what unlocks per-bucket volume in Timeseries (a single token is
// summable). Status must be one of the payment states; Mode is "authorize"/"charge".
type AnalyticsFilters struct {
	Mode    string // "authorize" | "charge"
	Status  string // a payment status (e.g. "captured")
	Token   string // token address (0x…) — scopes volume to one token
	ChainID int    // chain id; 0 = no filter
	From    string // ISO-8601: only payments created at/after this time
	To      string // ISO-8601: only payments created at/before this time
}

// apply writes the set filters onto a url.Values query.
func (f AnalyticsFilters) apply(q url.Values) {
	if f.Mode != "" {
		q.Set("mode", f.Mode)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Token != "" {
		q.Set("token", f.Token)
	}
	if f.ChainID > 0 {
		q.Set("chain_id", strconv.Itoa(f.ChainID))
	}
	if f.From != "" {
		q.Set("from", f.From)
	}
	if f.To != "" {
		q.Set("to", f.To)
	}
}

// TimeseriesParams are the Timeseries filters plus the bucket Interval.
type TimeseriesParams struct {
	AnalyticsFilters
	// Interval is the bucket width: "day" (default), "week", or "month".
	Interval string
}

// BreakdownParams are the Breakdown filters plus the required grouping dimension.
type BreakdownParams struct {
	AnalyticsFilters
	// By is the dimension to group by: "token", "chain", "mode", "status" or
	// "operation". token/chain rows carry volume; mode/status rows are counts only;
	// "operation" groups the merchant's own CONFIRMED transactions and carries
	// Transactions beside Orders. Required.
	By string
}

// AnalyticsVolume is the volume for one (token, chain). The amount fields are token
// base-unit integer strings (format with Decimals). ChainID, ChainName, Symbol and
// Decimals are nil when the contract is no longer in the gateway's catalog.
//
// Two pairs, answering different questions. Settled/Escrowed say where the money is NOW,
// from the on-chain residuals the indexer mirrors: Settled is what the payee holds net of
// refunds, Escrowed what is still held and neither captured nor returned.
// Captured/Refunded say what HAPPENED, summed from the confirmed transactions. Neither
// pair substitutes for the other — refundable = captured − refunded is one equation in two
// unknowns, and void/release zero the capturable without recording how much went back.
type AnalyticsVolume struct {
	ChainID   *int    `json:"chain_id"`
	ChainName *string `json:"chain_name"`
	Token     string  `json:"token"`
	Symbol    *string `json:"symbol"`
	Decimals  *int    `json:"decimals"`
	Orders    int     `json:"orders"`
	Gross     string  `json:"gross"`
	Settled   string  `json:"settled"`
	Escrowed  string  `json:"escrowed"`
	Captured  string  `json:"captured"`
	Refunded  string  `json:"refunded"`
}

// AnalyticsGas is what the merchant's own transactions cost on ONE chain, in that chain's
// NATIVE token — not the payment token. Spent and Wasted are wei-scale base-unit integer
// strings and Decimals is always 18, as it is for every EVM native token.
//
// Per chain and NEVER summed across chains: Base ETH and Polygon POL are different
// currencies, the same reason volume is only summed within one token. Wasted is gas an
// on-chain revert burned — money spent for nothing. A failure the node refused before the
// chain burns none, so it raises Failed without raising Wasted.
//
// Orders is the count of payments behind the figures — the denominator for the average
// cost of an order. It counts every payment in scope, INCLUDING those that produced no
// transaction and so cost nothing, since leaving them out would average only the expensive
// ones. It is nil on the by-operation cut, where one order spans several operations.
//
// Covers only the operations the merchant broadcasts: dispute/close_dispute are the
// buyer's cost on-chain, and release records no sender, so both are excluded.
type AnalyticsGas struct {
	ChainID   *int    `json:"chain_id"`
	ChainName *string `json:"chain_name"`
	Symbol    *string `json:"symbol"`
	Decimals  *int    `json:"decimals"`
	Orders    *int    `json:"orders"`
	Spent     string  `json:"spent"`
	Wasted    string  `json:"wasted"`
	Confirmed int     `json:"confirmed"`
	Failed    int     `json:"failed"`
	// ConfirmationSecs is the mean time from broadcast to on-chain confirmation for this
	// chain's confirmed transactions, weighted by their count. Nil when none confirmed —
	// not zero, which would read as instant. Per chain, because a chain that wants 60
	// confirmations and one that wants 4 are not comparable.
	ConfirmationSecs *int `json:"confirmation_secs"`
}

// AnalyticsFailure is one decoded failure code and how many of the merchant's transactions
// hit it, commonest first. FailedRate says how much fails; this says what to act on — a
// revert is a state problem (an amount above the residual, a closed window) while a
// rejection that never reached the chain is a wallet problem (no gas money, a used nonce).
// Code is the same catalogue an error body uses, or "unknown" when the raw reason decodes
// to nothing.
type AnalyticsFailure struct {
	Code         string `json:"code"`
	Transactions int    `json:"transactions"`
}

// AnalyticsGasSlice is one slice of a chain's gas: the same fields as AnalyticsGas plus the
// Key naming the slice — a payment status for GasByStatus, an operation for GasByOperation.
// Both cuts are the same rows regrouped, so a chain's slices always add back up to its
// AnalyticsGas row.
//
// A status slice is a SNAPSHOT: status moves, so an authorize's gas sits under "authorized"
// until the payment is captured and then under "captured" — the same period's rows change
// over time. An operation slice never moves.
type AnalyticsGasSlice struct {
	AnalyticsGas
	Key string `json:"key"`
}

// AnalyticsSummary is the headline KPIs. ByStatus maps each payment status to its count;
// Volume is per (token, chain); Gas is per chain, in that chain's native token, with
// GasByStatus and GasByOperation slicing exactly those totals.
//
// RefundRate/DisputeRate are fractions in [0,1] per ORDER. FailedRate is per resolved
// TRANSACTION, not per order: one order can carry several attempts, and a retried capture
// that eventually confirms is what it exists to surface.
type AnalyticsSummary struct {
	Orders         int                 `json:"orders"`
	Disputed       int                 `json:"disputed"`
	RefundRate     float64             `json:"refund_rate"`
	DisputeRate    float64             `json:"dispute_rate"`
	FailedRate     float64             `json:"failed_rate"`
	ByStatus       map[string]int      `json:"by_status"`
	Failures       []AnalyticsFailure  `json:"failures"`
	Volume         []AnalyticsVolume   `json:"volume"`
	Gas            []AnalyticsGas      `json:"gas"`
	GasByStatus    []AnalyticsGasSlice `json:"gas_by_status"`
	GasByOperation []AnalyticsGasSlice `json:"gas_by_operation"`
}

// AnalyticsBucket is one point of the order-count time series. Volume is a base-unit
// integer string only when a single token+chain is filtered, else nil.
type AnalyticsBucket struct {
	Bucket string  `json:"bucket"`
	Orders int     `json:"orders"`
	Volume *string `json:"volume"`
}

// AnalyticsRow is one breakdown row. Key is the dimension value: a string for
// token/mode/status (and a chain's name), but an integer for a chain with no known
// name — hence any. Token/ChainID/Decimals/Volume are populated only for token and
// chain breakdowns (nil for mode/status).
type AnalyticsRow struct {
	Key      any     `json:"key"`
	Orders   int     `json:"orders"`
	Token    *string `json:"token"`
	ChainID  *int    `json:"chain_id"`
	Decimals *int    `json:"decimals"`
	Volume   *string `json:"volume"`
	// Transactions is set on "operation" rows only: how often the operation RAN, which is
	// not Orders — a partial capture runs several times on one order. Nil on every other
	// dimension.
	Transactions *int `json:"transactions"`
}

// Summary returns the account's headline sales KPIs (GET /analytics/summary).
func (s *AnalyticsService) Summary(ctx context.Context, filters ...AnalyticsFilters) (*AnalyticsSummary, error) {
	q := url.Values{}
	if len(filters) > 0 {
		filters[0].apply(q)
	}
	var out AnalyticsSummary
	if err := s.http.get(ctx, withQuery("/analytics/summary", q), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Timeseries returns the account's order count per time bucket, oldest first
// (GET /analytics/timeseries). Per-bucket Volume is included only when both Token and
// ChainID are set. Interval defaults to "day" when empty.
func (s *AnalyticsService) Timeseries(ctx context.Context, params TimeseriesParams) ([]AnalyticsBucket, error) {
	q := url.Values{}
	params.AnalyticsFilters.apply(q)
	if params.Interval != "" {
		q.Set("interval", params.Interval)
	}
	var out []AnalyticsBucket
	if err := s.http.get(ctx, withQuery("/analytics/timeseries", q), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Breakdown returns the account's orders aggregated by params.By
// (GET /analytics/breakdown). By is required (token/chain/mode/status/operation).
func (s *AnalyticsService) Breakdown(ctx context.Context, params BreakdownParams) ([]AnalyticsRow, error) {
	q := url.Values{}
	params.AnalyticsFilters.apply(q)
	q.Set("by", params.By)
	var out []AnalyticsRow
	if err := s.http.get(ctx, withQuery("/analytics/breakdown", q), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// withQuery appends an encoded query string to a path when non-empty.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}
