package rail0_test

// Alignment with commercelayer/rail0-gateway#330, #331 and #332.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	rail0 "github.com/rail0/go-sdk"
)

const testPaymentID = "0xabababababababababababababababababababababababababababababababab"

// respWithHeaders builds a JSON response carrying arbitrary headers - the shape the
// gateway returns for a collection: a bare array plus pagination headers.
func respWithHeaders(status int, body any, headers map[string]string) *http.Response {
	b, _ := json.Marshal(body)
	h := http.Header{"Content-Type": []string{"application/json"}}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

// #330 - the read the list could only answer by returning every row.
func TestGetTransactionHitsTheTransactionPath(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, map[string]any{"id": "tx-1", "operation": "capture"}))
	client := newMockClient(t, mt)

	tx, err := client.Payments.GetTransaction(context.Background(), testPaymentID, "tx-1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.ID != "tx-1" {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	want := "/payments/" + testPaymentID + "/transactions/tx-1"
	if got := mt.recorded[0].URL.Path; got != want {
		t.Errorf("path: got %s, want %s", got, want)
	}
}

// #331 - the header travels only when a key is given, so the un-keyed call is
// byte-for-byte what it was before.
func TestPrepareWithKeySendsTheHeaderOnlyWhenGiven(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{"id": "tx-1"}))
	mt.push(jsonResp(201, map[string]any{"id": "tx-2"}))
	client := newMockClient(t, mt)

	if _, err := client.Payments.PrepareWithKey(context.Background(), testPaymentID, rail0.OpCapture, "k-1", rail0.PrepareRequest{Amount: "1.00"}); err != nil {
		t.Fatalf("PrepareWithKey: %v", err)
	}
	if got := mt.recorded[0].Header.Get("Idempotency-Key"); got != "k-1" {
		t.Errorf("Idempotency-Key: got %q, want k-1", got)
	}

	if _, err := client.Payments.Prepare(context.Background(), testPaymentID, rail0.OpCapture, rail0.PrepareRequest{Amount: "1.00"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := mt.recorded[1].Header.Get("Idempotency-Key"); got != "" {
		t.Errorf("Idempotency-Key must be absent without a key, got %q", got)
	}
}

// #332 - total_pages and every Link rel folded into the meta.
func TestPageMetaCarriesTotalPagesAndLinks(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(respWithHeaders(200, []map[string]any{{"id": "p1"}}, map[string]string{
		"X-Total-Count": "7",
		"X-Total-Pages": "4",
		"X-Page":        "2",
		"X-Per-Page":    "2",
		"Link": strings.Join([]string{
			`</payments?page=1&per_page=2>; rel="first"`,
			`</payments?page=1&per_page=2>; rel="prev"`,
			`</payments?page=3&per_page=2>; rel="next"`,
			`</payments?page=4&per_page=2>; rel="last"`,
		}, ", "),
	}))
	client := newMockClient(t, mt)

	page, err := client.Payments.List(context.Background())
	if err != nil {
		t.Fatalf("Payments.List: %v", err)
	}
	if page.Meta.TotalPages != 4 {
		t.Errorf("TotalPages: got %d, want 4", page.Meta.TotalPages)
	}
	if page.Meta.Links.Next != "/payments?page=3&per_page=2" {
		t.Errorf("Links.Next: got %q", page.Meta.Links.Next)
	}
	if page.Meta.Links.Last != "/payments?page=4&per_page=2" {
		t.Errorf("Links.Last: got %q", page.Meta.Links.Last)
	}
}

// An empty collection sends no Link header at all, so every rel stays empty
// rather than pointing nowhere.
func TestPageMetaLinksAreEmptyWithoutTheHeader(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(respWithHeaders(200, []map[string]any{}, map[string]string{
		"X-Total-Count": "0",
		"X-Total-Pages": "0",
	}))
	client := newMockClient(t, mt)

	page, err := client.Payments.List(context.Background())
	if err != nil {
		t.Fatalf("Payments.List: %v", err)
	}
	if page.Meta.TotalPages != 0 {
		t.Errorf("TotalPages: got %d, want 0", page.Meta.TotalPages)
	}
	if page.Meta.Links != (rail0.PageLinks{}) {
		t.Errorf("Links must be the zero value, got %+v", page.Meta.Links)
	}
}

// The dispute pair takes the key too, so no prepare quietly ignores the header.
func TestDisputePrepareWithKeySendsTheHeader(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(201, map[string]any{"id": "tx-3"}))
	client := newMockClient(t, mt)

	if _, err := client.Payments.DisputePrepareWithKey(context.Background(), testPaymentID, "k-3"); err != nil {
		t.Fatalf("DisputePrepareWithKey: %v", err)
	}
	if got := mt.recorded[0].Header.Get("Idempotency-Key"); got != "k-3" {
		t.Errorf("Idempotency-Key: got %q, want k-3", got)
	}
	if got := mt.recorded[0].URL.Path; got != "/payments/"+testPaymentID+"/dispute/prepare" {
		t.Errorf("path: %s", got)
	}
}
