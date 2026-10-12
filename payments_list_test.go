package rail0

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The operator filters ride on the same List call as the wallet's own: scope=all is
// what switches the gateway to the cross-account list, and account_id / has_error /
// expiring_within only mean something there. Each must reach the wire exactly once,
// with has_error a tri-state (nil sends nothing, false sends "false") and the zero
// ExpiringWithin omitted rather than sent as 0, which the gateway would refuse (400).
func TestPaymentsListSendsTheOperatorScopeFilters(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})

	noErrors := false
	if _, err := c.Payments.List(context.Background(), ListPaymentsParams{
		Scope:          "all",
		AccountID:      "019f8a3d-b781-7b00-8b75-8427f7e591d2",
		HasError:       &noErrors,
		ExpiringWithin: 3600,
		Status:         "authorized",
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	want := url.Values{
		"scope":           {"all"},
		"account_id":      {"019f8a3d-b781-7b00-8b75-8427f7e591d2"},
		"has_error":       {"false"},
		"expiring_within": {"3600"},
		"status":          {"authorized"},
	}
	for k, v := range want {
		if got.Get(k) != v[0] {
			t.Errorf("query %s = %q, want %q (query %v)", k, got.Get(k), v[0], got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("query = %v, want exactly %v", got, want)
	}

	// Default scope: nothing operator-flavoured on the wire, so a merchant's call is
	// byte-identical to what it was before these fields existed.
	if _, err := c.Payments.List(context.Background(), ListPaymentsParams{Status: "authorized"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, absent := range []string{"scope", "account_id", "has_error", "expiring_within"} {
		if _, present := got[absent]; present {
			t.Errorf("query carries %s without Scope set: %v", absent, got)
		}
	}
}
