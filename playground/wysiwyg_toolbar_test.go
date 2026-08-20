// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// TestToolbarVisibilityAndLayout asserts the formatting toolbar shows only on the
// WYSIWYG tab, sits directly under the Source│WYSIWYG tab strip and above the
// RichEditor (reserving its measured height), and exposes 12 buttons whose rects
// all fall inside the strip.
func TestToolbarVisibilityAndLayout(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)

	// Source tab: the toolbar is hidden and its rect is zero-height.
	if s.RichToolbarVisible() {
		t.Fatal("toolbar visible on the Source tab")
	}
	if r := s.RichToolbarRect(); r[3] != 0 {
		t.Fatalf("hidden toolbar rect should be zero-height, got %v", r)
	}
	// toolbarHeight / layoutBounds on the hidden (Source) path: no strip reserved.
	w := s.wysiwyg()
	if h := w.toolbarHeight(); h != 0 {
		t.Fatalf("hidden toolbarHeight = %d, want 0", h)
	}
	if tb, ed := w.layoutBounds(); tb.H != 0 || ed != s.editor.Bounds() {
		t.Fatalf("hidden layoutBounds = (%v,%v), want zero toolbar + full editor %v", tb, ed, s.editor.Bounds())
	}

	// Activate WYSIWYG.
	s.SetEditorTab(tabWysiwyg)
	if !s.RichToolbarVisible() {
		t.Fatal("toolbar not visible on the WYSIWYG tab")
	}
	if n := s.RichToolbarButtonCount(); n != 12 {
		t.Fatalf("button count = %d, want 12", n)
	}

	tbR := s.RichToolbarRect()
	if tbR[3] <= 0 {
		t.Fatalf("visible toolbar rect should have positive height, got %v", tbR)
	}
	// Directly under the tab strip: the strip's bottom edge is the toolbar's top.
	if got, want := tbR[1], w.strip.Y+w.strip.H; got != want {
		t.Fatalf("toolbar top = %d, want strip bottom %d", got, want)
	}
	// Above the RichEditor: the toolbar's bottom edge is the editor's top, and the
	// editor was pushed down by exactly the reserved height.
	er := w.editor.Bounds()
	if got, want := tbR[1]+tbR[3], er.Y; got != want {
		t.Fatalf("toolbar bottom = %d, want editor top %d", got, want)
	}
	if got, want := er.Y, s.editor.Bounds().Y+tbR[3]; got != want {
		t.Fatalf("RichEditor top = %d, want CodeEditor top + toolbar height %d", got, want)
	}

	// Every button rect lands inside the toolbar strip, in left-to-right order.
	rects := s.RichToolbarButtonRects()
	if len(rects) != 12 {
		t.Fatalf("button rects = %d, want 12", len(rects))
	}
	strip := toolkit.Rect{X: tbR[0], Y: tbR[1], W: tbR[2], H: tbR[3]}
	prevRight := strip.X - 1
	for i, r := range rects {
		bx := toolkit.Rect{X: r[0], Y: r[1], W: r[2], H: r[3]}
		if bx.W <= 0 || bx.H <= 0 {
			t.Fatalf("button %d has empty rect %v", i, r)
		}
		if bx.X < strip.X || bx.X+bx.W > strip.X+strip.W || bx.Y < strip.Y || bx.Y+bx.H > strip.Y+strip.H {
			t.Fatalf("button %d rect %v escapes the toolbar strip %v", i, r, strip)
		}
		if bx.X <= prevRight {
			t.Fatalf("button %d x=%d not to the right of the previous (%d)", i, bx.X, prevRight)
		}
		prevRight = bx.X
	}

	// Painting the WYSIWYG tab paints the toolbar strip above the RichEditor.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// Back to Source: the toolbar is gone again.
	s.SetEditorTab(tabSource)
	if s.RichToolbarVisible() {
		t.Fatal("toolbar still visible after returning to Source")
	}
}

// TestToolbarClickDrivesEditor drives the toolbar through the REAL pointer path
// (HandleClick at a button's device-pixel centre, i.e. through wysiwygClick's
// toolbar routing) and asserts the click reaches the editor verb and lights the
// button: Bold makes the selection Strong, an H2 changes the caret block, a
// bullet-list button wraps it in a list.
func TestToolbarClickDrivesEditor(t *testing.T) {
	s := newWysState(t)
	s.SetSource("\\section{Title}\n\nPlain paragraph.\n")
	s.SetEditorTab(tabWysiwyg)
	if !s.WysiwygActive() {
		t.Fatal("WYSIWYG did not activate")
	}
	if s.RichHasBold() {
		t.Fatal("precondition: the seeded document has no bold")
	}

	// Select the plain paragraph (block 1: block 0 is the heading).
	s.RichSelectBlock(1)

	click := func(idx int) {
		r := s.RichToolbarButtonRects()[idx]
		cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
		if !s.HandleClick(cx, cy) {
			t.Fatalf("HandleClick on toolbar button %d not consumed (rect %v)", idx, r)
		}
	}

	// Bold: the selection becomes Strong and the Bold button lights.
	click(rtbBold)
	if !s.RichHasBold() {
		t.Fatal("Bold click did not add a Strong to the document")
	}
	if !s.RichToolbarButtonPressed(rtbBold) {
		t.Fatal("Bold button not pressed after bolding the selection")
	}

	// H2: the caret block turns into a level-2 heading; H2 lights, Paragraph does not.
	click(rtbH2)
	if got := s.RichCurrentBlockKind(); got != int(toolkit.BlockH2) {
		t.Fatalf("after H2 click, block kind = %d, want %d", got, int(toolkit.BlockH2))
	}
	if !s.RichToolbarButtonPressed(rtbH2) || s.RichToolbarButtonPressed(rtbParagraph) {
		t.Fatalf("H2 pressed=%v, Paragraph pressed=%v; want true/false",
			s.RichToolbarButtonPressed(rtbH2), s.RichToolbarButtonPressed(rtbParagraph))
	}

	// Bullet list: the caret block is wrapped into an unordered list; Bullet lights.
	click(rtbBullet)
	if !s.RichToolbarButtonPressed(rtbBullet) {
		t.Fatal("Bullet button not pressed after wrapping the block in a list")
	}

	// Leaving to Source writes the edited document back, so the new bold survives
	// the round-trip as a \textbf in the LaTeX source.
	s.SetEditorTab(tabSource)
	if s.RichToolbarVisible() {
		t.Fatal("toolbar visible after leaving to Source")
	}
	if !strings.Contains(s.Source(), "\\textbf") {
		t.Fatalf("written-back source lost the bold:\n%s", s.Source())
	}
}

// TestToolbarButtonPressedOutOfRange covers the guarded index path.
func TestToolbarButtonPressedOutOfRange(t *testing.T) {
	s := newWysState(t)
	s.SetEditorTab(tabWysiwyg)
	if s.RichToolbarButtonPressed(-1) || s.RichToolbarButtonPressed(999) {
		t.Fatal("out-of-range button index should report not-pressed")
	}
}

// TestToolbarHeightClampsToTinyPane covers layoutBounds' clamp when the editor
// pane is shorter than the toolbar's measured height: the strip takes the whole
// pane and the RichEditor collapses to zero height rather than going negative.
func TestToolbarHeightClampsToTinyPane(t *testing.T) {
	s := newWysState(t)
	s.SetSource("x")
	s.SetEditorTab(tabWysiwyg)
	w := s.wysiwyg()

	// Force the editor pane far shorter than the toolbar and re-apply the bounds.
	s.editor.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 300, H: 5})
	w.applyWysiwygBounds()

	if w.toolbarRect.H != 5 {
		t.Fatalf("clamped toolbar height = %d, want 5 (the whole tiny pane)", w.toolbarRect.H)
	}
	if got := w.editor.Bounds().H; got != 0 {
		t.Fatalf("RichEditor height = %d, want 0 (pane fully consumed by the clamp)", got)
	}
}
