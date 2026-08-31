// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/painter"
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

// SetGitProgress is best-effort: it is ignored when no op is in flight, clamps the
// fraction, and (while busy) feeds the parsed line + fraction the UI reads.
func TestSetGitProgressGatedAndClamped(t *testing.T) {
	s := newTestState(t, false)

	// Idle: a stray progress update from a late notification is dropped.
	s.SetGitProgress("Receiving objects:  50% (500/1000)", 0.5)
	if s.GitProgressLine() != "" || s.GitProgressFraction() != -1 {
		t.Fatalf("progress leaked while idle: line=%q frac=%v", s.GitProgressLine(), s.GitProgressFraction())
	}

	s.git.beginOp("Pulling")
	// beginOp starts from a clean slate.
	if s.GitProgressLine() != "" || s.git.progressFrac != -1 {
		t.Fatalf("beginOp did not reset progress: line=%q frac=%v", s.GitProgressLine(), s.git.progressFrac)
	}
	s.SetGitProgress("Receiving objects:  45% (450/1000)", 0.45)
	if s.GitProgressLine() != "Receiving objects:  45% (450/1000)" || s.GitProgressFraction() != 0.45 {
		t.Fatalf("progress not recorded: line=%q frac=%v", s.GitProgressLine(), s.GitProgressFraction())
	}
	// Over-range fractions clamp; negatives collapse to the -1 "unknown".
	s.SetGitProgress("x", 2)
	if s.GitProgressFraction() != 1 {
		t.Fatalf("fraction not clamped up: %v", s.GitProgressFraction())
	}
	s.SetGitProgress("y", -3)
	if s.GitProgressFraction() != -1 {
		t.Fatalf("negative fraction not normalised to -1: %v", s.GitProgressFraction())
	}
	// Re-setting the same values must not dirty the frame.
	s.SetGitProgress("y", -1)
	s.dirty = false
	s.SetGitProgress("y", -1)
	if s.dirty {
		t.Fatal("re-setting identical progress should not request a repaint")
	}
	// endOp reports "unknown" again even though the field still holds the last line.
	s.git.endOp()
	if s.GitProgressFraction() != -1 {
		t.Fatalf("progress fraction leaked past endOp: %v", s.GitProgressFraction())
	}
}

// With real object-count progress in flight, the repo-view busy status shows the
// remote's phase line instead of the bare elapsed clock; without it, the #120
// elapsed-seconds fallback still stands.
func TestBusyStatusShowsRemoteProgress(t *testing.T) {
	s := newTestState(t, false)
	s.git.beginOp("Pulling")
	s.Tick(4) // 4s elapsed — the fallback would say "Pulling… 4s"

	s.SetGitProgress("Receiving objects:  70% (700/1000)", 0.70)
	if got, _ := s.sidebar.detailText(); got != "Pulling — Receiving objects:  70% (700/1000)" {
		t.Fatalf("busy status = %q, want the remote phase line", got)
	}
	// A phase that stops streaming (line cleared) falls back to the elapsed clock.
	s.SetGitProgress("", -1)
	if got, _ := s.sidebar.detailText(); got != "Pulling… 4s" {
		t.Fatalf("busy status without progress = %q, want the elapsed fallback", got)
	}
}

// A clone drives the empty-workspace bar from the remote's object count once the
// git client wasm has landed (AssetLoading cleared): the bar goes determinate at
// the parsed fraction, and the loading caption shows the phase line.
func TestCloneBarFollowsRemoteObjectCount(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, testW*testH*4)
	p := painter.NewPixelPainter(buf, testW, testH)

	s.git.beginOp("Cloning")
	// Phase 1: the client wasm is still downloading — the asset bar owns the frac.
	s.SetAssetLoading("git-worker.wasm")
	s.SetAssetProgress(0.4)
	s.sidebar.draw(p, s.theme)
	if s.sidebar.assetBar.Indeterminate || s.sidebar.assetBar.Fraction().Get() != 0.4 {
		t.Fatalf("asset phase bar = ind:%v frac:%v", s.sidebar.assetBar.Indeterminate, s.sidebar.assetBar.Fraction().Get())
	}

	// Phase 2: the client landed; the remote's object count now drives the bar.
	s.SetAssetLoading("")
	s.SetGitProgress("Receiving objects:  60% (600/1000)", 0.60)
	s.sidebar.draw(p, s.theme)
	if s.sidebar.assetBar.Indeterminate {
		t.Fatal("a measured clone transfer should draw a determinate bar")
	}
	if got := s.sidebar.assetBar.Fraction().Get(); got != 0.60 {
		t.Fatalf("clone bar shows %v, want 0.60 from the object count", got)
	}
	if _, caption := s.sidebar.loadingText(); caption != "Receiving objects:  60% (600/1000)" {
		t.Fatalf("clone caption = %q, want the remote phase line", caption)
	}

	// An unmeasured phase (Counting objects, no ratio) slides the bar rather than
	// claiming a fraction.
	s.SetGitProgress("Counting objects: 128", -1)
	s.sidebar.draw(p, s.theme)
	if !s.sidebar.assetBar.Indeterminate {
		t.Fatal("an unmeasured clone phase should slide, not show a fraction")
	}
}
