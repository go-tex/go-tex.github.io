// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// --- #4 clipboard -----------------------------------------------------------

func TestClipboardCopyCutPasteSelectAll(t *testing.T) {
	s := newTestState(t, false)
	var wrote string
	s.SetClipboardWriter(func(x string) { wrote = x })

	// Select all + copy: the buffer text reaches both the toolkit clipboard and
	// the host writer hook.
	s.editor.SelectAll()
	if !s.HandleCopy() {
		t.Fatalf("HandleCopy not consumed while focused")
	}
	full := s.editor.Text()
	if s.clip.ClipboardText() != full {
		t.Fatalf("clipboard text = %q, want the whole buffer", s.clip.ClipboardText())
	}
	if wrote != full {
		t.Fatalf("host writer got %q, want the copied text", wrote)
	}
	if toolkit.ClipboardText() != full {
		t.Fatalf("toolkit-wide clipboard not routed through the app clipboard")
	}

	// Paste inserts at the caret (buffer changes).
	before := s.editor.Text()
	if !s.HandlePaste("PASTED") {
		t.Fatalf("HandlePaste not consumed")
	}
	if s.editor.Text() == before || !strings.Contains(s.editor.Text(), "PASTED") {
		t.Fatalf("paste did not insert text")
	}

	// Select-all then cut empties the buffer and the cut text is on the clipboard.
	if !s.HandleSelectAll() {
		t.Fatalf("HandleSelectAll not consumed")
	}
	if !s.editor.HasSelection() {
		t.Fatalf("SelectAll left no selection")
	}
	if !s.HandleCut() {
		t.Fatalf("HandleCut not consumed")
	}
	if strings.TrimSpace(s.editor.Text()) != "" {
		t.Fatalf("cut-all left text: %q", s.editor.Text())
	}

	// Unfocused: every clipboard op is a no-op.
	s.editor.Focused = false
	if s.HandleCopy() || s.HandleCut() || s.HandlePaste("x") || s.HandleSelectAll() {
		t.Fatalf("clipboard ops should be no-ops when the editor is unfocused")
	}
}

func TestAppClipboardNilWriter(t *testing.T) {
	c := &appClipboard{} // no onWrite hook
	c.SetClipboardText("hello")
	if c.ClipboardText() != "hello" {
		t.Fatalf("appClipboard did not store text without a writer")
	}
}

// --- #1 minimap fixed-row geometry ------------------------------------------

func TestMinimapMetricsTinyHeightAndOverflow(t *testing.T) {
	m := &minimap{}
	// Height smaller than one row clamps maxRows up to 1.
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 80, H: 1})
	m.update([]string{"a", "b"}, nil, 0, 1)
	if _, dr := m.metrics(); dr != 1 {
		t.Fatalf("tiny height should draw exactly 1 row, got %d", dr)
	}
	buf := make([]byte, 80*1*4)
	m.Draw(painter.NewPixelPainter(buf, 80, 1), toolkit.DefaultLight())

	// Taller-than-widget buffer: displayRows caps at the rows that fit (compress).
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 80, H: 40})
	many := make([]string, 500)
	for i := range many {
		many[i] = "z"
	}
	m.update(many, nil, 0, 5)
	_, dr := m.metrics()
	if dr >= len(many) || dr < 1 {
		t.Fatalf("overflow should compress to fitting rows, got displayRows=%d", dr)
	}
}

func TestLineForRowClamps(t *testing.T) {
	// A row past the end (only reachable via a defensive call) clamps to the last
	// source line.
	if got := lineForRow(5, 2, 3); got != 2 {
		t.Fatalf("lineForRow overshoot = %d, want 2", got)
	}
	if got := lineForRow(0, 4, 4); got != 0 {
		t.Fatalf("lineForRow(0) = %d, want 0", got)
	}
}

// --- #5 continuous / paginated render ---------------------------------------

// multiPageDoc is a document long enough to paginate into several sheets.
func multiPageDoc() string {
	var b strings.Builder
	b.WriteString(`\documentclass{article}\begin{document}`)
	for i := 0; i < 6; i++ {
		b.WriteString(`\section{Section}`)
		for j := 0; j < 40; j++ {
			b.WriteString("Paragraph text filling the page. ")
		}
		b.WriteString(`\newpage`)
	}
	b.WriteString(`\end{document}`)
	return b.String()
}

func TestCompileLaTeXStacksAllPages(t *testing.T) {
	single := compileLaTeX(SampleLaTeX, toolkit.DefaultLight())
	multi := compileLaTeX(multiPageDoc(), toolkit.DefaultLight())
	if multi.drawnPages < 2 {
		t.Fatalf("multi-page doc rasterized %d pages, want >=2", multi.drawnPages)
	}
	if multi.pages != multi.drawnPages {
		t.Fatalf("pages(%d) != drawnPages(%d) for a clean multi-page compile", multi.pages, multi.drawnPages)
	}
	// One bitmap per drawn page; the multi-page doc yields more than the single.
	if len(multi.bitmaps) != multi.drawnPages {
		t.Fatalf("bitmaps(%d) != drawnPages(%d)", len(multi.bitmaps), multi.drawnPages)
	}
	if len(multi.bitmaps) <= len(single.bitmaps) {
		t.Fatalf("multi-page bitmaps %d not > single-page %d", len(multi.bitmaps), len(single.bitmaps))
	}
}

func TestPaginationIntrospection(t *testing.T) {
	s := newTestState(t, false)
	if s.PageCount() != 1 || s.DrawnPages() != 1 {
		t.Fatalf("sample doc should be 1 page: pages=%d drawn=%d", s.PageCount(), s.DrawnPages())
	}
	if s.renderView.PageCount() != 1 {
		t.Fatalf("render pane should hold 1 page, got %d", s.renderView.PageCount())
	}

	s.SetSource(multiPageDoc())
	if s.PageCount() < 2 || s.DrawnPages() < 2 {
		t.Fatalf("multi-page: pages=%d drawn=%d, want >=2", s.PageCount(), s.DrawnPages())
	}
	if s.renderView.PageCount() != s.DrawnPages() {
		t.Fatalf("render pane page count %d != drawn %d", s.renderView.PageCount(), s.DrawnPages())
	}
}

// --- #2 tab bar -------------------------------------------------------------

func TestTabBarEdges(t *testing.T) {
	SetupText(1)
	tb := newTabBar([]string{"Rendered", "Log"}, nil) // nil onChange is tolerated
	tb.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 24})

	// Out-of-range setActive is a no-op; valid one moves (onChange nil-safe).
	tb.setActive(-1)
	tb.setActive(9)
	if tb.active != 0 {
		t.Fatalf("out-of-range setActive changed active to %d", tb.active)
	}
	tb.setActive(1)
	if tb.active != 1 {
		t.Fatalf("setActive(1) did not move: %d", tb.active)
	}

	// Out-of-range tabRect returns the zero rect.
	if tb.tabRect(-1) != (toolkit.Rect{}) || tb.tabRect(9) != (toolkit.Rect{}) {
		t.Fatalf("out-of-range tabRect should be the zero rect")
	}
	// tabAt hits a real tab and misses empty space.
	r0 := tb.tabRect(0)
	if tb.tabAt(r0.X+1, r0.Y+1) != 0 {
		t.Fatalf("tabAt over tab 0 did not return 0")
	}
	if tb.tabAt(-100, -100) != -1 {
		t.Fatalf("tabAt over empty space should be -1")
	}

	// A tab height shorter than the top gap clamps to 1.
	tb.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 2})
	if r := tb.tabRect(0); r.H < 1 {
		t.Fatalf("tabRect height should clamp to >=1, got %d", r.H)
	}
}

func TestTabBarDrawBranches(t *testing.T) {
	SetupText(1)
	tb := newTabBar([]string{"Rendered", "Log"}, nil)
	buf := make([]byte, 240*24*4)
	p := painter.NewPixelPainter(buf, 240, 24)
	th := toolkit.DefaultLight()

	// Zero-size: no-op.
	tb.Draw(p, th)

	// Normal draw (active + inactive tabs).
	tb.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 24})
	tb.Draw(p, th)

	// An out-of-range active index draws every tab as inactive (guard skips the
	// active pass).
	tb.active = 99
	tb.Draw(p, th)
}

func TestFillTopRoundRectBranches(t *testing.T) {
	buf := make([]byte, 40*40*4)
	p := painter.NewPixelPainter(buf, 40, 40)
	red := toolkit.RGB(0xFF, 0, 0)
	fillTopRoundRect(p, toolkit.Rect{X: 0, Y: 0, W: 20, H: 20}, 0, red) // rad<1 -> plain fill
	fillTopRoundRect(p, toolkit.Rect{X: 0, Y: 0, W: 20, H: 3}, 6, red)  // H<=rad -> no bottom square
	fillTopRoundRect(p, toolkit.Rect{X: 0, Y: 0, W: 20, H: 20}, 6, red) // normal top-round
	fillTopRoundRect(p, toolkit.Rect{X: 0, Y: 0, W: 0, H: 20}, 6, red)  // W<=0 -> plain fill
}
