package rail0

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Hardhat account #0: a well-known test key, never funded on a real chain.
const payoutTestKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

const (
	payoutChain  = 5042002
	payoutToken  = "0x3600000000000000000000000000000000000000"
	payoutDest   = "0x1111111111111111111111111111111111111111"
	payoutAmount = "125500000" // 125.5 at 6 decimals
	payoutDestID = "0b5f1d3e-7a42-4c1e-9b1a-6f3c2d1e0a99"
	payoutID     = "3c7f2a10-1d2b-4e5f-8a9b-0c1d2e3f4a5b"
)

func transferData(to, amount string) string {
	n, _ := new(big.Int).SetString(amount, 10)
	return "0xa9059cbb" +
		strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(to), "0x") +
		fmt.Sprintf("%064x", n)
}

// unsignedTransfer builds the gateway's unsigned_transaction JSON for a transfer, with
// mut applied to the field map so each refusal case changes exactly one thing.
func unsignedTransfer(t *testing.T, mut func(m map[string]any)) string {
	t.Helper()
	m := map[string]any{
		"chain_id":                 payoutChain,
		"from":                     "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266",
		"to":                       payoutToken,
		"value":                    0,
		"data":                     transferData(payoutDest, payoutAmount),
		"nonce":                    7,
		"gas_limit":                65000,
		"max_priority_fee_per_gas": 1000000000,
		"max_fee_per_gas":          30000000000,
	}
	if mut != nil {
		mut(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func payoutWant() PayoutTransferExpectation {
	return PayoutTransferExpectation{ChainID: payoutChain, Token: payoutToken, To: payoutDest, Amount: payoutAmount}
}

func TestERC20TransferSelector(t *testing.T) {
	if ERC20TransferSelector != "a9059cbb" {
		t.Fatalf("selector = %s, want a9059cbb", ERC20TransferSelector)
	}
}

func TestVerifyPayoutTransferAcceptsTheExactTransfer(t *testing.T) {
	signer := "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
	if err := VerifyPayoutTransfer(unsignedTransfer(t, nil), signer, payoutWant()); err != nil {
		t.Fatalf("exact transfer refused: %v", err)
	}
	// Case-insensitive addresses on both sides, and the value encodings a zero arrives in.
	want := payoutWant()
	want.Token = Address(strings.ToUpper(payoutToken[:2]) + strings.ToUpper(payoutToken[2:]))
	want.To = Address("0x" + strings.ToUpper(payoutDest[2:]))
	for _, v := range []any{nil, "0", "0x0"} {
		tx := unsignedTransfer(t, func(m map[string]any) { m["value"] = v })
		if err := VerifyPayoutTransfer(tx, "", want); err != nil {
			t.Errorf("value %v: refused: %v", v, err)
		}
	}
}

func TestVerifyPayoutTransferRefuses(t *testing.T) {
	other := "0x2222222222222222222222222222222222222222"
	cases := []struct {
		name string
		mut  func(m map[string]any)
		want func(w *PayoutTransferExpectation)
		msg  string
	}{
		{"wrong recipient", func(m map[string]any) { m["data"] = transferData(other, payoutAmount) }, nil, "pays " + other},
		{"wrong amount", func(m map[string]any) { m["data"] = transferData(payoutDest, "125500001") }, nil, "moves 125500001"},
		{"wrong token", func(m map[string]any) { m["to"] = other }, nil, "addressed to " + other},
		{"non-zero value", func(m map[string]any) { m["value"] = 1 }, nil, "native currency"},
		{"non-zero hex value", func(m map[string]any) { m["value"] = "0x1" }, nil, "native currency"},
		{"wrong chain", func(m map[string]any) { m["chain_id"] = 1 }, nil, "chain"},
		{"missing chain", func(m map[string]any) { delete(m, "chain_id") }, nil, "chain"},
		{"trailing bytes", func(m map[string]any) { m["data"] = transferData(payoutDest, payoutAmount) + "00" }, nil, "trailing"},
		{"trailing word", func(m map[string]any) { m["data"] = transferData(payoutDest, payoutAmount) + strings.Repeat("0", 64) }, nil, "trailing"},
		{"approve instead of transfer", func(m map[string]any) {
			m["data"] = "0x095ea7b3" + strings.TrimPrefix(transferData(payoutDest, payoutAmount), "0xa9059cbb")
		}, nil, "selector 0x095ea7b3"},
		{"short calldata", func(m map[string]any) { m["data"] = transferData(payoutDest, payoutAmount)[:74] }, nil, "too short"},
		{"empty calldata", func(m map[string]any) { m["data"] = "0x" }, nil, "too short"},
		{"dirty address padding", func(m map[string]any) {
			d := transferData(payoutDest, payoutAmount)
			m["data"] = d[:10] + "ff" + d[12:]
		}, nil, "left-padded"},
		{"no to (contract creation)", func(m map[string]any) { delete(m, "to") }, nil, "no valid `to`"},
		{"wrong sender", func(m map[string]any) { m["from"] = other }, nil, "prepared from"},
		{"expectation without chain", nil, func(w *PayoutTransferExpectation) { w.ChainID = 0 }, "chain is unknown"},
		{"expectation without token", nil, func(w *PayoutTransferExpectation) { w.Token = "" }, "token"},
		{"expectation without recipient", nil, func(w *PayoutTransferExpectation) { w.To = "" }, "destination's address"},
		{"expectation with a decimal amount", nil, func(w *PayoutTransferExpectation) { w.Amount = "125.5" }, "not base units"},
		{"expectation without amount", nil, func(w *PayoutTransferExpectation) { w.Amount = "" }, "not base units"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := payoutWant()
			if tc.want != nil {
				tc.want(&want)
			}
			err := VerifyPayoutTransfer(unsignedTransfer(t, tc.mut), "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266", want)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.HasPrefix(err.Error(), "rail0: refusing to sign:") || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error = %q, want a refusal mentioning %q", err, tc.msg)
			}
		})
	}
	if err := VerifyPayoutTransfer("{not json", "", payoutWant()); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestSignPayoutTransferSignsOnlyTheExpectedTransfer(t *testing.T) {
	key, _ := HexToPrivateKey(payoutTestKey)
	raw, err := SignPayoutTransfer(unsignedTransfer(t, nil), key, payoutWant())
	if err != nil {
		t.Fatalf("SignPayoutTransfer: %v", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(common.FromHex(raw)); err != nil {
		t.Fatalf("decode signed tx: %v", err)
	}
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(from.Hex(), "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266") {
		t.Errorf("signed by %s", from.Hex())
	}
	if !strings.EqualFold(tx.To().Hex(), payoutToken) || tx.Value().Sign() != 0 || tx.ChainId().Int64() != payoutChain {
		t.Errorf("signed tx to=%s value=%s chain=%s", tx.To().Hex(), tx.Value(), tx.ChainId())
	}

	bad := payoutWant()
	bad.To = "0x2222222222222222222222222222222222222222"
	if raw, err := SignPayoutTransfer(unsignedTransfer(t, nil), key, bad); err == nil || raw != "" {
		t.Errorf("signed a transfer to the wrong recipient: raw=%q err=%v", raw, err)
	}
}

// payoutGateway is a fake gateway for the payout routes: it records every request
// and answers prepare with tx (built by the test), the destination with dest, and the
// token catalog with one 6-decimals token.
type payoutGateway struct {
	t        *testing.T
	tx       string
	dest     map[string]any
	payout   map[string]any
	requests []string
	bodies   map[string]map[string]any
}

func (g *payoutGateway) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		g.requests = append(g.requests, key)
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				if g.bodies == nil {
					g.bodies = map[string]map[string]any{}
				}
				g.bodies[key] = m
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "POST /payouts/prepare":
			p := map[string]any{}
			for k, v := range g.payout {
				p[k] = v
			}
			p["unsigned_transaction"] = g.tx
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(p)
		case "GET /payout_destinations/" + payoutDestID:
			_ = json.NewEncoder(w).Encode(g.dest)
		case "GET /tokens":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"address": payoutToken, "chain_id": payoutChain, "decimals": 6, "symbol": "USDC", "active": true}})
		case "POST /payouts/" + payoutID + "/submit":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": payoutID, "status": "submitted", "signed": true, "transaction_hash": "0xabc"})
		default:
			g.t.Errorf("unexpected request %s", key)
			w.WriteHeader(404)
		}
	}))
}

func newPayoutGateway(t *testing.T) *payoutGateway {
	return &payoutGateway{
		t:  t,
		tx: unsignedTransfer(t, nil),
		dest: map[string]any{
			"id": payoutDestID, "chain_id": payoutChain, "token": payoutToken, "address": payoutDest, "status": "verified",
		},
		payout: map[string]any{
			"id": payoutID, "destination_id": payoutDestID, "chain_id": payoutChain, "token": payoutToken,
			"address": payoutDest, "amount": payoutAmount, "purpose": "payout", "status": "pending", "signed": false,
		},
	}
}

func TestPrepareAndSubmitVerifiesSignsAndSubmits(t *testing.T) {
	g := newPayoutGateway(t)
	srv := g.server()
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	key, _ := HexToPrivateKey(payoutTestKey)

	out, err := c.Payouts.PrepareAndSubmit(context.Background(), PreparePayoutRequest{DestinationID: payoutDestID, Amount: "125.50"}, key)
	if err != nil {
		t.Fatalf("PrepareAndSubmit: %v", err)
	}
	if out.Status != PayoutStatusSubmitted {
		t.Errorf("status = %q", out.Status)
	}
	want := []string{"POST /payouts/prepare", "GET /tokens", "GET /payout_destinations/" + payoutDestID, "POST /payouts/" + payoutID + "/submit"}
	if strings.Join(g.requests, "|") != strings.Join(want, "|") {
		t.Errorf("requests = %v, want %v", g.requests, want)
	}
	if b := g.bodies["POST /payouts/prepare"]; b["destination_id"] != payoutDestID || b["amount"] != "125.50" {
		t.Errorf("prepare body = %v", b)
	}
	signed, _ := g.bodies["POST /payouts/"+payoutID+"/submit"]["signed_transaction"].(string)
	if !strings.HasPrefix(signed, "0x02") {
		t.Errorf("submitted %q, want a signed type-2 transaction", signed)
	}
}

func TestPrepareAndSubmitRefusesBeforeSigning(t *testing.T) {
	cases := []struct {
		name string
		req  PreparePayoutRequest
		mut  func(g *payoutGateway)
		msg  string
	}{
		{"gateway pays another address", PreparePayoutRequest{DestinationID: payoutDestID, Amount: "125.5"}, func(g *payoutGateway) {
			g.tx = unsignedTransfer(g.t, func(m map[string]any) {
				m["data"] = transferData("0x2222222222222222222222222222222222222222", payoutAmount)
			})
		}, "pays 0x2222"},
		{"payout record and calldata both swapped", PreparePayoutRequest{DestinationID: payoutDestID, Amount: "125.5"}, func(g *payoutGateway) {
			g.payout["address"] = "0x2222222222222222222222222222222222222222"
			g.tx = unsignedTransfer(g.t, func(m map[string]any) {
				m["data"] = transferData("0x2222222222222222222222222222222222222222", payoutAmount)
			})
		}, "not the payout destination " + payoutDest},
		{"amount above the one asked", PreparePayoutRequest{DestinationID: payoutDestID, Amount: "100"}, nil, "you asked for 100"},
		{"another destination", PreparePayoutRequest{DestinationID: payoutDestID, Amount: "125.5"}, func(g *payoutGateway) {
			g.payout["destination_id"] = "7d1e0000-0000-4000-8000-000000000000"
		}, "is for destination"},
		{"a proof answered with a payout", PreparePayoutRequest{DestinationID: payoutDestID, Purpose: PayoutPurposeProof}, nil, "is a payout but you asked for a proof"},
		{"already signed", PreparePayoutRequest{DestinationID: payoutDestID, Amount: PayoutAmountAll}, func(g *payoutGateway) {
			g.payout["signed"] = true
		}, "already holds a signed transaction"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newPayoutGateway(t)
			if tc.mut != nil {
				tc.mut(g)
			}
			srv := g.server()
			defer srv.Close()
			c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
			key, _ := HexToPrivateKey(payoutTestKey)
			_, err := c.Payouts.PrepareAndSubmit(context.Background(), tc.req, key)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.msg)
			}
			for _, r := range g.requests {
				if strings.HasSuffix(r, "/submit") {
					t.Errorf("submitted despite the refusal: %v", g.requests)
				}
			}
		})
	}
}

func TestPayoutRoutesAndBodies(t *testing.T) {
	type rec struct {
		method, path, query string
		body                map[string]any
		raw                 string
	}
	var got []rec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got = append(got, rec{r.Method, r.URL.Path, r.URL.RawQuery, m, string(b)})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/events") || r.URL.Path == "/payouts" || r.URL.Path == "/payout_destinations") {
			w.Header().Set("X-Total-Count", "1")
			w.Header().Set("X-Page", "2")
			w.Header().Set("X-Per-Page", "10")
			_, _ = w.Write([]byte(`[{"id":"x"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err := c.PayoutDestinations.Create(ctx, CreatePayoutDestinationRequest{WalletID: "w", ChainID: payoutChain, Token: payoutToken, Address: payoutDest})
	must(err)
	if v, present := got[0].body["confirm_details"]; !present || v != false {
		t.Errorf("create without confirmation must SEND confirm_details=false (never default it): %s", got[0].raw)
	}
	_, err = c.PayoutDestinations.Create(ctx, CreatePayoutDestinationRequest{WalletID: "w", ChainID: payoutChain, Token: payoutToken, Address: payoutDest, MinAmount: "11000000", ConfirmDetails: true})
	must(err)
	if got[1].body["confirm_details"] != true || got[1].body["min_amount"] != "11000000" {
		t.Errorf("create body = %s", got[1].raw)
	}

	label := "Kraken"
	_, err = c.PayoutDestinations.Update(ctx, payoutDestID, UpdatePayoutDestinationRequest{Label: &label})
	must(err)
	if got[2].method != http.MethodPatch || got[2].raw != `{"label":"Kraken"}` {
		t.Errorf("label-only update = %s %s", got[2].method, got[2].raw)
	}
	minimum := Uint256String("")
	_, err = c.PayoutDestinations.Update(ctx, payoutDestID, UpdatePayoutDestinationRequest{MinAmount: &minimum, ConfirmDetails: true})
	must(err)
	if got[3].raw != `{"min_amount":"","confirm_details":true}` {
		t.Errorf("minimum update = %s", got[3].raw)
	}

	_, err = c.PayoutDestinations.Get(ctx, payoutDestID)
	must(err)
	_, err = c.PayoutDestinations.Confirm(ctx, payoutDestID)
	must(err)
	page, err := c.PayoutDestinations.Events(ctx, payoutDestID, ListPayoutEventsParams{Page: 2, PerPage: 10})
	must(err)
	if page.Meta.Total != 1 || page.Meta.Page != 2 || len(page.Data) != 1 {
		t.Errorf("events page = %+v", page)
	}
	_, err = c.PayoutDestinations.List(ctx, ListPayoutDestinationsParams{WalletID: "w", Status: "verified"})
	must(err)

	_, err = c.Payouts.List(ctx, ListPayoutsParams{DestinationID: payoutDestID, Status: "pending", Statuses: []string{"submitted", "pending"}, Purpose: "payout", PerPage: 50})
	must(err)
	_, err = c.Payouts.Get(ctx, payoutID)
	must(err)
	_, err = c.Payouts.Events(ctx, payoutID)
	must(err)
	_, err = c.Payouts.Submit(ctx, payoutID, "0x02ab")
	must(err)
	_, err = c.Payouts.Submitted(ctx, payoutID, "0xhash")
	must(err)
	_, err = c.Payouts.Resend(ctx, payoutID)
	must(err)
	_, err = c.Payouts.Cancel(ctx, payoutID)
	must(err)
	_, err = c.Payouts.Prepare(ctx, PreparePayoutRequest{DestinationID: payoutDestID, Purpose: PayoutPurposeProof})
	must(err)

	want := []struct{ method, path, query, raw string }{
		{"POST", "/payout_destinations", "", ""},
		{"POST", "/payout_destinations", "", ""},
		{"PATCH", "/payout_destinations/" + payoutDestID, "", ""},
		{"PATCH", "/payout_destinations/" + payoutDestID, "", ""},
		{"GET", "/payout_destinations/" + payoutDestID, "", ""},
		{"POST", "/payout_destinations/" + payoutDestID + "/confirm", "", "{}"},
		{"GET", "/payout_destinations/" + payoutDestID + "/events", "page=2&per_page=10", ""},
		{"GET", "/payout_destinations", "status=verified&wallet_id=w", ""},
		{"GET", "/payouts", "destination_id=" + payoutDestID + "&per_page=50&purpose=payout&status=pending%2Csubmitted", ""},
		{"GET", "/payouts/" + payoutID, "", ""},
		{"GET", "/payouts/" + payoutID + "/events", "", ""},
		{"POST", "/payouts/" + payoutID + "/submit", "", `{"signed_transaction":"0x02ab"}`},
		{"POST", "/payouts/" + payoutID + "/submitted", "", `{"transaction_hash":"0xhash"}`},
		{"POST", "/payouts/" + payoutID + "/resend", "", "{}"},
		{"POST", "/payouts/" + payoutID + "/cancel", "", "{}"},
		{"POST", "/payouts/prepare", "", `{"destination_id":"` + payoutDestID + `","purpose":"proof"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("%d requests, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.method != w.method || g.path != w.path || g.query != w.query {
			t.Errorf("request %d = %s %s?%s, want %s %s?%s", i, g.method, g.path, g.query, w.method, w.path, w.query)
		}
		if w.raw != "" && g.raw != w.raw {
			t.Errorf("request %d body = %s, want %s", i, g.raw, w.raw)
		}
	}
}

func TestPayoutWebhookTopicsAndHints(t *testing.T) {
	for topic, want := range map[string]string{
		TopicPayoutsConfirmed:           "payouts.confirmed",
		TopicPayoutsFailed:              "payouts.failed",
		TopicPayoutDestinationsVerified: "payout_destinations.verified",
	} {
		if topic != want {
			t.Errorf("topic %q, want %q", topic, want)
		}
	}
	for _, code := range []string{
		"confirmation_required", "payout_below_minimum", "payout_destination_unverified", "payout_destination_disabled",
		"payout_destination_proof_missing", "payout_already_signed", "payout_not_pending", "payout_not_signed",
		"payout_hash_in_use", "payout_limit_reached", "insufficient_balance", "account_deactivated",
		"foreign_transfer_hash", "transaction_mismatch",
	} {
		if DescribeError(code) == "" {
			t.Errorf("no hint for %s", code)
		}
	}
}
