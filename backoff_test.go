package rail0

import (
	"testing"
	"time"
)

// The two decisions that are easy to get backwards, and invisible once they are: a client
// that waits too little walks back into the limiter, one that waits too long looks hung.
//
// Mirrors spec/backoff_spec.rb (rail0-ruby) and test/backoff.test.ts (rail0-ts).

func TestThrottleDelay_HonoursAnInstructedWaitInFullAndOnlyAddsJitter(t *testing.T) {
	// Scaling the gateway's own number down (textbook full jitter) means retrying before
	// the window it named has passed — a second 429 by construction.
	got := throttleDelay(30*time.Second, 200*time.Millisecond, time.Minute, 1, 0.5)
	if want := 30*time.Second + 100*time.Millisecond; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got <= 30*time.Second {
		t.Errorf("an instructed wait must never be shortened: got %v", got)
	}
}

func TestThrottleDelay_CapsAnInstructedWait(t *testing.T) {
	// A gateway older than rail0-gateway#201 sends the whole period, not the time left.
	if got := throttleDelay(time.Hour, 200*time.Millisecond, time.Minute, 1, 0); got != time.Minute {
		t.Errorf("got %v, want %v", got, time.Minute)
	}
}

func TestThrottleDelay_TreatsNoInstructionAsAGuess(t *testing.T) {
	// Zero IS a real duration, which is the trap: honouring it produces a burst of
	// back-to-back requests against the limiter that just asked for a pause.
	for _, retryAfter := range []time.Duration{0, -5 * time.Second} {
		got := throttleDelay(retryAfter, 500*time.Millisecond, time.Minute, 1, 1)
		if got != 500*time.Millisecond {
			t.Errorf("retryAfter %v: got %v, want the base delay", retryAfter, got)
		}
	}
}

func TestThrottleDelay_GuessedWaitKeepsAFloorOfHalf(t *testing.T) {
	// EQUAL jitter, not full: a wait that can land near zero is indistinguishable from the
	// bug where a "0" Retry-After was stored as the delay and the retry fired immediately.
	base := 200 * time.Millisecond
	if got := throttleDelay(0, base, time.Minute, 1, 0); got != 100*time.Millisecond {
		t.Errorf("no jitter: got %v, want half the base", got)
	}
	if got := throttleDelay(0, base, time.Minute, 1, 1); got != base {
		t.Errorf("full jitter: got %v, want the base", got)
	}
	for _, jitter := range []float64{0, 0.25, 0.5, 0.99} {
		if got := throttleDelay(0, base, time.Minute, 3, jitter); got < 400*time.Millisecond {
			t.Errorf("attempt 3 jitter %v: got %v, want >= half of 800ms", jitter, got)
		}
	}
	if got := throttleDelay(0, base, time.Minute, 20, 1); got != time.Minute {
		t.Errorf("attempt 20: got %v, want the cap", got)
	}
}

func TestThrottleDelay_DrawsItsOwnJitterAndNeverDipsBelowTheInstruction(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 20; i++ {
		got := throttleDelay(10*time.Second, time.Second, time.Minute, 1, -1)
		if got < 10*time.Second || got > 11*time.Second {
			t.Fatalf("out of range: %v", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Error("jitter must actually vary")
	}
}
