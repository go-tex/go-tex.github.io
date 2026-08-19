// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strconv"
	"testing"

	"github.com/go-widgets/toolkit"
)

// --- #3 monospace editor font + click accuracy ------------------------------

func TestEditorFontIsMonospaceAndClickAccurate(t *testing.T) {
	SetupText(1)
	s := NewState(testW, testH, false)
	f := s.editor.EffectiveFont()
	if f.Measure("i") != f.Measure("W") || f.Measure("i") != f.Advance() {
		t.Fatalf("editor font is not monospace: Measure(i)=%d Measure(W)=%d Advance=%d",
			f.Measure("i"), f.Measure("W"), f.Advance())
	}
	// With a monospace font, a click over a known column lands on that column
	// (within the half-cell rounding), NOT drifted far away.
	s.editor.SetText("HELLO world here\nsecond line")
	er := s.editor.Bounds()
	adv, gh := f.Advance(), f.Height()
	gutter := f.Measure(strconv.Itoa(len(s.editorLines()))) + 8
	// Click over column 6 (the 'w' of "world") on line 0.
	lx := 4 + gutter + 6*adv + 1
	ly := 4 + gh/2
	s.HandleClick(er.X+lx, er.Y+ly)
	if s.editor.CursorLine().Get() != 0 {
		t.Fatalf("click landed on line %d, want 0", s.editor.CursorLine().Get())
	}
	if c := s.editor.CursorCol().Get(); c < 5 || c > 7 {
		t.Fatalf("click over column 6 landed on column %d (want ~6); proportional-font drift not fixed", c)
	}
}

func TestApplyEditorFontCacheAndError(t *testing.T) {
	SetupText(1)
	s := NewState(testW, testH, false)
	// Cache hit: same scale, font already set -> early return, font unchanged.
	before := s.editor.Font
	s.applyEditorFont()
	if s.editor.Font != before {
		t.Fatalf("applyEditorFont rebuilt the font on a cache hit")
	}
	// Error branch: a bad TTF blob leaves the editor on its current font.
	orig := editorFontTTF
	defer func() { editorFontTTF = orig }()
	editorFontTTF = []byte("not a font")
	s.monoScale = -1 // force a rebuild attempt
	s.applyEditorFont()
	if s.editor.Font != before {
		t.Fatalf("a failed font load should leave the editor font unchanged")
	}
}

// --- #1 visible selection (keyboard) ----------------------------------------

func TestKeyboardShiftSelection(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetText("abcdef\nghijkl")
	s.editor.CursorLine().Set(0)
	s.editor.CursorCol().Set(0)
	s.editor.ClearSelection()

	// Shift+Right thrice selects "abc".
	for i := 0; i < 3; i++ {
		if !s.HandleKeyDown("Shift+ArrowRight") {
			t.Fatalf("Shift+ArrowRight not consumed")
		}
	}
	if !s.editor.HasSelection() {
		t.Fatalf("Shift+ArrowRight did not create a selection")
	}
	if got := s.editor.SelectionText(); got != "abc" {
		t.Fatalf("selection = %q, want abc", got)
	}
	if !s.selecting {
		t.Fatalf("state should be mid keyboard-selection")
	}

	// A plain ArrowRight collapses the selection.
	s.HandleKeyDown("ArrowRight")
	if s.editor.HasSelection() || s.selecting {
		t.Fatalf("plain ArrowRight did not collapse the selection")
	}

	// Shift+End selects to end of line; Shift+Home would select back.
	s.editor.CursorLine().Set(0)
	s.editor.CursorCol().Set(0)
	s.HandleKeyDown("Shift+End")
	if !s.editor.HasSelection() {
		t.Fatalf("Shift+End did not select")
	}

	// A Shift-prefixed NON-navigation key falls through untouched (no panic).
	s.HandleKeyDown("Shift+Enter")

	// A plain non-nav key resets selecting and forwards.
	s.selecting = true
	s.HandleKeyDown("Backspace")
	if s.selecting {
		t.Fatalf("a plain key should end keyboard selection")
	}
}

func TestCaretPixelRoundTrips(t *testing.T) {
	for _, sc := range []float64{1, 2} {
		SetupText(sc)
		s := NewState(testW, testH, false)
		s.editor.SetText("HELLO world here\nsecond line\nthird row now")
		for _, tc := range []struct{ line, col int }{{0, 0}, {0, 6}, {1, 3}, {2, 9}} {
			px, py := s.CaretPixel(tc.line, tc.col)
			s.HandleClick(px, py)
			if s.editor.CursorLine().Get() != tc.line || s.editor.CursorCol().Get() != tc.col {
				t.Fatalf("scale %v: click at CaretPixel(%d,%d) landed on (%d,%d)",
					sc, tc.line, tc.col, s.editor.CursorLine().Get(), s.editor.CursorCol().Get())
			}
		}
	}
	SetupText(1)
}

func TestHandleCharReplacesSelection(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetText("abcdef")
	s.editor.CursorLine().Set(0)
	s.editor.CursorCol().Set(0)
	s.HandleKeyDown("Shift+End") // select the whole line
	if !s.editor.HasSelection() {
		t.Fatalf("precondition: expected a selection")
	}
	if !s.HandleChar("Z") {
		t.Fatalf("HandleChar not consumed")
	}
	if s.editor.HasSelection() {
		t.Fatalf("typing over a selection should clear it")
	}
	if txt := s.editorLines()[0]; txt != "Z" {
		t.Fatalf("typed char did not replace the selection: %q", txt)
	}
}

func TestNavBaseAndSelectionAccessors(t *testing.T) {
	if navBase("ArrowLeft") != "ArrowLeft" || navBase("Home") != "Home" {
		t.Fatalf("navBase should echo navigation keys")
	}
	if navBase("Enter") != "" || navBase("x") != "" {
		t.Fatalf("navBase should reject non-navigation keys")
	}
	s := newTestState(t, false)
	if s.CursorLine() != s.editor.CursorLine().Get() || s.CursorCol() != s.editor.CursorCol().Get() {
		t.Fatalf("cursor accessors mismatch")
	}
	s.editor.SelectAll()
	if !s.HasSelection() || s.SelectionText() == "" {
		t.Fatalf("selection accessors mismatch after SelectAll")
	}
}

// --- #4 continuous vs paginated render (PagedView-owned) --------------------

// TestStatePaginationIntrospection drives the render pane's mode / current-page /
// visible-page introspection, which now reads the PagedView's MVVM observables.
func TestStatePaginationIntrospection(t *testing.T) {
	s := newTestState(t, false)
	s.SetSource(multiPageDoc())
	if s.RenderMode() != int(toolkit.PagedContinuous) {
		t.Fatalf("default render mode should be continuous")
	}
	if s.RenderVisiblePages() < 2 {
		t.Fatalf("continuous should show all pages, got %d", s.RenderVisiblePages())
	}
	// Switch to paginated via the PagedView's Mode observable: one page shown.
	s.renderView.Mode().Set(toolkit.PagedPaginated)
	if s.RenderVisiblePages() != 1 || s.RenderCurrentPage() != 1 {
		t.Fatalf("paginated: visible=%d page=%d", s.RenderVisiblePages(), s.RenderCurrentPage())
	}
	// A PageDown through the focused viewer advances the 1-based page.
	s.renderView.SetFocused(true)
	if !s.HandleKeyDown("PageDown") {
		t.Fatalf("PageDown not consumed while the viewer is focused")
	}
	if s.RenderCurrentPage() != 2 {
		t.Fatalf("PageDown did not advance the 1-based page: %d", s.RenderCurrentPage())
	}
	// An empty document reports zero visible pages in paginated mode.
	s.SetSource(`\documentclass{article}\begin{document}\end{document}`)
	if s.RenderVisiblePages() != 0 {
		t.Fatalf("empty paginated visiblePages = %d, want 0", s.RenderVisiblePages())
	}
}
