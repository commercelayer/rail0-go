package rail0

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pathf escapes every interpolated value into ONE segment: nothing a caller passes can
// add a segment, start a query or a fragment, or smuggle a pre-encoded sequence.
func TestPathfEscapesEachSegment(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want string
	}{
		{"uuid unchanged", "019f8a3d-b781-7b00-8b75-8427f7e591d2", "/payments/019f8a3d-b781-7b00-8b75-8427f7e591d2"},
		{"0x hex unchanged", "0xabc123", "/payments/0xabc123"},
		{"slash", "a/b", "/payments/a%2Fb"},
		{"traversal inside a segment", "x/../../webhooks/y", "/payments/x%2F..%2F..%2Fwebhooks%2Fy"},
		{"query", "id?admin=1", "/payments/id%3Fadmin=1"},
		{"fragment", "id#frag", "/payments/id%23frag"},
		{"space", "a b", "/payments/a%20b"},
		{"percent is re-escaped", "a%2Fb", "/payments/a%252Fb"},
		{"unicode", "pagamento-é", "/payments/pagamento-%C3%A9"},
		{"dot segment passes through (rejected later)", "..", "/payments/.."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pathf("/payments/%s", c.seg); got != c.want {
				t.Errorf("pathf(%q) = %q, want %q", c.seg, got, c.want)
			}
		})
	}
	if got := pathf("/webhooks/%s/event_callbacks/%s/redeliver", "w/1", "c?2"); got != "/webhooks/w%2F1/event_callbacks/c%3F2/redeliver" {
		t.Errorf("multi-segment pathf = %q", got)
	}
}

// A segment PathEscape cannot neutralise — empty, "." or ".." — is refused before a
// request is built; ordinary paths, with or without a query, pass.
func TestCheckPathSegments(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"/payments", false},
		{"/payments/0xabc/transactions?page=2&q=a/../b", false}, // the query is not a path
		{"/payments/x%2F..%2Fy", false},
		{"/payments/..", true},
		{"/payments/./capture", true},
		{"/payments/", true},
		{"/payments//transactions", true},
		{"/accounts/../webhooks", true},
	}
	for _, c := range cases {
		err := checkPathSegments(c.path)
		if (err != nil) != c.wantErr {
			t.Errorf("checkPathSegments(%q) error = %v, wantErr %v", c.path, err, c.wantErr)
		}
	}
}

// End to end: the path the gateway receives keeps each hostile id inside its own
// segment, for a representative method of every resource that takes ids.
func TestResourceMethodsEscapeIDsOnTheWire(t *testing.T) {
	const hostile = "a/../b?c=d#e f-é"
	const esc = "a%2F..%2Fb%3Fc=d%23e%20f-%C3%A9"

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
		want string
	}{
		{"Payments.Get", func() error { _, err := c.Payments.Get(ctx, hostile); return err }, "/payments/" + esc},
		{"Payments.GetTransaction", func() error { _, err := c.Payments.GetTransaction(ctx, "p1", hostile); return err }, "/payments/p1/transactions/" + esc},
		{"Payments.Redrive", func() error { _, err := c.Payments.Redrive(ctx, hostile, hostile); return err }, "/payments/" + esc + "/transactions/" + esc + "/redrive"},
		{"Payments.Prepare (op)", func() error { _, err := c.Payments.Prepare(ctx, "p1", "capture/../void"); return err }, "/payments/p1/capture%2F..%2Fvoid/prepare"},
		{"Payments.Dispute", func() error { _, err := c.Payments.Dispute(ctx, hostile, SubmitTransactionRequest{}); return err }, "/payments/" + esc + "/dispute"},
		{"Accounts.Update", func() error { _, err := c.Accounts.Update(ctx, hostile, UpdateAccountRequest{}); return err }, "/accounts/" + esc},
		{"Wallets.Get", func() error { _, err := c.Wallets.Get(ctx, hostile, hostile); return err }, "/accounts/" + esc + "/wallets/" + esc},
		{"Wallets.EnableToken", func() error { _, err := c.Wallets.EnableToken(ctx, "a1", "w1", hostile); return err }, "/accounts/a1/wallets/w1/tokens/" + esc + "/enable"},
		{"Wallets.RemoveToken", func() error { return c.Wallets.RemoveToken(ctx, "a1", "w1", hostile) }, "/accounts/a1/wallets/w1/tokens/" + esc},
		{"Webhooks.Redeliver", func() error { _, err := c.Webhooks.Redeliver(ctx, hostile, hostile); return err }, "/webhooks/" + esc + "/event_callbacks/" + esc + "/redeliver"},
		{"Webhooks.Delete", func() error { return c.Webhooks.Delete(ctx, hostile) }, "/webhooks/" + esc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotQuery = "", ""
			if err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotPath != tc.want {
				t.Errorf("%s path = %q, want %q", tc.name, gotPath, tc.want)
			}
			if gotQuery != "" {
				t.Errorf("%s leaked a query string %q from an id", tc.name, gotQuery)
			}
		})
	}
}

// An id that is empty or exactly "." / ".." fails before anything is sent: the request
// would otherwise land on the list endpoint or on the parent resource.
func TestDotAndEmptyIDsAreRejectedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	ctx := context.Background()

	for _, id := range []string{"..", ".", ""} {
		if _, err := c.Payments.Get(ctx, id); err == nil || !strings.Contains(err.Error(), "invalid request path") {
			t.Errorf("Payments.Get(%q) error = %v, want an invalid request path error", id, err)
		}
		if err := c.Webhooks.Delete(ctx, id); err == nil {
			t.Errorf("Webhooks.Delete(%q) succeeded, want an error", id)
		}
	}
	if hits != 0 {
		t.Errorf("gateway received %d request(s), want none", hits)
	}
}
