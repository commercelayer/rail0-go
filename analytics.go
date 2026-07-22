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
	// By is the dimension to group by: "token", "chain", "mode", or "status".
	// token/chain rows carry volume; mode/status rows are counts only. Required.
	By string
}

// AnalyticsVolume is gross/captured/refunded volume for one (token, chain). The
// amount fields are token base-unit integer strings (format with Decimals). ChainID,
// ChainName, Symbol and Decimals are nil when the contract is no longer in the
// gateway's catalog.
type AnalyticsVolume struct {
	ChainID   *int    `json:"chain_id"`
	ChainName *string `json:"chain_name"`
	Token     string  `json:"token"`
	Symbol    *string `json:"symbol"`
	Decimals  *int    `json:"decimals"`
	Orders    int     `json:"orders"`
	Gross     string  `json:"gross"`
	Captured  string  `json:"captured"`
	Refunded  string  `json:"refunded"`
}

// AnalyticsSummary is the headline KPIs. ByStatus maps each payment status to its
// count; Volume is per (token, chain). RefundRate/DisputeRate are fractions in [0,1].
type AnalyticsSummary struct {
	Orders      int               `json:"orders"`
	Disputed    int               `json:"disputed"`
	RefundRate  float64           `json:"refund_rate"`
	DisputeRate float64           `json:"dispute_rate"`
	ByStatus    map[string]int    `json:"by_status"`
	Volume      []AnalyticsVolume `json:"volume"`
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
// (GET /analytics/breakdown). By is required (one of token/chain/mode/status).
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
