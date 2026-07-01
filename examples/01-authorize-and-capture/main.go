// Standard two-step payment flow: authorize → capture
//
// On-chain flow:
//   payer  signs EIP-3009         → off-chain
//   payee  PUT /sign              → stores signature server-side
//   payee  POST /authorize/prepare → get unsigned tx
//   payee  signs tx off-chain     → signed tx
//   payee  POST /authorize        → async broadcast (HTTP 202)
//   (poll) GET  /payments/:id     → wait for status "authorized"
//   payee  POST /capture/prepare  → get unsigned tx
//   payee  signs tx off-chain     → signed tx
//   payee  POST /capture          → async broadcast (HTTP 202)
//   (poll) GET  /payments/:id     → wait for status "captured"

package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	rail0 "github.com/rail0/go-sdk"
)

func main() {
	ctx := context.Background()
	client := rail0.NewClient(rail0.ClientOptions{
		BaseURL: "https://api.rail0.xyz",
	})

	// ----------------------------------------------------------------
	// Step 1 — Payer creates a payment intent
	// ----------------------------------------------------------------

	createResp, err := client.Payments.Create(ctx, rail0.CreatePaymentRequest{
		ChainID: 8453,
		Mode:    "authorize",
		Amount:  "50.00",
		Token:   "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Payer:   "0xBuyerAddress000000000000000000000000000000",
		Payee:   "0xMerchantAddress0000000000000000000000000000",
	})
	if err != nil {
		log.Fatalf("CreatePayment: %v", err)
	}

	fmt.Printf("Payment ID:  %s\n", createResp.Rail0Id)
	fmt.Printf("Config hash: %s\n", createResp.ConfigHash)

	// Step 2 — Payer signs and submits the EIP-712 signature.
	// Sign returns the updated payment (now "signed", with SignedAt set).
	signedPayment, err := client.Payments.Sign(ctx, createResp.Rail0Id, rail0.PayerSignatureRequest{
		Signature: "0x1a2b3c...(130 hex chars from eth_signTypedData_v4)",
	})
	if err != nil {
		log.Fatalf("Sign: %v", err)
	}
	fmt.Printf("Payment status: %s (signed at %s)\n", signedPayment.Status, signedPayment.SignedAt)

	// ----------------------------------------------------------------
	// Step 3 — Payee builds the unsigned authorize transaction
	// ----------------------------------------------------------------

	authPayload, err := client.Payments.AuthorizePrepare(ctx, createResp.Rail0Id)
	if err != nil {
		var apiErr *rail0.APIError
		if errors.As(err, &apiErr) {
			log.Fatalf("AuthorizePayload failed [%s]: %s", apiErr.Code, apiErr.Message)
		}
		log.Fatalf("AuthorizePayload: %v", err)
	}
	fmt.Printf("Unsigned authorize tx: %s\n", authPayload.UnsignedTransaction)

	// The payee signs authPayload.UnsignedTransaction offline, then submits:
	//   signedAuthTx := payeeWallet.SignTransaction(authPayload.UnsignedTransaction)
	signedAuthTx := "0x02f8..." // placeholder

	authSubmit, err := client.Payments.Authorize(ctx, createResp.Rail0Id,
		rail0.SubmitTransactionRequest{SignedTransaction: signedAuthTx})
	if err != nil {
		log.Fatalf("Authorize: %v", err)
	}
	fmt.Printf("Authorize enqueued: tx=%s status=%s\n", authSubmit.ID, authSubmit.Status)
	// poll until status == "authorized": client.Payments.Get(ctx, createResp.Rail0Id)

	// ----------------------------------------------------------------
	// Step 4 — Payee prepares and submits a capture transaction
	// ----------------------------------------------------------------

	capturePayload, err := client.Payments.CapturePrepare(ctx, createResp.Rail0Id, "50.00")
	if err != nil {
		log.Fatalf("CapturePayload: %v", err)
	}

	signedCaptureTx := "0x02f9..." // placeholder
	_ = capturePayload

	captureSubmit, err := client.Payments.Capture(ctx, createResp.Rail0Id,
		rail0.SubmitTransactionRequest{SignedTransaction: signedCaptureTx})
	if err != nil {
		var apiErr *rail0.APIError
		if errors.As(err, &apiErr) {
			log.Fatalf("Capture failed [%s]: %s", apiErr.Code, apiErr.Message)
		}
		log.Fatalf("Capture: %v", err)
	}
	fmt.Printf("Capture enqueued: tx=%s status=%s\n", captureSubmit.ID, captureSubmit.Status)
}
