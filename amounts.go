package rail0

import (
	"fmt"
	"regexp"
	"strings"
)

var amountRe = regexp.MustCompile(`^\d+(\.\d+)?$`)

// maxDecimals is the largest token decimals ToBaseUnits and FormatAmount accept: 78, the
// number of digits in the uint256 maximum (2^256-1 ≈ 1.16e77). No ERC-20 amount can have
// more significant digits than that, so a larger value is not a real token's decimals —
// and both functions allocate proportionally to decimals, so an unbounded value handed
// over by a malicious or broken gateway (payment.Decimals, token catalogs) would make a
// caller build a string of that many zeros and hang or run out of memory.
const maxDecimals = 78

// ToBaseUnits converts a human decimal amount ("1.50") to its base-unit integer
// string ("1500000") for the given token decimals, using string math so there is
// no float rounding. Returns an error on a malformed amount, decimals outside
// [0, maxDecimals], or MORE fractional digits than the token has. Shared with the TS SDK's toBaseUnits so
// every consumer converts amounts identically.
//
// The over-precision case used to be truncated silently, and that is wrong for the one
// thing this function is for: the gateway answers 422 on an amount a token cannot
// express, so dropping the extra digits meant sending a DIFFERENT amount than the caller
// wrote — and learning about it from a rejected request, or worse from a successful one
// for less money. Refusing names the mistake at the call site.
func ToBaseUnits(human string, decimals int) (string, error) {
	if decimals < 0 || decimals > maxDecimals {
		return "", fmt.Errorf("invalid decimals: %d", decimals)
	}
	s := strings.TrimSpace(human)
	if !amountRe.MatchString(s) {
		return "", fmt.Errorf("invalid amount: %q", human)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > decimals {
		return "", fmt.Errorf("amount %q has %d decimal places, but this token has %d", human, len(frac), decimals)
	}
	fracPadded := (frac + strings.Repeat("0", decimals))[:decimals]
	combined := strings.TrimLeft(whole+fracPadded, "0")
	if combined == "" {
		return "0", nil
	}
	return combined, nil
}

// FormatAmount renders a base-unit integer string ("1500000") as a human decimal
// ("1.5") for the given decimals. Trailing zeros in the fraction are trimmed and a
// whole amount has no decimal point. Returns the input unchanged if it isn't a
// non-negative integer string.
//
// decimals is bounded: outside [0, maxDecimals] the input is returned unchanged, as the
// raw base-unit value. FormatAmount is display-only and has no error to return, and the
// decimals it is given usually come from the gateway (payment.Decimals); an unbounded
// value used to drive a one-zero-at-a-time padding loop, so a gateway answering
// decimals: 100000000 hung the caller. Showing the unscaled integer is the honest
// fallback — it is the exact amount, just not scaled — where guessing a scale is not.
func FormatAmount(base string, decimals int) string {
	s := strings.TrimSpace(base)
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return base
	}
	if decimals < 0 || decimals > maxDecimals {
		return base
	}
	if decimals == 0 {
		return s
	}
	if len(s) < decimals+1 {
		s = strings.Repeat("0", decimals+1-len(s)) + s
	}
	whole := s[:len(s)-decimals]
	frac := strings.TrimRight(s[len(s)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
