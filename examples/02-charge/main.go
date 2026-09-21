// One-shot charge flow: authorize + capture in a single transaction.

package main

import (
	"context"
	"fmt"
	"log"

	rail0 "github.com/commercelayer/rail0-go"
)

func main() {
	ctx := context.Background()
	// /payments is SIWE-gated — authenticate first. Seed a JWT you hold (below), or run
	// client.Auth.Login(ctx, key, "api.rail0.xyz"), which stashes the token on this client.
	client := rail0.NewClient(rail0.ClientOptions{
		BaseURL: "https://api.rail0.xyz",
		Token:   "<jwt>", // Authorization: Bearer <jwt>
	})

	createResp, err := client.Payments.Create(ctx, rail0.CreatePaymentRequest{
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

	// Payer signs and submits signature. Sign returns the updated payment.
	signedPayment, err := client.Payments.Sign(ctx, createResp.Rail0Id, rail0.PayerSignatureRequest{
		Signature: "0x...",
	})
	if err != nil {
		log.Fatalf("Sign: %v", err)
	}
	fmt.Printf("Payment status: %s\n", signedPayment.Status)

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
