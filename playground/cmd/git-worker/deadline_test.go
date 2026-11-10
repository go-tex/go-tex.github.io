package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ⛔ The property that matters is not the number, it is that the context ENDS.
// Every remote operation in the worker used context.Background(), which cannot be
// cancelled, so a fetch that never settled parked its goroutine for the life of
// the page — and the page giving up (the main thread's idle deadline) does not
// unpark it, so a retry queued behind an operation that would never finish.
func TestNetworkContextAlwaysEnds(t *testing.T) {
	ctx, cancel := networkContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("networkContext returned a context with no deadline; a wedged fetch would park its goroutine for ever")
	}
	if left := time.Until(deadline); left <= 0 {
		t.Fatalf("the context was already expired (%v left)", left)
	}
	// Cancelling must actually release it, so the deferred cancel in each caller
	// is not decoration.
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not end the context")
	}
	if err := ctx.Err(); err == nil {
		t.Error("a cancelled context reports no error")
	}
}

// Bounded at both ends, and for opposite reasons: too tight cuts off a clone that
// was working — the very mistake the main thread's deadline avoids by watching
// for SILENCE rather than counting elapsed time — and unbounded is the defect
// being fixed. go-git's API here offers no silence hook, so the honest substitute
// is a bound generous enough that reaching it means something is truly wrong.
func TestNetworkDeadlineIsGenerousButFinite(t *testing.T) {
	if networkDeadline < 2*time.Minute {
		t.Errorf("networkDeadline = %v, too tight: a large but working clone would be cut off, which is worse than the hang", networkDeadline)
	}
	if networkDeadline > time.Hour {
		t.Errorf("networkDeadline = %v, so long that the worker is wedged for practical purposes", networkDeadline)
	}
}

// ⛔ The store's bound is the one that wedges the WHOLE worker, not just its own
// operation: requests are drained by one goroutine on purpose (main.go serialises
// a worktree that is not concurrency-safe), so a Cache API promise that never
// settles leaves every later request unhandled behind it. #212's origin trace is
// that state from outside — "not one request arrived" while the clone's reply
// never came, which is a clone that was never executed rather than one that
// failed.
func TestStoreDeadlineIsShortBecauseTheStoreIsLocal(t *testing.T) {
	// Opening a cache and reading one entry is sub-millisecond when it works, so a
	// browser that cannot answer in seconds has no storage to offer. Short, because
	// the whole session waits behind it.
	if storeDeadline > 15*time.Second {
		t.Errorf("storeDeadline = %v: far too long for a LOCAL store, and every later request queues behind it", storeDeadline)
	}
	if storeDeadline < time.Second {
		t.Errorf("storeDeadline = %v: tight enough to lose a saved workspace on a slow but working browser", storeDeadline)
	}
	// ⛔ It must be well inside the main thread's own idle deadline, or the page
	// gives up first and the worker's answer arrives too late to be read.
	if storeDeadline >= networkDeadline {
		t.Errorf("storeDeadline %v must be far below networkDeadline %v", storeDeadline, networkDeadline)
	}
}

// Not answering and answering "no" are different, and the error says so: a
// rejection is the browser refusing, which openCache already handles as "this
// browser has no storage to offer"; this one is the browser never replying, which
// is what used to take the worker down with it.
func TestUnresponsiveStoreErrorIsDistinctAndNamesTheWait(t *testing.T) {
	if errStoreUnresponsive == nil {
		t.Fatal("errStoreUnresponsive is nil")
	}
	msg := errStoreUnresponsive.Error()
	if !strings.Contains(msg, "did not answer") {
		t.Errorf("errStoreUnresponsive = %q, want it to say the store did not answer", msg)
	}
	if !strings.Contains(msg, storeDeadline.String()) {
		t.Errorf("errStoreUnresponsive = %q, want it to name the wait (%v)", msg, storeDeadline)
	}
	// Distinct from a rejection, whose message comes from JavaScript's own String().
	if strings.Contains(msg, "rejected") {
		t.Errorf("errStoreUnresponsive = %q reads like a rejection; the two must not be confused", msg)
	}
}

// A context already past its deadline must be refused by anything that checks it
// — this pins the contract the callers rely on, that ctx.Err() is non-nil once
// the bound is reached rather than the operation running on regardless.
func TestAnExpiredNetworkContextReportsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("a one-nanosecond context never finished")
	}
	if ctx.Err() == nil {
		t.Error("an expired context reports no error")
	}
}
