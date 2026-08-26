// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strconv"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// multiLinePageDoc is a document with one paragraph per SOURCE line, long enough
// to paginate across several sheets — so distinct source lines land on distinct
// rendered pages (unlike multiPageDoc, which is a single physical source line).
// It is the fixture for the caret ↔ render linking, which needs a Y→line map that
// actually varies by page.
func multiLinePageDoc() string {
	var b strings.Builder
	b.WriteString("\\documentclass{article}\n\\begin{document}\n")
	for i := 0; i < 90; i++ {
		b.WriteString("Paragraph " + strconv.Itoa(i) + ". ")
		for j := 0; j < 12; j++ {
			b.WriteString("filler words to fill the line with text. ")
		}
		b.WriteString("\n\n") // a blank line ends the paragraph: next content is a new source line
	}
	b.WriteString("\\end{document}\n")
	return b.String()
}

// --- source ↔ render linking (restored after the PagedView migration) -------

// firstRenderPagePoint scans the render pane for the first SURFACE point that
// PagedView.PageAt resolves to a page (below its toolbar, on a card), returning
// the surface coordinate and the page + natural point under it.
func firstRenderPagePoint(s *State) (sx, sy, page, localY int, ok bool) {
	rb := s.renderView.Bounds()
	cx := rb.X + rb.W/2
	for y := rb.Y; y < rb.Y+rb.H; y++ {
		if p, _, ly, hit := s.renderView.PageAt(cx-rb.X, y-rb.Y); hit {
			return cx, y, p, ly, true
		}
	}
	return 0, 0, 0, 0, false
}

func TestMakeLineMapOrdersAndTieBreaks(t *testing.T) {
	lm := makeLineMap(map[int][2]int{
		9: {50, 70},
		4: {10, 20},
		7: {10, 30}, // same yTop as line 4 -> tie broken by source line
	})
	want := []lineBand{{4, 10, 20}, {7, 10, 30}, {9, 50, 70}}
	if len(lm.bands) != len(want) {
		t.Fatalf("bands = %v, want %v", lm.bands, want)
	}
	for i := range want {
		if lm.bands[i] != want[i] {
			t.Fatalf("band %d = %v, want %v", i, lm.bands[i], want[i])
		}
	}
	// An empty map yields an empty (non-nil) band list.
	if b := makeLineMap(map[int][2]int{}).bands; len(b) != 0 {
		t.Fatalf("empty map bands = %v, want none", b)
	}
}

// The line bands no longer come out of the compile — the browser measures them
// once it has laid the pages out (see State.SetLineBands). What the compile must
// still guarantee is a page per drawable page, each with a size to lay out in.
func TestCompileProducesParallelPages(t *testing.T) {
	res := compileLaTeX(SampleLaTeX, toolkit.DefaultLight())
	if len(res.sizes) != len(res.svgs) {
		t.Fatalf("sizes(%d) not parallel to svgs(%d)", len(res.sizes), len(res.svgs))
	}
	total := 0
	for _, sz := range res.sizes {
		total += sz.X * sz.Y
	}
	if total == 0 {
		t.Fatalf("sample doc produced no page area to lay out")
	}
}

func TestLineAtPageY(t *testing.T) {
	s := newTestState(t, false)
	s.lineMaps = []pageLineMap{{bands: []lineBand{
		{line: 5, yTop: 100, yBot: 140},
		{line: 6, yTop: 140, yBot: 180},
	}}}
	// Exact containment.
	if ln, ok := s.lineAtPageY(1, 120); !ok || ln != 5 {
		t.Fatalf("contained click: ln=%d ok=%v, want 5,true", ln, ok)
	}
	// Just above the first band but within tolerance -> nearest (line 5).
	if ln, ok := s.lineAtPageY(1, 100-10); !ok || ln != 5 {
		t.Fatalf("near-miss above: ln=%d ok=%v, want 5,true", ln, ok)
	}
	// Just below the last band, within tolerance -> nearest (line 6).
	if ln, ok := s.lineAtPageY(1, 180+10); !ok || ln != 6 {
		t.Fatalf("near-miss below: ln=%d ok=%v, want 6,true", ln, ok)
	}
	// Far past every band -> no line.
	if _, ok := s.lineAtPageY(1, 180+sourceClickTolY+50); ok {
		t.Fatalf("far click should not resolve to a line")
	}
	// Out-of-range page.
	if _, ok := s.lineAtPageY(9, 120); ok {
		t.Fatalf("out-of-range page should not resolve")
	}
	if _, ok := s.lineAtPageY(0, 120); ok {
		t.Fatalf("page 0 should not resolve")
	}
	// A page with no recorded lines.
	s.lineMaps = []pageLineMap{{}}
	if _, ok := s.lineAtPageY(1, 10); ok {
		t.Fatalf("empty-band page should not resolve")
	}
}

func TestPageYForLine(t *testing.T) {
	s := newTestState(t, false)
	s.lineMaps = []pageLineMap{
		{bands: []lineBand{{line: 3, yTop: 20, yBot: 40}, {line: 5, yTop: 60, yBot: 80}}},
		{bands: []lineBand{{line: 9, yTop: 30, yBot: 50}}},
	}
	// Exact line on page 1.
	if p, y, ok := s.pageYForLine(5); !ok || p != 1 || y != 60 {
		t.Fatalf("exact line 5: p=%d y=%d ok=%v, want 1,60,true", p, y, ok)
	}
	// Exact line on page 2.
	if p, y, ok := s.pageYForLine(9); !ok || p != 2 || y != 30 {
		t.Fatalf("exact line 9: p=%d y=%d ok=%v, want 2,30,true", p, y, ok)
	}
	// A line with no band -> nearest present line (7 is nearest to 5 and 9; 5 wins
	// on distance tie by first-seen).
	if p, _, ok := s.pageYForLine(7); !ok || p != 1 {
		t.Fatalf("structural line 7: p=%d ok=%v, want page 1 (nearest)", p, ok)
	}
	// No maps at all -> not found.
	s.lineMaps = nil
	if _, _, ok := s.pageYForLine(5); ok {
		t.Fatalf("empty lineMaps should report not-found")
	}
}

func TestJumpCaretToLineClamps(t *testing.T) {
	s := newTestState(t, false)
	n := len(s.editorLines())

	s.jumpCaretToLine(2)
	if s.editor.CursorLine().Get() != 1 || s.editor.CursorCol().Get() != 0 {
		t.Fatalf("jump to line 2: caret=(%d,%d), want (1,0)", s.editor.CursorLine().Get(), s.editor.CursorCol().Get())
	}
	if !s.editor.Focused().Get() {
		t.Fatalf("jump should focus the editor")
	}
	// A non-positive line clamps to the first line.
	s.jumpCaretToLine(0)
	if s.editor.CursorLine().Get() != 0 {
		t.Fatalf("jump to line 0 landed on %d, want 0", s.editor.CursorLine().Get())
	}
	// A line past the buffer clamps to the last line.
	s.jumpCaretToLine(n + 100)
	if s.editor.CursorLine().Get() != n-1 {
		t.Fatalf("jump past end landed on %d, want %d", s.editor.CursorLine().Get(), n-1)
	}
}

func TestScrollEditorToLine(t *testing.T) {
	s := newTestState(t, false)
	s.editor.ScrollLine().Set(0)
	// A line already in view leaves the scroll where it is.
	s.scrollEditorToLine(0)
	if s.editor.ScrollLine().Get() != 0 {
		t.Fatalf("in-view line moved scroll to %d, want 0", s.editor.ScrollLine().Get())
	}
	// A line far below centres it (top >= 0).
	s.scrollEditorToLine(500)
	if s.editor.ScrollLine().Get() <= 0 {
		t.Fatalf("far-below line did not scroll down: %d", s.editor.ScrollLine().Get())
	}
	// A line above the current window clamps the top at 0.
	s.editor.ScrollLine().Set(50)
	s.scrollEditorToLine(1)
	if s.editor.ScrollLine().Get() != 0 {
		t.Fatalf("above-window line clamped to %d, want 0", s.editor.ScrollLine().Get())
	}
}

func TestCaretMaybeChangedGuards(t *testing.T) {
	s := newTestState(t, false)

	// syncing suppresses the sync entirely (lastCaretLine untouched).
	s.lastCaretLine = 0
	s.editor.CursorLine().Set(4)
	s.syncing = true
	s.caretMaybeChanged()
	if s.lastCaretLine != 0 {
		t.Fatalf("syncing did not suppress the caret sync (last=%d)", s.lastCaretLine)
	}
	s.syncing = false

	// Same line: no-op.
	s.lastCaretLine = 4
	s.caretMaybeChanged()

	// Changed line but no map for it -> updates the tracker, no scroll, no panic.
	s.lineMaps = nil
	s.lastCaretLine = 0
	s.editor.CursorLine().Set(6)
	s.caretMaybeChanged()
	if s.lastCaretLine != 6 {
		t.Fatalf("tracker not advanced on a mapless caret move: %d", s.lastCaretLine)
	}
}

func TestCaretMoveScrollsRenderToLaterPage(t *testing.T) {
	s := newTestState(t, false)
	s.SetSource(multiLinePageDoc())
	if s.DrawnPages() < 2 {
		t.Fatalf("multi-page doc drew %d pages, want >= 2", s.DrawnPages())
	}
	// The bands are measured from the DOM by the host, which there is none of in
	// a headless test, so they are seeded here through the same seam the host
	// uses. What this test is about is the LINKING — that a caret move scrolls
	// the render to the page carrying that line — and that logic does not care
	// where the measurement came from.
	s.SetLineBands(1, []int{3}, []int{0}, []int{100})
	s.SetLineBands(2, []int{9}, []int{0}, []int{100})
	laterLine, laterPage := 9, 2
	// Paginated mode so the scroll is observable as a page change.
	s.renderView.Mode().Set(toolkit.PagedPaginated)
	if s.RenderCurrentPage() != 1 {
		t.Fatalf("precondition: current page = %d, want 1", s.RenderCurrentPage())
	}
	// Move the caret to that later-page line (as a navigation would) and sync.
	s.lastCaretLine = 0
	s.editor.CursorLine().Set(laterLine - 1)
	s.caretMaybeChanged()
	if s.RenderCurrentPage() != laterPage {
		t.Fatalf("caret move did not scroll render to page %d (got %d)", laterPage, s.RenderCurrentPage())
	}
}

func TestClickRenderJumpsCaretToSource(t *testing.T) {
	s := newTestState(t, false)
	sx, sy, page, _, ok := firstRenderPagePoint(s)
	if !ok {
		t.Fatalf("no render page point found to click")
	}

	// (a) A page click that resolves to a source line jumps the caret and focuses
	// the editor. Override this page's band table to span the whole page so any
	// on-page click resolves deterministically.
	s.lineMaps = make([]pageLineMap, page)
	s.lineMaps[page-1] = pageLineMap{bands: []lineBand{{line: 3, yTop: 0, yBot: 1 << 20}}}
	s.editor.CursorLine().Set(0)
	s.editor.CursorCol().Set(0)
	s.editor.Focused().Set(false)
	s.renderView.SetFocused(true)
	if !s.HandleClick(sx, sy) {
		t.Fatalf("render click not consumed")
	}
	if s.editor.CursorLine().Get() != 2 {
		t.Fatalf("click-to-source landed on line %d, want 2", s.editor.CursorLine().Get())
	}
	if !s.editor.Focused().Get() || s.renderView.Focused() {
		t.Fatalf("click-to-source should focus the editor, not the render")
	}

	// (b) The jump settles: it did NOT re-enter the caret→render sync (syncing
	// guard), so a second identical click is idempotent on the caret line.
	before := s.editor.CursorLine().Get()
	s.HandleClick(sx, sy)
	if s.editor.CursorLine().Get() != before {
		t.Fatalf("second click oscillated the caret: %d -> %d", before, s.editor.CursorLine().Get())
	}
}

func TestClickRenderNotOnSourceKeepsRenderFocus(t *testing.T) {
	s := newTestState(t, false)

	// (a) A click on the render pane's TOOLBAR strip: PageAt is false there, so no
	// source jump; the render takes keyboard focus for paging.
	rb := s.renderView.Bounds()
	s.editor.Focused().Set(true)
	s.renderView.SetFocused(false)
	if !s.HandleClick(rb.X+rb.W/2, rb.Y+2) { // +2: inside the toolbar strip
		t.Fatalf("render toolbar click not consumed")
	}
	if s.renderView.Focused() == false && s.editor.Focused().Get() {
		// A toolbar click may hit a button; either way it must NOT have jumped the
		// caret into the editor. Assert the render owns focus.
		t.Fatalf("toolbar click unexpectedly left focus on the editor")
	}

	// (b) A click on a page area with NO band nearby: PageAt ok but lineAtPageY
	// false -> render keeps focus, editor loses it.
	sx, sy, page, _, ok := firstRenderPagePoint(s)
	if !ok {
		t.Fatalf("no render page point found")
	}
	s.lineMaps = make([]pageLineMap, page) // page exists but carries no bands
	s.editor.Focused().Set(true)
	s.renderView.SetFocused(false)
	if !s.HandleClick(sx, sy) {
		t.Fatalf("render page click not consumed")
	}
	if !s.renderView.Focused() || s.editor.Focused().Get() {
		t.Fatalf("bandless page click should give the render focus")
	}
}

func TestAbsInt(t *testing.T) {
	if absInt(-3) != 3 || absInt(3) != 3 || absInt(0) != 0 {
		t.Fatalf("absInt wrong")
	}
}

func TestLinkIntrospectionHooks(t *testing.T) {
	s := newTestState(t, false)

	// RenderLineAt: a full-page band makes any on-page point resolve; the toolbar
	// strip resolves to 0.
	sx, sy, page, _, ok := firstRenderPagePoint(s)
	if !ok {
		t.Fatalf("no render page point")
	}
	s.lineMaps = make([]pageLineMap, page)
	s.lineMaps[page-1] = pageLineMap{bands: []lineBand{{line: 4, yTop: 0, yBot: 1 << 20}}}
	if ln := s.RenderLineAt(sx, sy); ln != 4 {
		t.Fatalf("RenderLineAt on page = %d, want 4", ln)
	}
	rb := s.renderView.Bounds()
	if ln := s.RenderLineAt(rb.X+rb.W/2, rb.Y+2); ln != 0 {
		t.Fatalf("RenderLineAt on toolbar = %d, want 0", ln)
	}

	// LineToRenderPage: present line -> its page; absent -> 0.
	s.lineMaps = []pageLineMap{{bands: []lineBand{{line: 8, yTop: 10, yBot: 20}}}}
	if p := s.LineToRenderPage(8); p != 1 {
		t.Fatalf("LineToRenderPage(8) = %d, want 1", p)
	}
	s.lineMaps = nil
	if p := s.LineToRenderPage(8); p != 0 {
		t.Fatalf("LineToRenderPage with no maps = %d, want 0", p)
	}

	// SetRenderPaginated flips the mode both ways; RenderScrollY reads back an int.
	s.SetRenderPaginated(true)
	if s.RenderMode() != int(toolkit.PagedPaginated) {
		t.Fatalf("SetRenderPaginated(true) did not switch mode")
	}
	s.SetRenderPaginated(false)
	if s.RenderMode() != int(toolkit.PagedContinuous) {
		t.Fatalf("SetRenderPaginated(false) did not switch mode")
	}
	_ = s.RenderScrollY()
}
