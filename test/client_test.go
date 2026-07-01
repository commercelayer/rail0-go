// Package rail0_test contains unit tests for the RAIL0 Go SDK.
//
// Tests in this file cover the HTTP client layer: correct URL routing, request
// body serialisation, error decoding, retry logic, and logger callbacks.
// All network I/O is intercepted via a mock http.RoundTripper — no real server
// is started (equivalent to vi.spyOn(globalThis, 'fetch') in the TypeScript tests).
package rail0_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	rail0 "github.com/rail0/go-sdk"
)

// ================================================================
//  Mock transport — replaces the fetch spy used in the TS tests
// ================================================================

type mockCall struct {
	resp *http.Response
	err  error
}

type mockTransport struct {
	t        *testing.T
	queue    []mockCall
	i        int
	recorded []*http.Request
}

func (m *mockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Clone request before body is consumed so tests can inspect it later.
	clone := r.Clone(context.Background())
	if r.Body != nil {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r.Body)
		clone.Body = io.NopCloser(&buf)
	}
	m.recorded = append(m.recorded, clone)

	if m.i >= len(m.queue) {
		m.t.Fatalf("unexpected request #%d: %s %s", m.i+1, r.Method, r.URL)
	}
	c := m.queue[m.i]
	m.i++
	return c.resp, c.err
}

func (m *mockTransport) push(resp *http.Response) { m.queue = append(m.queue, mockCall{resp: resp}) }
func (m *mockTransport) fail(err error)           { m.queue = append(m.queue, mockCall{err: err}) }

func jsonResp(status int, body any) *http.Response {
	b, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

// listResp builds a collection response as the gateway sends it: a bare JSON
// array body plus pagination headers (no {data,meta} envelope). `total` is the
// X-Total-Count value.
func listResp(status int, body any, total string) *http.Response {
	r := jsonResp(status, body)
	r.Header.Set("X-Total-Count", total)
	r.Header.Set("X-Page", "1")
	r.Header.Set("X-Per-Page", "20")
	return r
}

// newMockClient wires a fresh Client with the mock transport and zero retry delay.
func newMockClient(t *testing.T, mt *mockTransport, extra ...func(*rail0.ClientOptions)) *rail0.Client {
	t.Helper()
	opts := rail0.ClientOptions{
		BaseURL:   "http://test.invalid",
		Transport: mt,
	}
	for _, f := range extra {
		f(&opts)
	}
	return rail0.NewClient(opts)
}

// ================================================================
//  Shared fixtures
// ================================================================

const mockPaymentID = "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

var mockCreateReq = rail0.CreatePaymentRequest{
	ChainID: 8453,
	Mode:    "authorize",
	Amount:  "10.50",
	Token:   "0x3333333333333333333333333333333333333333",
	Payer:   "0x1111111111111111111111111111111111111111",
	Payee:   "0x2222222222222222222222222222222222222222",
}

var mockSig = struct {
	V    int
	R, S rail0.Bytes32
}{
	V: 27,
	R: "0x1111111111111111111111111111111111111111111111111111111111111111",
	S: "0x2222222222222222222222222222222222222222222222222222222222222222",
}

var mockCreatePaymentResp = map[string]any{
	"id":                   "pay_123",
	"contract_id":          "con_123",
	"rail0_id":             mockPaymentID,
	"status":               "created",
	"mode":                 "authorize",
	"amount":               "10500000",
	"config_hash":          "0xabababababababababababababababababababababababababababababababababab",
	"payer":                "0x1111111111111111111111111111111111111111",
	"payee":                "0x2222222222222222222222222222222222222222",
	"token":                "0x3333333333333333333333333333333333333333",
	"authorization_expiry": 9999999999,
	"refund_expiry":        9999999999,
	"chain_id":             8453,
	"created_at":           "2026-06-30T12:00:00Z",
	"signed_at":            "2026-06-30T12:05:00Z",
}

var mockTxResp = map[string]any{
	"id":         "tx_123",
	"payment_id": "pay_123",
	"operation":  "authorize",
	"status":     "pending",
}

var mockSubmittedTxResp = map[string]any{
	"id":         "tx_123",
	"payment_id": "pay_123",
	"operation":  "authorize",
	"status":     "submitting",
}

// mockSignedPaymentResp mirrors what PUT /payments/{id}/sign now returns: the
// full Payment record moved to the "signed" status with signed_at populated.
var mockSignedPaymentResp = map[string]any{
	"id":          "pay_123",
	"contract_id": "con_123",
	"rail0_id":    mockPaymentID,
	"status":      "signed",
	"mode":        "authorize",
	"amount":      "10500000",
	"config_hash": "0xabababababababababababababababababababababababababababababababababab",
	"payer":       "0x1111111111111111111111111111111111111111",
	"payee":       "0x2222222222222222222222222222222222222222",
	"token":       "0x3333333333333333333333333333333333333333",
	"chain_id":    8453,
	"signed_at":   "2026-06-30T12:05:00Z",
	"created_at":  "2026-06-30T12:00:00Z",
}

var mockPrepareResp = map[string]any{
	"unsigned_transaction": "0xdeadbeef",
	"to":                   "0x4444444444444444444444444444444444444444",
	"data":                 "0x",
	"chain_id":             8453,
	"nonce":                1,
	"maxFeePerGas":         1000000000,
	"maxPriorityFeePerGas": 1000000000,
	"gasLimit":             100000,
}

// The submit endpoints (POST /payments/{id}/{op}) acknowledge with a pending
// Transaction record — snake_case, matching the gateway Transaction schema.
var mockSubmitResp = map[string]any{
	"id":           "tx_123",
	"payment_id":   "pay_123",
	"operation":    "authorize",
	"status":       "submitting",
	"amount":       "10500000",
	"submitted_at": "2026-06-30T12:06:00Z",
}

var mockSubmitCaptureResp = map[string]any{
	"id":           "tx_cap",
	"payment_id":   "pay_123",
	"operation":    "capture",
	"status":       "submitting",
	"amount":       "1000000",
	"submitted_at": "2026-06-30T12:07:00Z",
}

var mockSubmitVoidResp = map[string]any{
	"id":         "tx_void",
	"payment_id": "pay_123",
	"operation":  "void",
	"status":     "submitting",
}

var mockReleaseResp = map[string]any{
	"id":         "tx_rel",
	"payment_id": "pay_123",
	"operation":  "release",
	"status":     "submitting",
}

var mockSubmitRefundResp = map[string]any{
	"id":               "tx_ref",
	"payment_id":       "pay_123",
	"operation":        "refund",
	"status":           "submitting",
	"amount":           "500000",
	"transaction_hash": "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
}

// ================================================================
//  Payments.CreatePayment
// ================================================================

func TestCreatePayment_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	if req.URL.Path != "/payments" {
		t.Errorf("path: got %s, want /payments", req.URL.Path)
	}
}

func TestCreatePayment_SendsJSONBody(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt)

	params := mockCreateReq
	if _, err := client.Payments.CreatePayment(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(mt.recorded[0].Body)
	var decoded rail0.CreatePaymentRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if decoded.Amount != "10.50" {
		t.Errorf("amount: got %s, want 10.50", decoded.Amount)
	}
	if decoded.ChainID != 8453 {
		t.Errorf("chain_id: got %d, want 8453", decoded.ChainID)
	}
	if decoded.Mode != "authorize" {
		t.Errorf("mode: got %s, want authorize", decoded.Mode)
	}
}

func TestCreatePayment_ReturnsCreatePaymentResponse(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)
	if err != nil {
		t.Fatal(err)
	}

	if res.Rail0Id != mockPaymentID {
		t.Errorf("Rail0Id: got %s, want %s", res.Rail0Id, mockPaymentID)
	}
	if res.ConfigHash == "" {
		t.Error("ConfigHash should not be empty")
	}
	if res.SignedAt != "2026-06-30T12:05:00Z" {
		t.Errorf("SignedAt: got %q, want the fixture timestamp", res.SignedAt)
	}
}

// ================================================================
//  Payments.Sign
// ================================================================

func TestSign_PutsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSignedPaymentResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.Sign(context.Background(), mockPaymentID, rail0.PayerSignatureRequest{
		Signature: "0x" + strings.Repeat("ab", 65),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPut {
		t.Errorf("method: got %s, want PUT", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/sign"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestSign_SendsVRS(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSignedPaymentResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.Sign(context.Background(), mockPaymentID, rail0.PayerSignatureRequest{
		Signature: "0x" + strings.Repeat("ab", 65),
	}); err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(mt.recorded[0].Body)
	var decoded rail0.PayerSignatureRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if decoded.Signature == "" {
		t.Error("signature should not be empty")
	}
}

// Sign now returns the full Payment (moved to "signed", signed_at populated),
// not the stale {paymentId,status} acknowledgement.
func TestSign_ReturnsSignedPayment(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSignedPaymentResp))
	client := newMockClient(t, mt)

	p, err := client.Payments.Sign(context.Background(), mockPaymentID, rail0.PayerSignatureRequest{
		Signature: "0x" + strings.Repeat("ab", 65),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "signed" {
		t.Errorf("Status: got %q, want signed", p.Status)
	}
	if p.SignedAt == "" {
		t.Error("SignedAt should be set on a signed payment")
	}
	if p.Rail0Id != mockPaymentID {
		t.Errorf("Rail0Id: got %s, want %s", p.Rail0Id, mockPaymentID)
	}
}

// ================================================================
//  Payments.AuthorizePayload / Authorize
// ================================================================

func TestAuthorizePayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.AuthorizePrepare(context.Background(), mockPaymentID)
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/authorize/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestAuthorize_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.Authorize(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/authorize"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestAuthorize_ReturnsSubmitResponse(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Authorize(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("Status: got %s, want submitting", res.Status)
	}
	if res.ID != "tx_123" {
		t.Errorf("ID: got %q, want tx_123", res.ID)
	}
	if res.Operation != "authorize" {
		t.Errorf("Operation: got %q, want authorize", res.Operation)
	}
	if res.Amount != "10500000" {
		t.Errorf("Amount: got %q, want 10500000", res.Amount)
	}
}

// ================================================================
//  Payments.ChargePayload / Charge
// ================================================================

func TestChargePayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.ChargePrepare(context.Background(), mockPaymentID)
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/charge/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestCharge_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.Charge(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/charge"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

// ================================================================
//  Payments.CapturePayload
// ================================================================

func TestCapturePayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.CapturePrepare(context.Background(), mockPaymentID, "1000000")
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/capture/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestCapturePayload_ReturnsPrepareTransactionResponse(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.CapturePrepare(context.Background(), mockPaymentID, "1000000")
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("UnsignedTransaction should not be empty")
	}
}

// ================================================================
//  Payments.Capture
// ================================================================

func TestCapture_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSubmitCaptureResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Capture(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/capture"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	if res.Operation != "capture" || res.Amount != "1000000" {
		t.Errorf("decoded tx: got operation=%q amount=%q, want capture/1000000", res.Operation, res.Amount)
	}
}

// ================================================================
//  Payments.VoidPayload / Void
// ================================================================

func TestVoidPayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.VoidPrepare(context.Background(), mockPaymentID)
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/void/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestVoid_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSubmitVoidResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Void(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/void"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	if res.Operation != "void" || res.Status != "submitting" {
		t.Errorf("decoded tx: got operation=%q status=%q, want void/submitting", res.Operation, res.Status)
	}
}

// ================================================================
//  Payments.ReleasePayload / Release
// ================================================================

func TestReleasePayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.ReleasePrepare(context.Background(), mockPaymentID, "")
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/release/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestRelease_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockReleaseResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Release(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/release"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	if res.Operation != "release" {
		t.Errorf("Operation: got %q, want release", res.Operation)
	}
}

// ================================================================
//  Payments.RefundPayload / Refund
// ================================================================

func TestRefundPayload_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	_, err := client.Payments.RefundPrepare(context.Background(), mockPaymentID, rail0.PrepareRequest{
		Amount: "500000",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/refund/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestRefund_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSubmitRefundResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.Refund(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/refund"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	if res.Operation != "refund" || res.Amount != "500000" {
		t.Errorf("decoded tx: got operation=%q amount=%q, want refund/500000", res.Operation, res.Amount)
	}
	if res.TransactionHash == nil || *res.TransactionHash == "" {
		t.Error("TransactionHash should decode from the fixture")
	}
}

// ================================================================
//  Payments.List (query params + header pagination)
// ================================================================

func TestList_SendsQueryParamsAndDecodesPagination(t *testing.T) {
	mt := &mockTransport{t: t}
	// Bare JSON array body + pagination headers, as the gateway sends it.
	mt.push(listResp(200, []map[string]any{mockCreatePaymentResp}, "42"))
	client := newMockClient(t, mt)

	res, err := client.Payments.List(context.Background(), rail0.ListPaymentsParams{
		Status:  "authorized",
		Mode:    "authorize",
		Sort:    "-created_at",
		Page:    2,
		PerPage: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodGet {
		t.Errorf("method: got %s, want GET", req.Method)
	}
	if req.URL.Path != "/payments" {
		t.Errorf("path: got %s, want /payments", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("status") != "authorized" {
		t.Errorf("status query: got %q, want authorized", q.Get("status"))
	}
	if q.Get("mode") != "authorize" {
		t.Errorf("mode query: got %q, want authorize", q.Get("mode"))
	}
	if q.Get("sort") != "-created_at" {
		t.Errorf("sort query: got %q, want -created_at", q.Get("sort"))
	}
	if q.Get("page") != "2" {
		t.Errorf("page query: got %q, want 2", q.Get("page"))
	}
	if q.Get("per_page") != "20" {
		t.Errorf("per_page query: got %q, want 20", q.Get("per_page"))
	}

	// Pagination comes from X-Total-Count / X-Page / X-Per-Page headers.
	if res.Meta.Total != 42 {
		t.Errorf("Meta.Total: got %d, want 42", res.Meta.Total)
	}
	if res.Meta.Page != 1 {
		t.Errorf("Meta.Page: got %d, want 1 (from header)", res.Meta.Page)
	}
	if res.Meta.PerPage != 20 {
		t.Errorf("Meta.PerPage: got %d, want 20 (from header)", res.Meta.PerPage)
	}
	if len(res.Data) != 1 || res.Data[0].Rail0Id != mockPaymentID {
		t.Errorf("Data: got %d payments, want the single fixture", len(res.Data))
	}
}

// ================================================================
//  Payments.Transactions (query params + paging)
// ================================================================

func TestTransactions_SendsQueryParamsAndDecodes(t *testing.T) {
	mt := &mockTransport{t: t}
	// Like List, the gateway returns a bare JSON array body + pagination
	// headers (the /payments/:id/transactions route uses the same paginate
	// helper), so Transactions decodes via getList, not a {data,meta} envelope.
	mt.push(listResp(200, []map[string]any{mockSubmitCaptureResp}, "3"))
	client := newMockClient(t, mt)

	res, err := client.Payments.Transactions(context.Background(), mockPaymentID, rail0.ListTransactionsParams{
		Operation: "capture",
		Status:    "submitting",
		Sort:      "-submitted_at",
		Page:      1,
		PerPage:   10,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/transactions"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	q := req.URL.Query()
	if q.Get("operation") != "capture" {
		t.Errorf("operation query: got %q, want capture", q.Get("operation"))
	}
	if q.Get("status") != "submitting" {
		t.Errorf("status query: got %q, want submitting", q.Get("status"))
	}
	if q.Get("sort") != "-submitted_at" {
		t.Errorf("sort query: got %q, want -submitted_at", q.Get("sort"))
	}
	if q.Get("per_page") != "10" {
		t.Errorf("per_page query: got %q, want 10", q.Get("per_page"))
	}
	if len(res.Data) != 1 || res.Data[0].Operation != "capture" {
		t.Errorf("Data: got %d txs, want the single capture fixture", len(res.Data))
	}
	if res.Meta.Total != 3 {
		t.Errorf("Meta.Total: got %d, want 3", res.Meta.Total)
	}
}

// ================================================================
//  Disputes (prepare/submit + list)
// ================================================================

var mockDisputeResp = map[string]any{
	"id":         "dsp_1",
	"payment_id": "pay_123",
	"status":     "open",
	"reason":     "0x0000000000000000000000000000000000000000000000000000000000000001",
	"opened_at":  "2026-06-30T12:10:00Z",
}

func TestDisputePrepare_PostsToCorrectURLWithReason(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	res, err := client.Payments.DisputePrepare(context.Background(), mockPaymentID, "0x01")
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/dispute/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}

	body, _ := io.ReadAll(req.Body)
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if decoded["reason"] != "0x01" {
		t.Errorf("reason body: got %v, want 0x01", decoded["reason"])
	}
	if res.UnsignedTransaction == "" {
		t.Error("UnsignedTransaction should decode from the prepare fixture")
	}
}

func TestDispute_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.Dispute(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	}); err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodPost {
		t.Errorf("method: got %s, want POST", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/dispute"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestCloseDisputePrepare_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockPrepareResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.CloseDisputePrepare(context.Background(), mockPaymentID); err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/dispute/close/prepare"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestCloseDispute_PostsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))
	client := newMockClient(t, mt)

	if _, err := client.Payments.CloseDispute(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0xdeadbeef",
	}); err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	wantPath := "/payments/" + mockPaymentID + "/dispute/close"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
}

func TestDisputes_ListsHistory(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(listResp(200, []map[string]any{mockDisputeResp}, "1"))
	client := newMockClient(t, mt)

	disputes, err := client.Payments.Disputes(context.Background(), mockPaymentID)
	if err != nil {
		t.Fatal(err)
	}

	req := mt.recorded[0]
	if req.Method != http.MethodGet {
		t.Errorf("method: got %s, want GET", req.Method)
	}
	wantPath := "/payments/" + mockPaymentID + "/disputes"
	if req.URL.Path != wantPath {
		t.Errorf("path: got %s, want %s", req.URL.Path, wantPath)
	}
	if len(disputes) != 1 {
		t.Fatalf("expected 1 dispute, got %d", len(disputes))
	}
	if disputes[0].Status != "open" {
		t.Errorf("Status: got %q, want open", disputes[0].Status)
	}
	if disputes[0].ID != "dsp_1" {
		t.Errorf("ID: got %q, want dsp_1", disputes[0].ID)
	}
}

// ================================================================
//  404 error decoding
// ================================================================

func TestCreatePayment_404_ReturnsAPIError(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(404, map[string]any{"code": "PaymentNotFound", "message": "No payment found."}))
	client := newMockClient(t, mt)

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)

	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 404 {
		t.Errorf("Status: got %d, want 404", apiErr.Status)
	}
}

func TestAPIError_SurfacesGatewayStatusAndMessage(t *testing.T) {
	mt := &mockTransport{t: t}
	// The gateway puts the identifier in `status` and the text in `message`.
	mt.push(jsonResp(422, map[string]any{"status": "address_not_registered", "message": "Address not associated with any account"}))
	client := newMockClient(t, mt)

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)

	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "address_not_registered" {
		t.Errorf("Code: got %q, want address_not_registered", apiErr.Code)
	}
	if apiErr.Message != "Address not associated with any account" {
		t.Errorf("Message: got %q, want the gateway message", apiErr.Message)
	}
}

func TestAPIError_FallsBackToGrapeErrorField(t *testing.T) {
	mt := &mockTransport{t: t}
	// Grape validation errors carry the human text in `error`.
	mt.push(jsonResp(400, map[string]any{"error": "payer is invalid"}))
	client := newMockClient(t, mt)

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)

	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "payer is invalid" {
		t.Fatalf("expected message from the `error` field, got %v", err)
	}
}

func TestAPIError_EmptyBodyFallsBackToHTTPStatus(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(500, map[string]any{}))
	client := newMockClient(t, mt)

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)

	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "HTTP 500" {
		t.Fatalf("expected the HTTP 500 fallback, got %v", err)
	}
}

// ================================================================
//  Retry logic
// ================================================================

func TestRetry_SucceedsOnThirdAttempt(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.fail(errors.New("network failure"))
	mt.fail(errors.New("network failure"))
	mt.push(jsonResp(200, mockCreatePaymentResp))

	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	res, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)
	if err != nil {
		t.Fatalf("expected success after 2 retries, got: %v", err)
	}
	if res.Rail0Id != mockPaymentID {
		t.Errorf("Rail0Id: got %s", res.Rail0Id)
	}
	if mt.i != 3 {
		t.Errorf("expected 3 total attempts, got %d", mt.i)
	}
}

func TestRetry_ThrowsAfterExhausted(t *testing.T) {
	networkErr := errors.New("network failure")
	mt := &mockTransport{t: t}
	mt.fail(networkErr)
	mt.fail(networkErr)
	mt.fail(networkErr)

	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)
	if !errors.Is(err, networkErr) {
		t.Errorf("expected networkErr, got %v", err)
	}
	if mt.i != 3 {
		t.Errorf("expected 3 total attempts, got %d", mt.i)
	}
}

func TestRetry_DoesNotRetryHTTPErrors(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(404, map[string]any{"code": "PaymentNotFound", "message": "Not found."}))

	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	_, err := client.Payments.CreatePayment(context.Background(), mockCreateReq)
	var apiErr *rail0.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if mt.i != 1 {
		t.Errorf("HTTP errors must not be retried; got %d attempts", mt.i)
	}
}

// ================================================================
//  Logger callbacks
// ================================================================

func TestLogger_SuccessEntry(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockCreatePaymentResp))

	var entries []rail0.LogEntry
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.Logger = func(e rail0.LogEntry) { entries = append(entries, e) }
	})

	if _, err := client.Payments.CreatePayment(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Method != "POST" {
		t.Errorf("Method: got %s", e.Method)
	}
	if !strings.Contains(e.URL, "/payments") {
		t.Errorf("URL missing payments path: %s", e.URL)
	}
	if e.Status != 200 {
		t.Errorf("Status: got %d", e.Status)
	}
	if e.DurationMs < 0 {
		t.Error("DurationMs must be non-negative")
	}
	if e.Err != nil {
		t.Errorf("Err should be nil, got %v", e.Err)
	}
}

func TestLogger_IncludesRequestBodyOnPOST(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(202, mockSubmitResp))

	var entries []rail0.LogEntry
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.Logger = func(e rail0.LogEntry) { entries = append(entries, e) }
	})

	if _, err := client.Payments.Sign(context.Background(), mockPaymentID, rail0.PayerSignatureRequest{
		Signature: "0x" + strings.Repeat("ab", 65),
	}); err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Method != "PUT" {
		t.Errorf("Method: got %s", e.Method)
	}
	if e.Status != 202 {
		t.Errorf("Status: got %d", e.Status)
	}
	if e.RequestBody == nil {
		t.Error("RequestBody should be non-nil for PUT with body")
	}
	if e.Err != nil {
		t.Errorf("Err should be nil, got %v", e.Err)
	}
}

func TestLogger_HTTPError(t *testing.T) {
	mt := &mockTransport{t: t}
	errBody := map[string]any{"code": "PaymentNotFound", "message": "No payment found."}
	mt.push(jsonResp(404, errBody))

	var entries []rail0.LogEntry
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.Logger = func(e rail0.LogEntry) { entries = append(entries, e) }
	})

	_, _ = client.Payments.Authorize(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{SignedTransaction: "0xdeadbeef"})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Status != 404 {
		t.Errorf("Status: got %d", e.Status)
	}
	var apiErr *rail0.APIError
	if !errors.As(e.Err, &apiErr) {
		t.Errorf("Err should be *APIError, got %T", e.Err)
	}
}

func TestLogger_NetworkError(t *testing.T) {
	networkErr := errors.New("network failure")
	mt := &mockTransport{t: t}
	mt.fail(networkErr)

	var entries []rail0.LogEntry
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.Logger = func(e rail0.LogEntry) { entries = append(entries, e) }
	})

	_, _ = client.Payments.Authorize(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{SignedTransaction: "0xdeadbeef"})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Status != 0 {
		t.Errorf("Status should be 0 on network error, got %d", e.Status)
	}
	if !errors.Is(e.Err, networkErr) {
		t.Errorf("Err: expected networkErr, got %v", e.Err)
	}
	if e.ResponseBody != nil {
		t.Error("ResponseBody should be nil on network error")
	}
}

func TestLogger_AttemptAndWillRetryFields(t *testing.T) {
	networkErr := errors.New("network failure")
	mt := &mockTransport{t: t}
	mt.fail(networkErr)
	mt.push(jsonResp(200, mockCreatePaymentResp))

	var entries []rail0.LogEntry
	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 1
		o.RetryDelay = time.Nanosecond
		o.Logger = func(e rail0.LogEntry) { entries = append(entries, e) }
	})

	if _, err := client.Payments.CreatePayment(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}
	fail := entries[0]
	if fail.Attempt != 1 {
		t.Errorf("fail.Attempt: got %d, want 1", fail.Attempt)
	}
	if !fail.WillRetry {
		t.Error("fail.WillRetry should be true")
	}
	if !errors.Is(fail.Err, networkErr) {
		t.Errorf("fail.Err: expected networkErr")
	}

	success := entries[1]
	if success.Attempt != 2 {
		t.Errorf("success.Attempt: got %d, want 2", success.Attempt)
	}
	if success.WillRetry {
		t.Error("success.WillRetry should be false")
	}
	if success.Err != nil {
		t.Errorf("success.Err should be nil, got %v", success.Err)
	}
}

// ================================================================
//  APIError
// ================================================================

func TestAPIError_ExposesStatusAndCode(t *testing.T) {
	err := &rail0.APIError{Status: 422, Code: "InvalidAmount", Message: "Amount is zero."}

	if err.Status != 422 {
		t.Errorf("Status: got %d", err.Status)
	}
	if err.Code != "InvalidAmount" {
		t.Errorf("Code: got %s", err.Code)
	}
	if err.Message != "Amount is zero." {
		t.Errorf("Message: got %s", err.Message)
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("Error() should contain status code: %s", err.Error())
	}
}

// ================================================================
//  DebugLogger smoke test
// ================================================================

func TestDebugLogger_DoesNotPanic(t *testing.T) {
	// Just ensure DebugLogger runs without panicking on a normal entry.
	rail0.DebugLogger(rail0.LogEntry{
		Method:     "GET",
		URL:        "http://test.invalid/payments/0xabc",
		Status:     200,
		DurationMs: 42,
	})
}

// Confirm that a tiny RetryDelay makes retries complete without significant latency.
func TestRetry_SmallDelayIsRespected(t *testing.T) {
	networkErr := errors.New("net")
	mt := &mockTransport{t: t}
	mt.fail(networkErr)
	mt.fail(networkErr)
	mt.push(jsonResp(200, mockCreatePaymentResp))

	client := newMockClient(t, mt, func(o *rail0.ClientOptions) {
		o.MaxRetries = 2
		o.RetryDelay = time.Nanosecond
	})

	start := time.Now()
	if _, err := client.Payments.CreatePayment(context.Background(), mockCreateReq); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("zero RetryDelay took too long: %s", elapsed)
	}
}
