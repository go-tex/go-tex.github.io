package main

import (
	"context"
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
