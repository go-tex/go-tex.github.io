// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-widgets/toolkit"
)

// samplePixel reads the RGBA at device pixel (x, y) from a testW-wide frame the
// State drew — the read-back the press/hover-feedback proofs assert on.
func samplePixel(buf []byte, x, y int) toolkit.RGBA {
	i := (y*testW + x) * 4
	return toolkit.RGBA{R: buf[i], G: buf[i+1], B: buf[i+2], A: buf[i+3]}
}

// TestCollabPressedButtonScreenshot renders the REAL Collaborate panel with its
// Shuffle button held down (a genuine mousedown routed through HandleClick, not
// released) and encodes it to PNG — the visible proof that a panel button now
// shows a depressed (Accent) face. It always exercises the render+encode path;
// it writes the file only when GOTEX_SCREENSHOT_DIR is set, matching
// TestGitPanelScreenshot, so a plain `go test` never litters the tree:
//
//	GOTEX_SCREENSHOT_DIR=. go test -run TestCollabPressedButtonScreenshot ./...
func TestCollabPressedButtonScreenshot(t *testing.T) {
	const w, h = 1200, 1000
	SetupText(2) // crisp at deviceScaleFactor 2, matching the browser driver
	defer SetupText(1)

	s := NewState(w, h, false)
	s.CompilePending()
	s.SetCollabOpen(true)

	buf := make([]byte, w*h*4)
	s.Draw(buf)
	sh, ok := s.CollabButtonRects()["shuffle"]
	if !ok {
		t.Fatal("shuffle button rect not exposed while the panel is open")
	}
	// Hold the button down (no release), so the frame we encode shows it pressed.
	if !s.HandleClick(sh[0]+sh[2]/2, sh[1]+sh[3]/2) {
		t.Fatal("the Shuffle press was not consumed")
	}
	s.Draw(buf)

	img := &image.RGBA{Pix: buf, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}

	dir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if dir == "" {
		return // render+encode proven above; skip writing on the plain lane
	}
	out := filepath.Join(dir, "collab-press-proof.png")
	fp, err := os.Create(out)
	if err != nil {
		t.Fatalf("create %s: %v", out, err)
	}
	defer func() { _ = fp.Close() }()
	if err := png.Encode(fp, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	t.Logf("wrote pressed-button screenshot to %s", out)
}

// TestPanelHandleMoveClosedNoop covers the defensive early return: a move
// delivered while a panel is closed does nothing (the app only routes moves to an
// open, modal panel, but the guard keeps the method safe to call regardless).
func TestPanelHandleMoveClosedNoop(t *testing.T) {
	s := newTestState(t, false)
	s.collab.open = false
	s.collab.handleMove(10, 10) // no panic, no state change
	s.git.open = false
	s.git.handleMove(10, 10)
}

// faceSample returns a point on a button's plain fill — a few pixels in from the
// left edge, vertically centred — so the sample lands on the face colour rather
// than the centred label glyphs or the rounded corners.
func faceSample(r [4]int) (int, int) { return r[0] + 3, r[1] + r[3]/2 }

// TestCollabButtonPressHoverFeedback is the regression proof for the reported
// bug: the Collaborate panel's buttons now depress (and light on hover) when the
// pointer interacts with them, because they are persistent toolkit Buttons that
// receive the real mousedown/move/up through the toolkit — the feedback that was
// entirely absent while the panel hand-drew throwaway buttons every frame.
//
// It drives the app's own public pointer entry points (HandleClick / HandleMove
// / HandleRelease), so it also exercises the modal routing added to app.go.
func TestCollabButtonPressHoverFeedback(t *testing.T) {
	s := newTestState(t, false)
	s.SetCollabOpen(true)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// The Shuffle button stays present across its own action (it only re-rolls the
	// identity, leaving the panel idle), so it is the stable target for the
	// press/hover pixel proof.
	sh, ok := s.CollabButtonRects()["shuffle"]
	if !ok {
		t.Fatal("shuffle button rect not exposed while the panel is open")
	}
	px, py := faceSample(sh)

	// At rest the face is the plain Surface, NOT the pressed Accent.
	if rest := samplePixel(buf, px, py); rest == s.theme.Accent {
		t.Fatalf("shuffle already shows the pressed Accent face at rest: %+v", rest)
	}

	// Hover: moving the pointer over the open modal panel raises the hover face
	// (SurfaceAlt), distinct from the resting Surface.
	if !s.HandleMove(px, py) {
		t.Fatal("HandleMove over the open Collaborate panel should be consumed")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got != s.theme.SurfaceAlt {
		t.Fatalf("hovered shuffle face = %+v, want SurfaceAlt %+v", got, s.theme.SurfaceAlt)
	}

	// Press: the mousedown depresses the button — the previously-missing feedback.
	if !s.HandleClick(px, py) {
		t.Fatal("a press on the shuffle button was not consumed")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got != s.theme.Accent {
		t.Fatalf("pressed shuffle face = %+v, want the pressed Accent %+v", got, s.theme.Accent)
	}

	// Release: the mouseup clears the pressed face (it is no longer Accent).
	if !s.HandleRelease(px, py) {
		t.Fatal("the release was not consumed by the Collaborate panel")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got == s.theme.Accent {
		t.Fatalf("released shuffle face still shows the pressed Accent %+v", got)
	}
}

// TestCollabButtonPressFiresActionThroughToolkit proves the action still fires
// through the toolkit press path: pressing Host (via the real HandleClick) runs
// the button's OnClick → dispatch → CollabHost, advancing the phase. It also
// covers the release after an action that changed the panel's structure.
func TestCollabButtonPressFiresActionThroughToolkit(t *testing.T) {
	s, f, _ := withFake(t)
	f.offer = "OFFER"
	s.SetCollabOpen(true)
	s.Draw(make([]byte, testW*testH*4))

	hr, ok := s.CollabButtonRects()["host"]
	if !ok {
		t.Fatal("host button rect not exposed while idle")
	}
	if !s.HandleClick(hr[0]+hr[2]/2, hr[1]+hr[3]/2) {
		t.Fatal("the Host press was not consumed")
	}
	if s.CollabPhase() != int(phaseHostWait) {
		t.Fatalf("pressing Host did not fire the action through the toolkit: phase=%d", s.CollabPhase())
	}
	// The release is still delivered even though the action rebuilt the panel.
	if !s.HandleRelease(hr[0], hr[1]) {
		t.Fatal("the release after the Host action was not consumed")
	}
}

// TestGitButtonPressHoverFeedback is the same proof for the Remote-Git panel:
// its Clone button depresses on press and lights on hover, driven through the
// toolkit, and the press fires the clone action (which fails with the no-browser
// error on a native build — proof the action ran).
func TestGitButtonPressHoverFeedback(t *testing.T) {
	s := newTestState(t, false)
	s.SetGitOpen(true)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// Locate the Clone button (always present) from the laid-out items.
	v := s.git
	v.layout()
	var clone toolkit.Rect
	for _, b := range v.buttons {
		if b.role == gitRoleClone {
			clone = b.rect
		}
	}
	if clone.W == 0 {
		t.Fatal("the Clone button was not laid out")
	}
	px, py := clone.X+3, clone.Y+clone.H/2

	s.Draw(buf)
	if rest := samplePixel(buf, px, py); rest == s.theme.Accent {
		t.Fatalf("Clone already shows the pressed Accent face at rest: %+v", rest)
	}

	// Hover feedback.
	if !s.HandleMove(px, py) {
		t.Fatal("HandleMove over the open Git panel should be consumed")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got != s.theme.SurfaceAlt {
		t.Fatalf("hovered Clone face = %+v, want SurfaceAlt %+v", got, s.theme.SurfaceAlt)
	}

	// Press: depresses + fires the clone action (which sets an error natively).
	if !s.HandleClick(px, py) {
		t.Fatal("a press on the Clone button was not consumed")
	}
	if s.GitError() == "" {
		t.Fatal("pressing Clone did not fire the clone action")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got != s.theme.Accent {
		t.Fatalf("pressed Clone face = %+v, want the pressed Accent %+v", got, s.theme.Accent)
	}

	// Release clears the pressed face.
	if !s.HandleRelease(px, py) {
		t.Fatal("the release was not consumed by the Git panel")
	}
	s.Draw(buf)
	if got := samplePixel(buf, px, py); got == s.theme.Accent {
		t.Fatalf("released Clone face still shows the pressed Accent %+v", got)
	}
}
