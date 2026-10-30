// Merchant cash-out: register a payout destination, prove it, then pay out.
//
// Flow:
//   merchant POST /payout_destinations      → "unverified" (requires confirm_details: true)
//   merchant POST /payouts/prepare (proof)  → unsigned ERC-20 transfer
//   merchant verifies + signs it locally    → POST /payouts/:id/submit (gateway broadcasts)
//   (poll)   GET  /payouts/:id              → wait for "confirmed"
//   merchant checks the venue credited it   → POST /payout_destinations/:id/confirm → "verified"
//   merchant POST /payouts/prepare (amount) → verify, sign, submit, poll to "confirmed"
//
// The private key is read from RAIL0_PRIVATE_KEY and never printed.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	rail0 "github.com/commercelayer/rail0-go"
)

func main() {
	ctx := context.Background()
	key, err := rail0.HexToPrivateKey(os.Getenv("RAIL0_PRIVATE_KEY"))
	if err != nil {
		log.Fatalf("RAIL0_PRIVATE_KEY: %v", err)
	}
	client := rail0.NewClient(rail0.ClientOptions{BaseURL: "https://api.rail0.xyz"})
	// SIWE login with the merchant wallet; stashes the JWT on this client.
	if _, err := client.Auth.Login(ctx, key, "api.rail0.xyz"); err != nil {
		log.Fatalf("Login: %v", err)
	}
	wallet, _ := rail0.SignerAddress(key)

	// 1. The merchant confirms the details — explicitly, never by default.
	fmt.Println(rail0.PayoutDetailsConfirmationText)
	fmt.Print("Type \"yes\" to confirm: ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	confirmed := strings.TrimSpace(strings.ToLower(answer)) == "yes"
	if !confirmed {
		log.Fatal("not confirmed: nothing registered")
	}

	// The venue's minimum deposit is a required answer: a figure, or NoMinimum: true.
	minAmount := rail0.Uint256String("11000000")
	dest, err := client.PayoutDestinations.Create(ctx, rail0.CreatePayoutDestinationRequest{
		WalletID:       wallet,
		ChainID:        5042002,                                      // the network the venue issued the address for
		Token:          "0x3600000000000000000000000000000000000000", // USDC on that chain
		Address:        "0xVenueDepositAddress0000000000000000000000",
		Label:          "Exchange EUR",
		Kind:           rail0.PayoutDestinationKindExchange,
		MinAmount:      &minAmount, // the venue's deposit minimum, base units — or NoMinimum: true when it has none
		ConfirmDetails: confirmed,
	})
	if err != nil {
		log.Fatalf("PayoutDestinations.Create: %v", err)
	}
	fmt.Printf("Destination %s: %s\n", dest.ID, dest.Status)

	// 2. The proof transfer: prepare → verify → sign → submit, then wait for it.
	proof, err := client.Payouts.PrepareAndSubmit(ctx, rail0.PreparePayoutRequest{
		DestinationID: dest.ID,
		Purpose:       rail0.PayoutPurposeProof,
	}, key)
	if err != nil {
		log.Fatalf("proof: %v", err)
	}
	if proof, err = waitSettled(ctx, client, proof.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Proof %s: %s (%s %s)\n", proof.ID, proof.Status, proof.AmountFormatted, proof.TokenSymbol)

	// Once the venue shows the proof credited, the merchant attests it.
	dest, err = client.PayoutDestinations.Confirm(ctx, dest.ID)
	if err != nil {
		log.Fatalf("PayoutDestinations.Confirm: %v", err)
	}
	fmt.Printf("Destination %s: %s\n", dest.ID, dest.Status)

	// 3. The payout. Amount is a human decimal, or rail0.PayoutAmountAll for the
	// balance minus the refund reserve.
	payout, err := client.Payouts.PrepareAndSubmit(ctx, rail0.PreparePayoutRequest{
		DestinationID: dest.ID,
		Amount:        "125.50",
	}, key)
	if err != nil {
		var apiErr *rail0.APIError
		if errors.As(err, &apiErr) {
			log.Fatalf("payout: %v (%s)", err, apiErr.Hint())
		}
		log.Fatalf("payout: %v", err)
	}
	if payout, err = waitSettled(ctx, client, payout.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Payout %s: %s, tx %s\n", payout.ID, payout.Status, payout.TransactionHash)
}

// waitSettled polls a payout until it is confirmed or failed.
func waitSettled(ctx context.Context, client *rail0.Client, id string) (*rail0.Payout, error) {
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		p, err := client.Payouts.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("Payouts.Get: %w", err)
		}
		switch p.Status {
		case rail0.PayoutStatusConfirmed:
			return p, nil
		case rail0.PayoutStatusFailed:
			return p, fmt.Errorf("payout %s failed: %s (%s)", p.ID, p.ErrorCode, p.ErrorDetail)
		}
		time.Sleep(5 * time.Second)
	}
	return nil, fmt.Errorf("payout %s not settled after 15 minutes", id)
}
