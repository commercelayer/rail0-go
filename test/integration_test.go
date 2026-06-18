// Package rail0_test contains integration tests for the RAIL0 Go SDK.
//
// Tests in this file verify that every endpoint is correctly serialised/deserialised
// over real HTTP using net/http/httptest — no mocked transport. The mock server
// returns fixed, correctly-typed responses for each route.
package rail0_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	rail0 "github.com/rail0/go-sdk"
)

// ================================================================
//  Mock server
// ================================================================

var (
	bytes32RE = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

// newTestServer creates an httptest.Server that serves all RAIL0 API routes
// with fixture responses and returns a connected Client.
func newTestServer(t *testing.T) *rail0.Client {
	t.Helper()

	respond := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	createPaymentFixture := map[string]any{
		"rail0_id":    "0x1111111111111111111111111111111111111111111111111111111111111111",
		"config_hash": "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"payment": map[string]any{
			"payer": "0xBuyerAddress0000000000000000000000000000",
			"payee": "0xMerchantAddress00000000000000000000000000",
			"token": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"amount": "100000000", "authorization_expiry": 9999999999, "refund_expiry": 9999999999,
			"fee_bps": 0, "fee_receiver": "0x0000000000000000000000000000000000000000",
		},
		"chain_id":      8453,
		"rail0_contract": "0x4444444444444444444444444444444444444444",
		"signing_prepare": map[string]any{
			"domain": map[string]any{"name": "USD Coin", "version": "2", "chainId": 8453, "verifyingContract": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
			"types": map[string]any{"TransferWithAuthorization": []any{}},
			"primaryType": "TransferWithAuthorization",
			"message": map[string]any{
				"from": "0xBuyerAddress0000000000000000000000000000",
				"to": "0x4444444444444444444444444444444444444444",
				"value": "50000000", "validAfter": "0", "validBefore": "9999999999",
				"nonce": "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			},
		},
	}

	sigFixture := map[string]any{
		"paymentId": "0x1111111111111111111111111111111111111111111111111111111111111111",
		"status":    "signature_stored",
	}

	submitFixture := map[string]any{
		"rail0_id": "0x1111111111111111111111111111111111111111111111111111111111111111",
		"status":   "submitting",
	}

	prepareFixture := map[string]any{
		"unsigned_transaction": "0x02f8beef",
		"to":                   "0x4444444444444444444444444444444444444444",
		"data":                 "0x",
		"chain_id":             8453,
		"nonce":                1,
		"maxFeePerGas":         1000000000,
		"maxPriorityFeePerGas": 1000000000,
		"gasLimit":             100000,
	}

	_ = map[string]any{
		"paymentId":        "0x1111111111111111111111111111111111111111111111111111111111111111",
		"transactionHash":  "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"capturedAmount":   "50000000",
		"capturableAmount": "0",
		"refundableAmount": "50000000",
	}

	_ = submitFixture // used inline in switch

	mux := http.NewServeMux()

	// POST /payments
	mux.HandleFunc("POST /payments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		respond(w, createPaymentFixture)
	})

	// PUT /payments/{id}/sign
	mux.HandleFunc("PUT /payments/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/payments/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 && parts[1] == "sign" {
			respond(w, sigFixture)
		} else {
			http.NotFound(w, r)
		}
	})

	// POST /payments/{id}/...
	mux.HandleFunc("POST /payments/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/payments/")
		parts := strings.SplitN(path, "/", 3)
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		action := strings.Join(parts[1:], "/")
		switch action {
		case "authorize/prepare", "charge/prepare", "capture/prepare", "void/prepare", "release/prepare", "refund/prepare":
			respond(w, prepareFixture)
		case "authorize", "charge", "capture", "void", "release", "refund":
			w.WriteHeader(http.StatusAccepted)
			respond(w, submitFixture)
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return rail0.NewClient(rail0.ClientOptions{BaseURL: srv.URL})
}

// ================================================================
//  Test fixtures
// ================================================================

const integrationPaymentID = "0x1111111111111111111111111111111111111111111111111111111111111111"

var integrationPayment = rail0.CreatePaymentRequest{
	ChainID: 8453,
	Mode:    "authorize",
	Amount:  "100.00",
	Token:   "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
	Payer:   "0xBuyerAddress0000000000000000000000000000",
	Payee:   "0xMerchantAddress00000000000000000000000000",
}

const integrationSignature = "0xabababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab"

// ================================================================
//  Payments
// ================================================================

func TestIntegration_CreatePayment(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.CreatePayment(context.Background(), integrationPayment)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes32RE.MatchString(res.Rail0Id) {
		t.Errorf("rail0_id format invalid: %s", res.Rail0Id)
	}
	if !bytes32RE.MatchString(res.ConfigHash) {
		t.Errorf("configHash format invalid: %s", res.ConfigHash)
	}
}

func TestIntegration_Sign(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Sign(context.Background(), integrationPaymentID, rail0.PayerSignatureRequest{
		Signature: integrationSignature,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "" {
		t.Error("status should not be empty")
	}
}

func TestIntegration_AuthorizePrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.AuthorizePrepare(context.Background(), integrationPaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Authorize(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Authorize(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}

func TestIntegration_ChargePrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.ChargePrepare(context.Background(), integrationPaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Charge(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Charge(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}

func TestIntegration_CapturePrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.CapturePrepare(context.Background(), integrationPaymentID, "50000000")
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Capture(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Capture(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}

func TestIntegration_VoidPrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.VoidPrepare(context.Background(), integrationPaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Void(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Void(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}

func TestIntegration_ReleasePrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.ReleasePrepare(context.Background(), integrationPaymentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Release(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Release(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}

func TestIntegration_RefundPrepare(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.RefundPrepare(context.Background(), integrationPaymentID, rail0.PrepareRequest{
		Amount: "10000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.UnsignedTransaction == "" {
		t.Error("unsignedTransaction should not be empty")
	}
}

func TestIntegration_Refund(t *testing.T) {
	client := newTestServer(t)
	res, err := client.Payments.Refund(context.Background(), integrationPaymentID, rail0.SubmitTransactionRequest{
		SignedTransaction: "0x02f8...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitting" {
		t.Errorf("status: got %s, want submitting", res.Status)
	}
}
