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

var mockSubmitResp = map[string]any{
	"paymentId": mockPaymentID,
	"status":    "submitting",
}

var mockChargeResp = map[string]any{
	"paymentId":        mockPaymentID,
	"transactionHash":  "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"chargedAmount":    "1000000",
	"feeAmount":        "0",
	"refundableAmount": "1000000",
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

var mockSubmitCaptureResp = map[string]any{
	"paymentId":        mockPaymentID,
	"transactionHash":  "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"capturedAmount":   "1000000",
	"capturableAmount": "0",
	"refundableAmount": "1000000",
}

var mockSubmitVoidResp = map[string]any{
	"paymentId":       mockPaymentID,
	"transactionHash": "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"releasedAmount":  "1000000",
}

var mockReleaseResp = map[string]any{
	"paymentId":       mockPaymentID,
	"transactionHash": "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"releasedAmount":  "1000000",
}

var mockSubmitRefundResp = map[string]any{
	"paymentId":        mockPaymentID,
	"transactionHash":  "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"refundedAmount":   "500000",
	"refundableAmount": "500000",
}

var mockSignatureResp = map[string]any{
	"paymentId": mockPaymentID,
	"status":    "accepted",
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
}

// ================================================================
//  Payments.Sign
// ================================================================

func TestSign_PutsToCorrectURL(t *testing.T) {
	mt := &mockTransport{t: t}
	mt.push(jsonResp(200, mockSignatureResp))
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
	mt.push(jsonResp(200, mockSignatureResp))
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

	_, err := client.Payments.Capture(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
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

	_, err := client.Payments.Void(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
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

	_, err := client.Payments.Release(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
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

	_, err := client.Payments.Refund(context.Background(), mockPaymentID, rail0.SubmitTransactionRequest{
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
