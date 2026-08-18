package rail0

import (
	"math/rand/v2"
	"time"
)

// How long to wait before retrying a request the gateway rate-limited.
//
// Its own file, and pure, because two of its decisions are easy to get backwards and
// impossible to notice once they are wrong — a client that waits too little walks straight
// back into the limiter, and one that waits too long looks hung.
//
//  1. JITTER NEVER SHORTENS THE WAIT BELOW WHAT IT IS FOR.
//     On a server-instructed wait it is ADDITIVE: scaling a Retry-After down means retrying
//     before the window the server named has passed, which is a second 429 by construction.
//     So the instruction is honoured in full and a small random tail is added.
//
//     On a guessed wait it is EQUAL jitter — half the delay fixed, half random — not the
//     textbook "full jitter" that multiplies the whole delay by a random factor. Full
//     jitter can land arbitrarily close to zero, which makes a real pause indistinguishable
//     from the bug where a Retry-After of "0" was stored as the delay and the retry fired
//     immediately (see TestRetryAfter_ZeroHeaderStillBacksOff). Keeping a floor spreads the
//     herd just as well and leaves "did we actually wait" observable.
//
//     Why jitter at all when the server named the time: because callers align on it.
//     rail0-admin proxies every merchant over ONE session, so they share the per-session
//     bucket, are told the same Retry-After, and would wake together — recreating the
//     burst the limiter just rejected.
//
//  2. THE CAP IS NOT PARANOIA. A gateway older than rail0-gateway#201 sends the WHOLE
//     throttle period as Retry-After rather than the time remaining in the window, so a
//     limit hit one second in asks for the full 60. The cap bounds that over-wait and any
//     hostile or misconfigured value from anything between the client and the gateway.
//
// Mirrors Rail0::Backoff (rail0-ruby) and core/backoff.ts (rail0-ts): same rules, same
// defaults, so the three SDKs behave the same against the same server.

// DefaultRetryAfterCap bounds how long a single Retry-After may park a request.
const DefaultRetryAfterCap = 60 * time.Second

// throttleDelay returns how long to wait before the next attempt.
//
// retryAfter is the server's instruction; zero or negative means it gave none — and zero is
// the trap, being a real duration: honouring it produces a burst of back-to-back requests
// against the limiter that just asked for a pause. attempt is 1 for the first retry.
// jitter is a factor in [0,1); pass -1 to draw one (tests pass a fixed value).
func throttleDelay(retryAfter, base, cap time.Duration, attempt int, jitter float64) time.Duration {
	if jitter < 0 {
		jitter = rand.Float64()
	}
	if retryAfter > 0 {
		// Honoured in full (clamped), plus a fraction of one base delay so aligned callers
		// do not wake in lockstep.
		wait := retryAfter
		if wait > cap {
			wait = cap
		}
		return wait + time.Duration(jitter*float64(base))
	}
	// Equal jitter: half the delay fixed, half random — see the note above.
	full := float64(base) * float64(int64(1)<<uint(attempt-1))
	backoff := time.Duration(full/2 + (full/2)*jitter)
	if backoff > cap {
		return cap
	}
	return backoff
}
