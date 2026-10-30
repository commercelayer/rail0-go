package rail0

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The cursor-paged lists (GET /payments, GET /payouts) answer a next page by the
// x-next-cursor header, and the Link next carries the same cursor as `after`. A
// walk passes Meta.NextCursor back as After until it comes back empty.
func TestCursorPaging(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, q)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case q.Get("page") != "": // offset paging: unchanged, no cursor
			w.Header().Set("X-Page", q.Get("page"))
			w.Header().Set("X-Total-Count", "30")
			w.Header().Set("Link", `<`+r.URL.Path+`?page=3&per_page=10>; rel="next"`)
		case q.Get("after") == "": // first cursor page: header
			w.Header().Set("X-Next-Cursor", "c1")
			w.Header().Set("Link", `<`+r.URL.Path+`?after=c1&per_page=10>; rel="next"`)
		case q.Get("after") == "c1": // second: header dropped, Link only
			w.Header().Set("Link", `<`+r.URL.Path+`?after=c%2B2&per_page=10>; rel="next"`)
		default: // last page: neither
		}
		_, _ = w.Write([]byte(`[{"id":"x"}]`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	ctx := context.Background()

	var payoutCursors []string
	after := ""
	for i := 0; i < 5; i++ {
		page, err := c.Payouts.List(ctx, ListPayoutsParams{PerPage: 10, After: after})
		if err != nil {
			t.Fatal(err)
		}
		payoutCursors = append(payoutCursors, page.Meta.NextCursor)
		if page.Meta.NextCursor == "" {
			break
		}
		after = page.Meta.NextCursor
	}
	if got := len(payoutCursors); got != 3 || payoutCursors[0] != "c1" || payoutCursors[1] != "c+2" || payoutCursors[2] != "" {
		t.Errorf("payout cursors = %q, want [c1 c+2 \"\"]", payoutCursors)
	}
	if queries[0].Has("after") || queries[1].Get("after") != "c1" || queries[2].Get("after") != "c+2" {
		t.Errorf("payout queries = %v", queries)
	}

	page, err := c.Payments.List(ctx, ListPaymentsParams{After: "c1", PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if q := queries[len(queries)-1]; q.Get("after") != "c1" || q.Get("per_page") != "10" {
		t.Errorf("payments query = %v", q)
	}
	if page.Meta.NextCursor != "c+2" {
		t.Errorf("payments NextCursor = %q, want c+2 (from the Link next)", page.Meta.NextCursor)
	}

	// Page numbers keep working exactly as before: no after on the wire, no cursor read
	// from an offset next link.
	for _, list := range []func() (PageMeta, error){
		func() (PageMeta, error) {
			p, err := c.Payments.List(ctx, ListPaymentsParams{Page: 2, PerPage: 10})
			if err != nil {
				return PageMeta{}, err
			}
			return p.Meta, nil
		},
		func() (PageMeta, error) {
			p, err := c.Payouts.List(ctx, ListPayoutsParams{Page: 2, PerPage: 10})
			if err != nil {
				return PageMeta{}, err
			}
			return p.Meta, nil
		},
	} {
		meta, err := list()
		if err != nil {
			t.Fatal(err)
		}
		if q := queries[len(queries)-1]; q.Has("after") || q.Get("page") != "2" {
			t.Errorf("offset query = %v", q)
		}
		if meta.NextCursor != "" || meta.Page != 2 || meta.Total != 30 || meta.Links.Next == "" {
			t.Errorf("offset meta = %+v", meta)
		}
	}
}
