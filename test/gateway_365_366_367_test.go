package rail0_test

// Alignment with commercelayer/rail0-gateway#365 (settlement time per chain),
// #366 (payments.authorization_expiring) and #367 (payment decimals, in_flight,
// multi-value status filter).

import (
	"context"
	"encoding/json"
	"testing"

	rail0 "github.com/commercelayer/rail0-go"
)

// #365 - measured percentiles decode as values; below the minimum sample they are
// null, and the pointers keep that apart from a real 0.
func TestChainsListDecodesSettlement(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, []map[string]any{
		{"chain_id": 84532, "settlement": map[string]any{"p50_seconds": 11, "p90_seconds": 19, "sample_size": 20, "window_days": 7}},
		{"chain_id": 11155111, "settlement": map[string]any{"p50_seconds": nil, "p90_seconds": nil, "sample_size": 3, "window_days": 7}},
	}))
	client := newMockClient(t, mt)

	chains, err := client.Chains.List(context.Background())
	if err != nil {
		t.Fatalf("Chains.List: %v", err)
	}
	if len(chains) != 2 {
		t.Fatalf("want 2 chains, got %d", len(chains))
	}
	measured := chains[0].Settlement
	if measured.P50Seconds == nil || *measured.P50Seconds != 11 || measured.P90Seconds == nil || *measured.P90Seconds != 19 {
		t.Errorf("measured percentiles: got %+v", measured)
	}
	if measured.SampleSize != 20 || measured.WindowDays != 7 {
		t.Errorf("measured sample/window: got %+v", measured)
	}
	sparse := chains[1].Settlement
	if sparse.P50Seconds != nil || sparse.P90Seconds != nil {
		t.Errorf("below the minimum sample the percentiles must be nil, got %+v", sparse)
	}
	if sparse.SampleSize != 3 || sparse.WindowDays != 7 {
		t.Errorf("sparse sample/window: got %+v", sparse)
	}
}

// #367 G4/G7 - decimals and in_flight on list rows.
func TestPaymentsListDecodesDecimalsAndInFlight(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []map[string]any{
		{"rail0_id": mockPaymentID, "amount": "1500000", "decimals": 6, "in_flight": true},
		{"rail0_id": mockPaymentID, "amount": "1500000", "decimals": nil, "in_flight": false},
	}, "2"))
	client := newMockClient(t, mt)

	res, err := client.Payments.List(context.Background())
	if err != nil {
		t.Fatalf("Payments.List: %v", err)
	}
	if len(res.Data) != 2 {
		t.Fatalf("want 2 payments, got %d", len(res.Data))
	}
	p := res.Data[0]
	if p.Decimals == nil || *p.Decimals != 6 {
		t.Fatalf("Decimals: got %v, want 6", p.Decimals)
	}
	if !p.InFlight {
		t.Errorf("InFlight: got false, want true")
	}
	// Decimals is what FormatAmount needs - no Tokens.List join.
	if got := rail0.FormatAmount(p.Amount, *p.Decimals); got != "1.5" {
		t.Errorf("FormatAmount with payment decimals: got %q, want 1.5", got)
	}
	// Unresolvable token: nil, not a misleading 0.
	if res.Data[1].Decimals != nil {
		t.Errorf("null decimals must decode to nil, got %v", *res.Data[1].Decimals)
	}
	if res.Data[1].InFlight {
		t.Errorf("InFlight: got true, want false")
	}
}

// #367 - the payment embedded in GET /disputes carries both fields too.
func TestDisputesListEmbeddedPaymentCarriesDecimalsAndInFlight(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []map[string]any{
		{"id": "d1", "status": "open", "payment": map[string]any{"rail0_id": mockPaymentID, "decimals": 18, "in_flight": true}},
	}, "1"))
	client := newMockClient(t, mt)

	res, err := client.Disputes.List(context.Background())
	if err != nil {
		t.Fatalf("Disputes.List: %v", err)
	}
	pay := res.Data[0].Payment
	if pay == nil || pay.Decimals == nil || *pay.Decimals != 18 || !pay.InFlight {
		t.Errorf("embedded payment: got %+v", pay)
	}
}

// in_flight has no omitempty: a false must survive re-encoding, like disputed.
func TestPaymentInFlightFalseSurvivesReencoding(t *testing.T) {
	b, err := json.Marshal(rail0.Payment{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if v, ok := m["in_flight"]; !ok || v != false {
		t.Errorf("in_flight: got %v (present=%v), want false", v, ok)
	}
	if _, ok := m["decimals"]; ok {
		t.Errorf("nil decimals must be omitted, got %s", b)
	}
}

// #367 G6 - Status and Statuses serialise to ONE comma-separated status value.
func TestPaymentsListStatusFilter(t *testing.T) {
	cases := []struct {
		name   string
		params rail0.ListPaymentsParams
		want   string // "" = the parameter must be absent
	}{
		{"single Status unchanged", rail0.ListPaymentsParams{Status: "authorized"}, "authorized"},
		{"Statuses joined", rail0.ListPaymentsParams{Statuses: []string{"authorized", "expired"}}, "authorized,expired"},
		{"Status first, then Statuses", rail0.ListPaymentsParams{Status: "captured", Statuses: []string{"authorized", "expired"}}, "captured,authorized,expired"},
		{"duplicates and blanks dropped", rail0.ListPaymentsParams{Status: "authorized", Statuses: []string{"", "authorized", " expired "}}, "authorized,expired"},
		{"all blank omits the parameter", rail0.ListPaymentsParams{Statuses: []string{"", " "}}, ""},
		{"nothing set omits the parameter", rail0.ListPaymentsParams{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mt := &mockTransport{t: t}
			mt.push(listResp(200, []map[string]any{}, "0"))
			client := newMockClient(t, mt)

			if _, err := client.Payments.List(context.Background(), tc.params); err != nil {
				t.Fatalf("Payments.List: %v", err)
			}
			q := mt.recorded[0].URL.Query()
			if tc.want == "" {
				if _, ok := q["status"]; ok {
					t.Errorf("status must be absent, got query %s", mt.recorded[0].URL.RawQuery)
				}
				return
			}
			if got := q["status"]; len(got) != 1 || got[0] != tc.want {
				t.Errorf("status: got %v, want [%s] (query %s)", got, tc.want, mt.recorded[0].URL.RawQuery)
			}
		})
	}
}

// #367 G6 - the transactions list accepts the same multi-value filter.
func TestPaymentTransactionsStatusFilter(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []map[string]any{}, "0"))
	client := newMockClient(t, mt)

	_, err := client.Payments.Transactions(context.Background(), testPaymentID, rail0.ListTransactionsParams{
		Statuses: []string{"submitting", "submitted"},
	})
	if err != nil {
		t.Fatalf("Payments.Transactions: %v", err)
	}
	if got := mt.recorded[0].URL.Query()["status"]; len(got) != 1 || got[0] != "submitting,submitted" {
		t.Errorf("status: got %v, want [submitting,submitted]", got)
	}
}

// #366 - the new topic is subscribable like any other.
func TestAuthorizationExpiringTopic(t *testing.T) {
	if rail0.TopicPaymentsAuthorizationExpiring != "payments.authorization_expiring" {
		t.Fatalf("topic: got %q", rail0.TopicPaymentsAuthorizationExpiring)
	}
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{"id": "wh-1", "topics": []string{rail0.TopicPaymentsAuthorizationExpiring}}))
	client := newMockClient(t, mt)

	if _, err := client.Webhooks.Create(context.Background(), rail0.CreateWebhookRequest{
		Name: "expiring", CallbackURL: "https://example.com/hook",
		Topics: []string{rail0.TopicPaymentsAuthorizationExpiring},
	}); err != nil {
		t.Fatalf("Webhooks.Create: %v", err)
	}
	var body struct {
		Topics []string `json:"topics"`
	}
	if err := json.NewDecoder(mt.recorded[0].Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Topics) != 1 || body.Topics[0] != "payments.authorization_expiring" {
		t.Errorf("topics sent: got %v", body.Topics)
	}
}

// #366 - every delivery's payment object carries capturable_amount and
// authorization_expiry; it decodes straight into rail0.Payment.
func TestWebhookPaymentPayloadDecodesIntoPayment(t *testing.T) {
	raw := []byte(`{"id":"evt-1","topic":"payments.authorization_expiring","emitted_at":1770000000,
		"payment":{"rail0_id":"` + mockPaymentID + `","status":"authorized","capturable_amount":"2500000",
		"authorization_expiry":1770086400,"decimals":6},"transaction":null}`)
	var evt struct {
		Payment rail0.Payment `json:"payment"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if evt.Payment.CapturableAmount != "2500000" || evt.Payment.AuthorizationExpiry != 1770086400 {
		t.Errorf("payment: got %+v", evt.Payment)
	}
}
