package rail0

import (
	"strings"
	"testing"
	"time"
)

func TestToBaseUnits(t *testing.T) {
	cases := []struct {
		human    string
		decimals int
		want     string
		wantErr  bool
	}{
		{"1.5", 6, "1500000", false},
		{"1", 6, "1000000", false},
		{"0.000001", 6, "1", false},
		{"1.234567", 6, "1234567", false}, // exactly `decimals` digits is exact, not over-precise
		{"1.2345678", 6, "", true},        // one digit too many: refused, never truncated (#26)
		{"0", 6, "0", false},
		{"0.0", 6, "0", false},
		{"10", 0, "10", false},
		{"1.5", 18, "1500000000000000000", false},
		{"", 6, "", true},
		{"1.2.3", 6, "", true},
		{"abc", 6, "", true},
		{"-1", 6, "", true},
		{"1", -1, "", true},
		{"1", 78, "1" + strings.Repeat("0", 78), false}, // the uint256 digit count is the bound
		{"1", 79, "", true},                             // one past it is refused
		{"1", 100000000, "", true},                      // a hostile value errors, never allocates
	}
	for _, c := range cases {
		got, err := ToBaseUnits(c.human, c.decimals)
		if c.wantErr {
			if err == nil {
				t.Errorf("ToBaseUnits(%q, %d): expected error, got %q", c.human, c.decimals, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ToBaseUnits(%q, %d): unexpected error %v", c.human, c.decimals, err)
		} else if got != c.want {
			t.Errorf("ToBaseUnits(%q, %d) = %q, want %q", c.human, c.decimals, got, c.want)
		}
	}
}

func TestFormatAmount(t *testing.T) {
	cases := []struct {
		base     string
		decimals int
		want     string
	}{
		{"1500000", 6, "1.5"},
		{"2000000", 6, "2"},
		{"1", 6, "0.000001"},
		{"0", 6, "0"},
		{"1234567", 6, "1.234567"},
		{"1500000000000000000", 18, "1.5"},
		{"10", 0, "10"},
		{"notanumber", 6, "notanumber"},                 // returned unchanged
		{"1", 78, "0." + strings.Repeat("0", 77) + "1"}, // the uint256 digit count is the bound
		{"1500000", 79, "1500000"},                      // past it: the raw base value
		{"1500000", -1, "1500000"},                      // negative: the raw base value
		{" 15 ", 100, " 15 "},                           // unchanged means unchanged, untrimmed
	}
	for _, c := range cases {
		if got := FormatAmount(c.base, c.decimals); got != c.want {
			t.Errorf("FormatAmount(%q, %d) = %q, want %q", c.base, c.decimals, got, c.want)
		}
	}
}

func TestToBaseUnitsRoundTrip(t *testing.T) {
	for _, h := range []string{"1.5", "0.000001", "1000", "0.25"} {
		b, err := ToBaseUnits(h, 6)
		if err != nil {
			t.Fatalf("ToBaseUnits(%q): %v", h, err)
		}
		// Formatting the base units back yields a value that re-parses to the same base.
		b2, err := ToBaseUnits(FormatAmount(b, 6), 6)
		if err != nil || b2 != b {
			t.Errorf("round trip for %q: %q -> %q", h, b, b2)
		}
	}
}

func TestDescribeError(t *testing.T) {
	if DescribeError("refund_expired") == "" {
		t.Error("expected a hint for refund_expired")
	}
	if DescribeError("totally_unknown_code") != "" {
		t.Error("expected no hint for an unknown code")
	}
	e := &APIError{Code: "contract_revert", Reason: "not_payee"}
	if e.Hint() == "" {
		t.Error("expected Hint() to fall back to the Reason sub-cause")
	}
}

// Every code the gateway's error catalogue defines with an actionable next step should
// carry a local hint, including the ones the other SDKs already describe.
func TestDescribeErrorCoversTheGatewayCodes(t *testing.T) {
	for _, code := range []string{
		"account_deactivated", "wallet_deactivated", "not_your_account",
		"release_submitter_not_a_party", "validation_failed", "rate_limited",
		"event_not_found", "submission_timeout", "token_not_accepted",
		"payer_must_be_caller", "not_redrivable", "last_active_wallet", "last_admin",
		"not_your_wallet", "wallet_add_unavailable",
		// the public onboarding codes of POST /signup and POST /claim
		"signup_unavailable", "onboarding_unavailable", "email_provider_not_configured",
		"email_domain_not_allowed", "account_email_taken", "account_wallet_taken",
		"account_name_taken", "terms_required", "onboarding_link_invalid",
		"onboarding_link_expired", "onboarding_link_superseded",
		// gateway error catalogue additions aligned on 2026-10-19
		"webhook_limit_reached", "queue_unavailable", "invalid_signature",
		"siwe_purpose_mismatch", "transaction_mismatch", "nonce_used",
		"signing_payload_unavailable", "invalid_sort",
	} {
		if DescribeError(code) == "" {
			t.Errorf("expected a hint for %s", code)
		}
	}
}

// not_releasable means nothing uncaptured is left; the expiry window is a different code
// (authorization_not_expired). The hint used to conflate the two and sent callers to wait
// for a window that would never help.
func TestNotReleasableHintIsAboutTheBalanceNotTheWindow(t *testing.T) {
	h := DescribeError("not_releasable")
	if strings.Contains(h, "authorizationExpiry") {
		t.Errorf("not_releasable hint mentions the expiry window: %q", h)
	}
	if !strings.Contains(h, "capturable") {
		t.Errorf("not_releasable hint should name the capturable balance: %q", h)
	}
}

// gateway#426 widened SIWE chain-id acceptance to 1 plus every active catalog chain, so
// the hint must point there instead of telling the caller to use "the chain id the
// client library sends" (a wallet on an active network is now accepted too).
func TestSiweChainMismatchHintNamesTheAcceptedChains(t *testing.T) {
	h := DescribeError("siwe_chain_mismatch")
	for _, want := range []string{"chain id 1", "GET /blockchains", "deactivated"} {
		if !strings.Contains(h, want) {
			t.Errorf("siwe_chain_mismatch hint should mention %q: %q", want, h)
		}
	}
	if strings.Contains(h, "nominal") {
		t.Errorf("siwe_chain_mismatch hint still carries the pre-#426 wording: %q", h)
	}
}

// The catalogue's invalid_signature text now covers a bad recovery byte, the commonest
// cause from a hand-packed signature (v as 0/1 instead of 27/28): the hint must say so.
func TestInvalidSignatureHintNamesTheRecoveryByte(t *testing.T) {
	h := DescribeError("invalid_signature")
	for _, want := range []string{"recovery byte", "65 bytes", "PackSignature"} {
		if !strings.Contains(h, want) {
			t.Errorf("invalid_signature hint should mention %q: %q", want, h)
		}
	}
}

// A gateway answering an absurd decimals must not hang the caller: FormatAmount used to
// pad one zero per loop iteration up to decimals, so 100000000 spun for minutes. The
// bound makes it return the raw value at once.
func TestFormatAmountHostileDecimalsReturnsPromptly(t *testing.T) {
	done := make(chan string, 1)
	go func() { done <- FormatAmount("1500000", 100000000) }()
	select {
	case got := <-done:
		if got != "1500000" {
			t.Errorf("FormatAmount with hostile decimals = %q, want the raw base value", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FormatAmount with hostile decimals did not return")
	}
}
