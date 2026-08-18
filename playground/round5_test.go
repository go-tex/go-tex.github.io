// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strconv"
	"testing"

	"github.com/go-widgets/painter"
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
	gutter := f.Measure(strconv.Itoa(len(s.editor.Lines))) + 8
	// Click over column 6 (the 'w' of "world") on line 0.
	lx := 4 + gutter + 6*adv + 1
	ly := 4 + gh/2
	s.HandleClick(er.X+lx, er.Y+ly)
	if s.editor.CursorLine != 0 {
		t.Fatalf("click landed on line %d, want 0", s.editor.CursorLine)
	}
	if c := s.editor.CursorCol; c < 5 || c > 7 {
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
	s.editor.CursorLine, s.editor.CursorCol = 0, 0
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
	s.editor.CursorLine, s.editor.CursorCol = 0, 0
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
			if s.editor.CursorLine != tc.line || s.editor.CursorCol != tc.col {
				t.Fatalf("scale %v: click at CaretPixel(%d,%d) landed on (%d,%d)",
					sc, tc.line, tc.col, s.editor.CursorLine, s.editor.CursorCol)
			}
		}
	}
	SetupText(1)
}

func TestHandleCharReplacesSelection(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetText("abcdef")
	s.editor.CursorLine, s.editor.CursorCol = 0, 0
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
	if txt := s.editor.Lines[0]; txt != "Z" {
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
	if s.CursorLine() != s.editor.CursorLine || s.CursorCol() != s.editor.CursorCol {
		t.Fatalf("cursor accessors mismatch")
	}
	s.editor.SelectAll()
	if !s.HasSelection() || s.SelectionText() == "" {
		t.Fatalf("selection accessors mismatch after SelectAll")
	}
}

// --- #4 continuous vs paginated render --------------------------------------

func fakePages(n int) (stacked []byte, w, h int, pages []pageImg) {
	w, h = 20, 12
	pages = make([]pageImg, n)
	for i := range pages {
		pages[i] = pageImg{pixels: make([]byte, w*h*4), w: w, h: h}
	}
	stacked = make([]byte, w*(h*n)*4)
	return stacked, w, h * n, pages
}

func TestRenderViewPaginationToggle(t *testing.T) {
	SetupText(1)
	rv := newRenderView()
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 300, H: 400})
	stacked, w, h, pages := fakePages(6)
	rv.SetPages(stacked, w, h, pages)

	// Default is continuous: all pages visible, base height == stacked height.
	if rv.mode != renderContinuous || rv.visiblePages() != 6 || rv.pageCount() != 6 {
		t.Fatalf("default mode wrong: mode=%d visible=%d count=%d", rv.mode, rv.visiblePages(), rv.pageCount())
	}
	if rv.baseH != h {
		t.Fatalf("continuous base height = %d, want %d", rv.baseH, h)
	}

	// Toggle to paginated: exactly one page shown, current page 1.
	rv.setMode(renderPaginated)
	if rv.mode != renderPaginated || rv.visiblePages() != 1 {
		t.Fatalf("paginated: visiblePages=%d, want 1", rv.visiblePages())
	}
	if !rv.pageBtn.Selected || rv.contBtn.Selected {
		t.Fatalf("mode toggle Selected states not updated")
	}
	if rv.currentPage() != 0 {
		t.Fatalf("current page = %d, want 0", rv.currentPage())
	}
	if rv.baseH != pages[0].h {
		t.Fatalf("paginated base height = %d, want one page %d", rv.baseH, pages[0].h)
	}

	// Next advances; clamps at the last page.
	rv.step(+1)
	if rv.currentPage() != 1 {
		t.Fatalf("after Next: page %d, want 1", rv.currentPage())
	}
	for i := 0; i < 20; i++ {
		rv.step(+1)
	}
	if rv.currentPage() != 5 {
		t.Fatalf("Next past the end should clamp to 5, got %d", rv.currentPage())
	}
	// Prev steps back and clamps at 0.
	for i := 0; i < 20; i++ {
		rv.step(-1)
	}
	if rv.currentPage() != 0 {
		t.Fatalf("Prev past the start should clamp to 0, got %d", rv.currentPage())
	}

	// Back to continuous restores all pages.
	rv.setMode(renderContinuous)
	if rv.visiblePages() != 6 || rv.baseH != h {
		t.Fatalf("back to continuous did not restore the stacked view")
	}

	// step is a no-op in continuous mode and with no pages.
	rv.step(+1)
	if rv.currentPage() != 0 {
		t.Fatalf("step in continuous mode should be a no-op")
	}
	empty := newRenderView()
	empty.step(+1)                 // no pages: no panic, no-op
	empty.setMode(renderPaginated) // paginated with zero pages
	if empty.visiblePages() != 0 {
		t.Fatalf("empty paginated visiblePages should be 0, got %d", empty.visiblePages())
	}
}

func TestRenderViewPaginatedClickAndDraw(t *testing.T) {
	SetupText(1)
	rv := newRenderView()
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 320, H: 400})
	stacked, w, h, pages := fakePages(4)
	rv.SetPages(stacked, w, h, pages)

	// Click the "Page" mode button through the router.
	pb := rv.pageBtn.Bounds()
	if !rv.click(pb.X+pb.W/2, pb.Y+pb.H/2) {
		t.Fatalf("mode button click not consumed")
	}
	if rv.mode != renderPaginated {
		t.Fatalf("clicking Page did not switch to paginated")
	}
	// Click Next through the router (only hit in paginated mode).
	nb := rv.nextBtn.Bounds()
	if !rv.click(nb.X+nb.W/2, nb.Y+nb.H/2) {
		t.Fatalf("Next click not consumed")
	}
	if rv.currentPage() != 1 {
		t.Fatalf("Next via click did not advance: page %d", rv.currentPage())
	}
	// Click Prev.
	pv := rv.prevBtn.Bounds()
	rv.click(pv.X+pv.W/2, pv.Y+pv.H/2)
	if rv.currentPage() != 0 {
		t.Fatalf("Prev via click did not go back: page %d", rv.currentPage())
	}

	// Draw while PAGINATED (exercises the prev/next buttons + chevron icons + the
	// "n / N" indicator).
	buf := make([]byte, 320*400*4)
	p := painter.NewPixelPainter(buf, 320, 400)
	th := toolkit.DefaultLight()
	rv.Draw(p, th)

	// Click the "Cont" mode button (fires its OnClick closure), then draw
	// continuous.
	cb := rv.contBtn.Bounds()
	rv.click(cb.X+cb.W/2, cb.Y+cb.H/2)
	if rv.mode != renderContinuous {
		t.Fatalf("clicking Cont did not switch to continuous")
	}
	rv.Draw(p, th)
}

func TestRenderViewSetPagesClampsStalePage(t *testing.T) {
	rv := newRenderView()
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 300, H: 400})
	_, w, h, pages := fakePages(6)
	stacked6, _, _, _ := fakePages(6)
	rv.SetPages(stacked6, w, h, pages)
	rv.setMode(renderPaginated)
	rv.step(+1)
	rv.step(+1) // page 2
	// Recompile to a 1-page doc: the stale current page clamps back to 0.
	_, w1, h1, pages1 := fakePages(1)
	stacked1, _, _, _ := fakePages(1)
	rv.SetPages(stacked1, w1, h1, pages1)
	if rv.currentPage() != 0 {
		t.Fatalf("stale current page not clamped: %d", rv.currentPage())
	}
}

func TestStatePaginationIntrospection(t *testing.T) {
	s := newTestState(t, false)
	s.SetSource(multiPageDoc())
	if s.RenderMode() != renderContinuous {
		t.Fatalf("default render mode should be continuous")
	}
	if s.RenderVisiblePages() < 2 {
		t.Fatalf("continuous should show all pages, got %d", s.RenderVisiblePages())
	}
	// Switch to paginated via the render view; assert one page and stepping.
	s.renderView.setMode(renderPaginated)
	if s.RenderVisiblePages() != 1 || s.RenderCurrentPage() != 1 {
		t.Fatalf("paginated: visible=%d page=%d", s.RenderVisiblePages(), s.RenderCurrentPage())
	}
	s.renderView.step(+1)
	if s.RenderCurrentPage() != 2 {
		t.Fatalf("Next did not advance the 1-based page: %d", s.RenderCurrentPage())
	}
	// DebugRects exposes the new controls.
	r := s.DebugRects()
	for _, name := range []string{"modeCont", "modePage", "prevPage", "nextPage"} {
		if v, ok := r[name]; !ok || v[2] <= 0 {
			t.Fatalf("DebugRects missing/empty %q: %v", name, v)
		}
	}
}

func TestDrawChevron(t *testing.T) {
	buf := make([]byte, 40*40*4)
	p := painter.NewPixelPainter(buf, 40, 40)
	ink := toolkit.RGB(0, 0, 0)
	drawChevron(p, toolkit.Rect{X: 0, Y: 0, W: 20, H: 20}, ink, false) // <
	drawChevron(p, toolkit.Rect{X: 0, Y: 0, W: 20, H: 20}, ink, true)  // >
	// A painted pixel proves it drew something.
	nonblank := false
	for i := 0; i+3 < len(buf); i += 4 {
		if buf[i+3] != 0 {
			nonblank = true
			break
		}
	}
	if !nonblank {
		t.Fatalf("drawChevron painted nothing")
	}
}
