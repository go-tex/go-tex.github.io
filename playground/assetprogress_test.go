// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/painter"
)

// The workspace is empty for tens of seconds while the git client downloads.
// These tests pin what it says during that wait, and that the wait is measured
// rather than guessed at.

func TestSetAssetProgressClampsAndReports(t *testing.T) {
	s := newTestState(t, false)

	// A named asset starts unmeasured: -1, not a dishonest 0%.
	s.SetAssetLoading("git-worker.wasm")
	if got := s.AssetProgress(); got != -1 {
		t.Fatalf("a freshly named asset reports %v, want -1 (unknown)", got)
	}

	for _, tc := range []struct {
		in, want float64
	}{
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{2, 1},     // clamped up
		{-0.5, -1}, // any negative means "cannot tell"
	} {
		s.SetAssetProgress(tc.in)
		if got := s.AssetProgress(); got != tc.want {
			t.Fatalf("SetAssetProgress(%v) → %v, want %v", tc.in, got, tc.want)
		}
	}

	// Setting the same fraction twice must not dirty the frame.
	s.SetAssetProgress(0.25)
	s.dirty = false
	s.SetAssetProgress(0.25)
	if s.dirty {
		t.Fatal("re-setting the same fraction should not request a repaint")
	}

	// Naming a different asset resets the measurement: the previous one's
	// fraction would show a bar already part-full.
	s.SetAssetProgress(0.9)
	s.SetAssetLoading("")
	if got := s.AssetProgress(); got != -1 {
		t.Fatalf("clearing the asset left progress at %v, want -1", got)
	}
}

func TestSidebarLoadingTextNamesThePhase(t *testing.T) {
	s := newTestState(t, false)

	// Phase 1: the git client itself is still coming. Saying "Cloning…" here
	// would blame a remote we have not contacted yet.
	s.SetAssetLoading("git-worker.wasm")
	msg, caption := s.sidebar.loadingText()
	if !strings.Contains(msg, "git client") {
		t.Fatalf("while the asset downloads the workspace says %q, want it to name the git client", msg)
	}
	if caption != "git-worker.wasm" {
		t.Fatalf("caption is %q, want the bare asset name: the 264 px column does not wrap, so a longer caption is clipped mid-word", caption)
	}

	// Phase 2: the client landed, so the wait now belongs to the operation.
	s.SetAssetLoading("")
	s.git.op.Set("Cloning")
	msg, caption = s.sidebar.loadingText()
	if msg != "Cloning…" {
		t.Fatalf("with the client landed the workspace says %q, want %q", msg, "Cloning…")
	}
	if caption == "" {
		t.Fatal("the operation phase should still say what it is fetching")
	}

	// Busy with nothing named: still honest, never silent.
	s.git.op.Set("")
	if msg, _ = s.sidebar.loadingText(); msg == "" {
		t.Fatal("a busy workspace with no named operation must still say something")
	}
}

func TestSidebarDetailNamesTheOperationInFlight(t *testing.T) {
	s := newTestState(t, false)
	s.git.busy.Set(true)
	s.git.op.Set("Pulling")
	got, _ := s.sidebar.detailText()
	if got != "Pulling…" {
		t.Fatalf("detail line is %q, want %q — with a repo open the tree stays up, so the op must name itself", got, "Pulling…")
	}
	// An idle workspace says nothing rather than leaving a stale operation up.
	s.git.busy.Set(false)
	s.git.op.Set("")
	if got, _ = s.sidebar.detailText(); got != "" {
		t.Fatalf("an idle workspace shows %q, want an empty detail line", got)
	}
}

func TestSidebarDrawsTheAssetProgressBar(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, testW*testH*4)
	p := painter.NewPixelPainter(buf, testW, testH)

	// No repo + busy + a named asset: the download draws its own bar.
	s.git.busy.Set(true)
	s.git.op.Set("Cloning")
	s.SetAssetLoading("git-worker.wasm")
	s.SetAssetProgress(0.5)
	s.sidebar.draw(p, s.theme)
	if s.sidebar.assetBar.Indeterminate {
		t.Fatal("a measured download should draw a determinate bar")
	}
	if got := s.sidebar.assetBar.Fraction().Get(); got != 0.5 {
		t.Fatalf("bar shows %v, want 0.5", got)
	}
	if s.sidebar.assetBar.Bounds().W <= 0 {
		t.Fatal("the bar was never given bounds, so it painted nothing")
	}

	// An unmeasurable download slides instead of claiming a fraction.
	s.SetAssetProgress(-1)
	s.sidebar.draw(p, s.theme)
	if !s.sidebar.assetBar.Indeterminate {
		t.Fatal("an unmeasured download should slide, not show a fraction")
	}
}

func TestGitOpNameClearsWithBusy(t *testing.T) {
	s := newTestState(t, false)
	s.git.beginOp("Pushing")
	if !s.git.busy.Get() || s.git.op.Get() != "Pushing" {
		t.Fatalf("beginOp left busy=%v op=%q", s.git.busy.Get(), s.git.op.Get())
	}
	s.git.endOp()
	if s.git.busy.Get() || s.git.op.Get() != "" {
		t.Fatalf("endOp left busy=%v op=%q — a finished op must stop announcing itself", s.git.busy.Get(), s.git.op.Get())
	}
}

// TestSidebarBusyDrawIsStableAcrossFrames is the regression test for a blank
// workspace. The busy branch moves the empty state down and shrinks it to seat
// the spinner above it; layout only runs on a resize, so a draw that read the
// widget's own bounds back each frame compounded its offset and walked the whole
// prompt off the bottom of the column. On screen that looked like a sidebar that
// simply said nothing while it cloned — the very state it exists to explain.
func TestSidebarBusyDrawIsStableAcrossFrames(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, testW*testH*4)
	p := painter.NewPixelPainter(buf, testW, testH)

	s.git.busy.Set(true)
	s.git.op.Set("Cloning")
	s.SetAssetLoading("git-worker.wasm")
	s.SetAssetProgress(0.3)

	s.sidebar.draw(p, s.theme)
	first, firstSpin, firstBar := s.sidebar.empty.Bounds(), s.sidebar.spinner.Bounds(), s.sidebar.assetBar.Bounds()
	if first.H <= 0 {
		t.Fatal("the empty state was given no height on the first frame")
	}
	for frame := 2; frame <= 30; frame++ {
		s.sidebar.draw(p, s.theme)
		if got := s.sidebar.empty.Bounds(); got != first {
			t.Fatalf("frame %d moved the prompt to %+v, want it to stay at %+v", frame, got, first)
		}
		if got := s.sidebar.spinner.Bounds(); got != firstSpin {
			t.Fatalf("frame %d moved the spinner to %+v, want %+v", frame, got, firstSpin)
		}
		if got := s.sidebar.assetBar.Bounds(); got != firstBar {
			t.Fatalf("frame %d moved the progress bar to %+v, want %+v", frame, got, firstBar)
		}
	}
	// And the prompt must still be inside the column, not below it.
	col := s.sidebar.bounds
	if e := s.sidebar.empty.Bounds(); e.Y+e.H > col.Y+col.H {
		t.Fatalf("the prompt ends at %d, past the column bottom %d", e.Y+e.H, col.Y+col.H)
	}
}
