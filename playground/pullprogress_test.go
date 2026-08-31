// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"
)

// A git operation over a slow network (a pull or push) can run for many seconds
// during which the workspace tree does not change. The host now drives an
// animation-frame loop for the whole time an operation is in flight (GitBusy, not
// only Cloning), and the busy status counts up its elapsed seconds so the wait
// reads as progressing rather than hung.

// Tick accumulates the operation's elapsed time only while a git op is in flight,
// and beginOp resets it so each operation counts from zero.
func TestGitOpElapsedAccumulatesWhileBusy(t *testing.T) {
	s := newTestState(t, false)

	// Idle: Tick does not accumulate.
	s.Tick(3)
	if s.git.opElapsed != 0 {
		t.Fatalf("elapsed advanced while idle: %v", s.git.opElapsed)
	}

	s.git.beginOp("Pulling")
	s.Tick(1.5)
	s.Tick(1.0)
	if got := s.git.opElapsed; got < 2.49 || got > 2.51 {
		t.Fatalf("elapsed after 2.5s of ticks = %v, want ~2.5", got)
	}

	// Ending the op freezes the counter; a fresh op resets it.
	s.git.endOp()
	s.Tick(5)
	if got := s.git.opElapsed; got < 2.49 || got > 2.51 {
		t.Fatalf("elapsed kept advancing after endOp: %v", got)
	}
	s.git.beginOp("Pushing")
	if s.git.opElapsed != 0 {
		t.Fatalf("beginOp did not reset elapsed: %v", s.git.opElapsed)
	}
}

// The sidebar's busy status names the operation, and once it has run a second it
// appends the elapsed seconds — a visibly ticking "Pulling… 3s" instead of a
// static "Pulling…".
func TestBusyStatusShowsElapsedSeconds(t *testing.T) {
	s := newTestState(t, false)
	s.git.beginOp("Pulling")

	// Under a second: just the named operation.
	if got, _ := s.sidebar.detailText(); got != "Pulling…" {
		t.Fatalf("status under 1s = %q, want %q", got, "Pulling…")
	}
	// After a few seconds: the count shows.
	s.Tick(3.2)
	if got, _ := s.sidebar.detailText(); got != "Pulling… 3s" {
		t.Fatalf("status after 3.2s = %q, want %q", got, "Pulling… 3s")
	}
}

// GitBusy reports any operation in flight (the condition the host's animation loop
// now runs on), while Cloning stays specific to the initial no-repo clone.
func TestGitBusyCoversAllOps(t *testing.T) {
	s := newTestState(t, false)
	if s.GitBusy() {
		t.Fatal("GitBusy true with nothing in flight")
	}
	fired := 0
	s.SubscribeGitBusy(func() { fired++ })
	s.git.beginOp("Pulling")
	if !s.GitBusy() {
		t.Fatal("GitBusy false during a pull")
	}
	if fired == 0 {
		t.Fatal("SubscribeGitBusy callback did not fire when the op began")
	}
}
