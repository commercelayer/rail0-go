// One-shot charge flow: authorize + capture in a single transaction.

package main

import (
	"context"
	"fmt"
	"log"

	rail0 "github.com/rail0/go-sdk"
)

func main() {
	ctx := context.Background()
	client := rail0.NewClient(rail0.ClientOptions{BaseURL: "https://api.rail0.xyz"})

	createResp, err := client.Payments.CreatePayment(ctx, rail0.CreatePaymentRequest{
		ChainID: 8453,
		Mode:    "charge",
		Amount:  "25.00",
		Token:   "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Payer:   "0xBuyerAddress000000000000000000000000000000",
		Payee:   "0xMerchantAddress0000000000000000000000000000",
	})
	if err != nil {
		log.Fatalf("CreatePayment: %v", err)
	}
	fmt.Printf("Payment ID: %s\n", createResp.Rail0Id)

	// Payer signs and submits signature
	client.Payments.Sign(ctx, createResp.Rail0Id, rail0.PayerSignatureRequest{
		Signature: "0x...",
	})

	// Payee gets the unsigned charge tx
	chargePayload, err := client.Payments.ChargePrepare(ctx, createResp.Rail0Id)
	if err != nil {
		log.Fatalf("ChargePayload: %v", err)
	}

	// Payee signs offline → SIGNED_TX
	signedTx := "0x02f8ab" // placeholder
	_ = chargePayload

	// Submit signed tx
	chargeSubmit, err := client.Payments.Charge(ctx, createResp.Rail0Id,
		rail0.SubmitTransactionRequest{SignedTransaction: signedTx})
	if err != nil {
		log.Fatalf("Charge: %v", err)
	}
	fmt.Printf("Charge enqueued: tx=%s status=%s\n", chargeSubmit.ID, chargeSubmit.Status)
	// poll until status == "charged": client.Payments.Get(ctx, createResp.Rail0Id)
}
