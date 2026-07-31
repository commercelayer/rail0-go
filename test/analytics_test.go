package rail0_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	rail0 "github.com/rail0/go-sdk"
)

// ================================================================
//  Analytics
// ================================================================

func TestAnalytics_Summary(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{
		"orders":       10,
		"disputed":     1,
		"refund_rate":  0.2,
		"dispute_rate": 0.1,
		"by_status":    map[string]any{"captured": 6, "refunded": 2},
		"volume": []any{map[string]any{
			"chain_id": 8453, "chain_name": "Base", "token": "0xtok",
			"symbol": "USDC", "decimals": 6, "orders": 8,
			"gross": "80000000", "captured": "60000000", "refunded": "20000000",
		}},
	}))
	client := newMockClient(t, mt)

	res, err := client.Analytics.Summary(context.Background(), rail0.AnalyticsFilters{
		Mode: "charge", Status: "captured", Token: "0xtok", ChainID: 8453,
		From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.Method != http.MethodGet || req.URL.Path != "/analytics/summary" {
		t.Errorf("request: got %s %s", req.Method, req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("mode") != "charge" || q.Get("status") != "captured" || q.Get("token") != "0xtok" ||
		q.Get("chain_id") != "8453" || q.Get("from") != "2026-01-01T00:00:00Z" || q.Get("to") != "2026-02-01T00:00:00Z" {
		t.Errorf("query missing filters: %s", req.URL.RawQuery)
	}
	if res.Orders != 10 || res.Disputed != 1 || res.RefundRate != 0.2 || res.DisputeRate != 0.1 {
		t.Errorf("headline KPIs: %+v", res)
	}
	if res.ByStatus["captured"] != 6 {
		t.Errorf("by_status: %+v", res.ByStatus)
	}
	if len(res.Volume) != 1 {
		t.Fatalf("volume: %+v", res.Volume)
	}
	v := res.Volume[0]
	if v.ChainID == nil || *v.ChainID != 8453 || v.Symbol == nil || *v.Symbol != "USDC" ||
		v.Decimals == nil || *v.Decimals != 6 || v.Gross != "80000000" || v.Captured != "60000000" {
		t.Errorf("volume row: %+v", v)
	}
}

func TestAnalytics_Timeseries(t *testing.T) {
	mt := &mockTransport{t: t}
	vol := "5000000"
	mt.push(jsonResp(200, []any{
		map[string]any{"bucket": "2026-01-01T00:00:00Z", "orders": 3, "volume": vol},
		map[string]any{"bucket": "2026-01-02T00:00:00Z", "orders": 0, "volume": nil},
	}))
	client := newMockClient(t, mt)

	res, err := client.Analytics.Timeseries(context.Background(), rail0.TimeseriesParams{
		AnalyticsFilters: rail0.AnalyticsFilters{Token: "0xtok", ChainID: 8453},
		Interval:         "week",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.URL.Path != "/analytics/timeseries" {
		t.Errorf("path: got %s", req.URL.Path)
	}
	if q := req.URL.Query(); q.Get("interval") != "week" || q.Get("token") != "0xtok" || q.Get("chain_id") != "8453" {
		t.Errorf("query: got %s", req.URL.RawQuery)
	}
	if len(res) != 2 {
		t.Fatalf("buckets: %+v", res)
	}
	if res[0].Orders != 3 || res[0].Volume == nil || *res[0].Volume != "5000000" {
		t.Errorf("bucket[0]: %+v", res[0])
	}
	// A bucket with no single-token filter carries a null volume.
	if res[1].Volume != nil {
		t.Errorf("bucket[1].Volume should be nil, got %v", *res[1].Volume)
	}
}

// Interval defaults to the server's when empty — the SDK omits the param.
func TestAnalytics_Timeseries_OmitsEmptyInterval(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []any{}))
	client := newMockClient(t, mt)

	if _, err := client.Analytics.Timeseries(context.Background(), rail0.TimeseriesParams{}); err != nil {
		t.Fatal(err)
	}
	if mt.recorded[0].URL.Query().Has("interval") {
		t.Errorf("interval should be omitted when empty: %s", mt.recorded[0].URL.RawQuery)
	}
}

func TestAnalytics_Breakdown(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []any{
		map[string]any{"key": "USDC", "orders": 8, "token": "0xtok", "chain_id": 8453, "decimals": 6, "volume": "80000000"},
	}))
	client := newMockClient(t, mt)

	res, err := client.Analytics.Breakdown(context.Background(), rail0.BreakdownParams{
		AnalyticsFilters: rail0.AnalyticsFilters{Mode: "charge"},
		By:               "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := mt.recorded[0]
	if req.URL.Path != "/analytics/breakdown" {
		t.Errorf("path: got %s", req.URL.Path)
	}
	if q := req.URL.Query(); q.Get("by") != "token" || q.Get("mode") != "charge" {
		t.Errorf("query: got %s", req.URL.RawQuery)
	}
	if len(res) != 1 {
		t.Fatalf("rows: %+v", res)
	}
	r := res[0]
	if r.Key != "USDC" || r.Orders != 8 || r.Token == nil || *r.Token != "0xtok" ||
		r.ChainID == nil || *r.ChainID != 8453 || r.Volume == nil || *r.Volume != "80000000" {
		t.Errorf("row: %+v", r)
	}
}

// mode/status breakdown rows leave token/chain/volume null.
func TestAnalytics_Breakdown_StatusRowsHaveNoVolume(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []any{
		map[string]any{"key": "captured", "orders": 6},
	}))
	client := newMockClient(t, mt)

	res, err := client.Analytics.Breakdown(context.Background(), rail0.BreakdownParams{By: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Key != "captured" || res[0].Orders != 6 {
		t.Fatalf("row: %+v", res)
	}
	if res[0].Token != nil || res[0].ChainID != nil || res[0].Volume != nil {
		t.Errorf("status row should have null token/chain/volume: %+v", res[0])
	}
}

// ================================================================
//  Token lifecycle
// ================================================================

// The Token option seeds Authorization on every request.
func TestToken_OptionSeedsAuthorizationHeader(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) { o.Token = "seed-jwt" })

	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[0].Header.Get("Authorization"); got != "Bearer seed-jwt" {
		t.Errorf("Authorization: got %q, want Bearer seed-jwt", got)
	}
}

// SetToken applies to the SAME client on subsequent requests, and clears with "".
func TestToken_SetTokenMutatesClient(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp)) // unauthenticated
	mt.push(jsonResp(200, mockCreatePaymentResp)) // after SetToken
	mt.push(jsonResp(200, mockCreatePaymentResp)) // after clear
	client := newMockClient(t, mt)

	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[0].Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization before SetToken, got %q", got)
	}

	client.SetToken("late-jwt")
	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[1].Header.Get("Authorization"); got != "Bearer late-jwt" {
		t.Errorf("Authorization after SetToken: got %q", got)
	}

	client.SetToken("")
	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[2].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization after clear: got %q, want empty", got)
	}
}

// Auth.Verify stashes the returned JWT on the shared client automatically, so the
// next call on the same client is authenticated with no manual SetToken.
func TestToken_VerifyAuthenticatesSameClient(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{
		"token": "verified-jwt", "address": "0xabc", "account_id": "acc_1",
		"expires_at": "2026-01-01T00:00:00Z",
	}))
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt)

	if _, err := client.Auth.Verify(context.Background(), rail0.AuthRequest{Message: "m", Signature: "0xsig"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if got := mt.recorded[1].Header.Get("Authorization"); got != "Bearer verified-jwt" {
		t.Errorf("second request Authorization: got %q, want Bearer verified-jwt", got)
	}
}

// ================================================================
//  Retry-After (429)
// ================================================================

// A 429 surfaces the Retry-After header (seconds form) on the APIError.
func TestRetryAfter_ParsedOntoAPIError(t *testing.T) {
	mt := &mockTransport{t: t}
	resp := jsonResp(429, map[string]any{"code": "RateLimited", "message": "slow down"})
	resp.Header.Set("Retry-After", "5")
	mt.push(resp)
	client := newMockClient(t, mt)

	_, err := client.Payments.Create(context.Background(), mockCreateReq)
	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 429 {
		t.Errorf("Status: got %d, want 429", apiErr.Status)
	}
	if apiErr.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter: got %s, want 5s", apiErr.RetryAfter)
	}
}

// Absent header → RetryAfter is zero (and the error is still surfaced).
func TestRetryAfter_ZeroWhenHeaderAbsent(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(429, map[string]any{"code": "RateLimited"}))
	client := newMockClient(t, mt)

	_, err := client.Payments.Create(context.Background(), mockCreateReq)
	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 0 {
		t.Fatalf("expected zero RetryAfter, got %v", err)
	}
}

// With RetryOn429, a 429 is retried (drawing on MaxRetries) and can then succeed.
func TestRetryAfter_RetriesWhenOptedIn(t *testing.T) {
	mt := &mockTransport{t: t}
	resp := jsonResp(429, map[string]any{"code": "RateLimited"})
	mt.push(resp)
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.RetryOn429 = true
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	res, err := client.Payments.Create(context.Background(), mockCreateReq)
	if err != nil {
		t.Fatalf("expected success after a 429 retry, got: %v", err)
	}
	if res.Rail0Id != mockPaymentID {
		t.Errorf("Rail0Id: got %s", res.Rail0Id)
	}
	if mt.i != 2 {
		t.Errorf("expected 2 attempts (429 then 200), got %d", mt.i)
	}
}

// A Retry-After of "0" — like an absent or unparseable one — means "not supplied",
// so the exponential backoff must still be applied. Storing that 0 as the delay
// override made the retry immediate (time.After(0)), turning the whole MaxRetries
// budget into a burst against the limiter that just asked us to slow down.
// The delay is measured, since only the wall clock distinguishes the two.
func TestRetryAfter_ZeroHeaderStillBacksOff(t *testing.T) {
	mt := &mockTransport{t: t}
	resp := jsonResp(429, map[string]any{"code": "RateLimited"})
	resp.Header.Set("Retry-After", "0")
	mt.push(resp)
	mt.push(jsonResp(200, mockCreatePaymentResp))
	backoff := 60 * time.Millisecond
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.RetryOn429 = true
		o.MaxRetries = 2
		o.RetryDelay = backoff
	})

	start := time.Now()
	if _, err := client.Payments.Create(context.Background(), mockCreateReq); err != nil {
		t.Fatalf("expected success after a 429 retry, got: %v", err)
	}
	elapsed := time.Since(start)

	if mt.i != 2 {
		t.Errorf("expected 2 attempts (429 then 200), got %d", mt.i)
	}
	// Attempt 2 waits RetryDelay * 2^0; a lower bound only, so a slow machine can't
	// make this flaky.
	if elapsed < backoff {
		t.Errorf("expected the exponential backoff to be applied (>= %s), waited only %s", backoff, elapsed)
	}
}

// Without RetryOn429, a 429 is NOT retried even with a retry budget.
func TestRetryAfter_NotRetriedByDefault(t *testing.T) {
	mt := &mockTransport{t: t}
	resp := jsonResp(429, map[string]any{"code": "RateLimited"})
	resp.Header.Set("Retry-After", "1")
	mt.push(resp)
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	_, err := client.Payments.Create(context.Background(), mockCreateReq)
	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if mt.i != 1 {
		t.Errorf("429 must not be retried without RetryOn429; got %d attempts", mt.i)
	}
}
