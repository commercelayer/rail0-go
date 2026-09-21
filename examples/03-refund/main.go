// Refund flow using EIP-3009 receiveWithAuthorization.
// No separate ERC-20 approve() needed.

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

	paymentID := "0x..." // a captured payment

	// ── Phase 1: get the EIP-3009 signing payload (omit signature) ──────────────
	phase1, err := client.Payments.RefundPrepare(ctx, paymentID, rail0.PrepareRequest{
		Amount: "50.00",
	})
	if err != nil {
		log.Fatalf("RefundPrepare phase 1: %v", err)
	}
	// phase1.SigningPayload contains the EIP-712 typed-data the payee must sign
	// using eth_signTypedData_v4 or rail0.SignReceiveWithAuthorization.
	fmt.Printf("Signing payload nonce: %s\n", phase1.SigningPayload.Message.Nonce)

	// Payee signs the payload → 0x-prefixed 65-byte signature
	signature := "0x" + "ab" // placeholder

	// ── Phase 2: build the unsigned refund() tx with EIP-3009 sig embedded ────
	phase2, err := client.Payments.RefundPrepare(ctx, paymentID, rail0.PrepareRequest{
		Amount:    "50.00",
		Signature: signature,
	})
	if err != nil {
		log.Fatalf("RefundPrepare phase 2: %v", err)
	}
	fmt.Printf("Unsigned refund tx: %s\n", phase2.UnsignedTransaction)

	// Payee signs the EIP-1559 tx → SIGNED_TX
	signedTx := "0x02f8..." // placeholder

	// ── Submit ────────────────────────────────────────────────────────────────
	refundSubmit, err := client.Payments.Refund(ctx, paymentID,
		rail0.SubmitTransactionRequest{SignedTransaction: signedTx})
	if err != nil {
		log.Fatalf("Refund: %v", err)
	}
	fmt.Printf("Refund enqueued: tx=%s status=%s\n", refundSubmit.ID, refundSubmit.Status)
	// poll until status == "refunded": client.Payments.Get(ctx, paymentID)
}
