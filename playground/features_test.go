// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/toolkit/rougelex"
)

const diagDoc = `\documentclass{article}\begin{document}
Text \undefinedcmd here.
\begin{mysteryenv}body\end{mysteryenv}
An equation $\nosuchmath x$ inline.
\end{document}`

// --- #4 resize grip: dragging the divider changes the editor width -----------

func TestDividerDragChangesEditorWidth(t *testing.T) {
	s := newTestState(t, false)
	pr := s.paned.Bounds()
	divX := pr.X + s.paned.Position
	y := pr.Y + 20

	beforePos := s.paned.Position
	beforeEditorW := s.editor.Bounds().W

	if !s.HandleClick(divX, y) {
		t.Fatalf("press on divider not consumed")
	}
	if s.pressKind != pressDivider {
		t.Fatalf("press did not capture the divider (pressKind=%d)", s.pressKind)
	}
	// Drag the grip 150px to the right.
	if !s.HandleMove(divX+150, y) {
		t.Fatalf("divider drag (move) not consumed")
	}
	if !s.HandleRelease(divX+150, y) {
		t.Fatalf("divider release not consumed")
	}
	if s.paned.Position <= beforePos+100 {
		t.Fatalf("divider did not move right: pos %d -> %d", beforePos, s.paned.Position)
	}
	if s.editor.Bounds().W <= beforeEditorW+100 {
		t.Fatalf("editor width did not grow with the divider: %d -> %d", beforeEditorW, s.editor.Bounds().W)
	}
	// Capture cleared after release.
	if s.pressKind != pressNone {
		t.Fatalf("pressKind not cleared after release")
	}
}

// --- #6 render pane: pointer drag + wheel routed to the PagedView -------------

// TestRenderDragRoutedToPagedView proves a press-drag-release over the render
// pane is captured and forwarded to the PagedView (the routing that makes its
// inner scrollbar thumb + content pan draggable). The PagedView owns the scroll
// offset, so we assert the app-level routing, not the offset.
func TestRenderDragRoutedToPagedView(t *testing.T) {
	s := newTestState(t, false)
	rr := s.renderRect()
	// Press inside the render content (below the PagedView toolbar strip).
	px, py := rr.X+rr.W/2, rr.Y+toolkit.Scaled(30)+40
	if !s.HandleClick(px, py) {
		t.Fatalf("press on the render pane not consumed")
	}
	if s.pressKind != pressRight {
		t.Fatalf("press did not capture the right pane (pressKind=%d)", s.pressKind)
	}
	if !s.HandleMove(px, py+200) {
		t.Fatalf("render drag (move) not routed")
	}
	if !s.HandleRelease(px, py+200) {
		t.Fatalf("render release not routed")
	}
	if s.pressKind != pressNone {
		t.Fatalf("capture not cleared after release")
	}
}

// TestWheelFlipsPageInPaginatedMode is the round-6 point: a vertical wheel notch
// over the render pane, in paginated mode on a multi-page document, flips the
// current page (the PagedView's edge-flip). This is real pointer input through
// the full State router.
func TestWheelFlipsPageInPaginatedMode(t *testing.T) {
	s := newTestState(t, false)
	// Deterministic short pages (each fits the pane) so a single wheel notch flips
	// at the page edge rather than scrolling within a tall page.
	s.renderView.SetPages(testBitmaps(6))
	s.renderView.Mode().Set(toolkit.PagedPaginated)
	if s.RenderCurrentPage() != 1 {
		t.Fatalf("precondition: current page = %d, want 1", s.RenderCurrentPage())
	}
	rr := s.renderRect()
	// A downward wheel over the render content flips to the next page.
	if !s.HandleScroll(rr.X+rr.W/2, rr.Y+toolkit.Scaled(30)+40, 0, 3) {
		t.Fatalf("wheel over render not consumed")
	}
	if s.RenderCurrentPage() != 2 {
		t.Fatalf("wheel did not flip to page 2: current=%d (pages=%d)",
			s.RenderCurrentPage(), s.renderView.PageCount())
	}
	// An upward wheel flips back.
	if !s.HandleScroll(rr.X+rr.W/2, rr.Y+toolkit.Scaled(30)+40, 0, -3) {
		t.Fatalf("upward wheel not consumed")
	}
	if s.RenderCurrentPage() != 1 {
		t.Fatalf("upward wheel did not flip back to page 1: current=%d", s.RenderCurrentPage())
	}
	// A wheel over the editor is still consumed by the editor path.
	er := s.editorRect()
	if !s.HandleScroll(er.X+10, er.Y+10, 0, 3) {
		t.Fatalf("editor wheel not consumed")
	}
}

// --- #1 minimap --------------------------------------------------------------

func TestMinimapClickScrollsEditor(t *testing.T) {
	s := newTestState(t, false)
	mm := s.minimap.Bounds()
	if mm.W <= 0 {
		t.Fatalf("minimap not laid out (W=%d)", mm.W)
	}
	if s.editor.ScrollLine != 0 {
		t.Fatalf("precondition: editor should start at top")
	}
	// Click near the bottom of the minimap -> scroll the editor down.
	if !s.HandleClick(mm.X+mm.W/2, mm.Y+mm.H-2) {
		t.Fatalf("minimap click not consumed")
	}
	if s.pressKind != pressMinimap {
		t.Fatalf("press did not capture the minimap (pressKind=%d)", s.pressKind)
	}
	if s.editor.ScrollLine <= 0 {
		t.Fatalf("minimap click did not scroll the editor: ScrollLine=%d", s.editor.ScrollLine)
	}
	// Drag up to the top -> back to line 0.
	s.HandleMove(mm.X+mm.W/2, mm.Y+1)
	if s.editor.ScrollLine != 0 {
		t.Fatalf("minimap drag to top did not scroll to 0: ScrollLine=%d", s.editor.ScrollLine)
	}
	s.HandleRelease(mm.X+mm.W/2, mm.Y+1)
}

func TestMinimapToggleWidensEditor(t *testing.T) {
	s := newTestState(t, false)
	withMinimap := s.editor.Bounds().W
	if !s.showMinimap {
		t.Fatalf("minimap should be on by default")
	}
	// Toggle it off via the toolbar button.
	b := s.minimapBtn.Bounds()
	s.HandleClick(b.X+2, b.Y+2)
	if s.showMinimap {
		t.Fatalf("minimap toggle did not turn it off")
	}
	if s.minimap.Bounds().W != 0 {
		t.Fatalf("minimap still laid out after toggle off")
	}
	if s.editor.Bounds().W <= withMinimap {
		t.Fatalf("editor did not widen after hiding the minimap: %d -> %d", withMinimap, s.editor.Bounds().W)
	}
}

// --- #3 diagnostics Log ------------------------------------------------------

func TestLogPanelListsDiagnostics(t *testing.T) {
	s := newTestState(t, false)
	before := s.LogEntryCount() // the initial clean compile already logged one entry
	s.editor.SetText(diagDoc)
	s.Compile()

	if s.diag.Skipped["undefinedcmd"] == 0 {
		t.Fatalf("undefined command not recorded: %v", s.diag.Skipped)
	}
	if s.diag.UndefinedEnvs["mysteryenv"] == 0 {
		t.Fatalf("undefined environment not recorded: %v", s.diag.UndefinedEnvs)
	}
	if len(s.diag.MathDropped) == 0 {
		t.Fatalf("dropped math not recorded: %v", s.diag.MathDropped)
	}
	if diagIssueCount(s.diag, s.errText) < 3 {
		t.Fatalf("issue count too low: %d", diagIssueCount(s.diag, s.errText))
	}
	// The compile appended entries to the accumulating Log (history kept, not
	// cleared): the count grew past the initial clean-compile entry.
	if s.LogEntryCount() <= before {
		t.Fatalf("diagnostics compile did not accumulate Log entries: %d -> %d", before, s.LogEntryCount())
	}
	// The status bar shows an issue count.
	if !strings.Contains(s.status.Segments[3], "issue") {
		t.Fatalf("status issue segment = %q", s.status.Segments[3])
	}

	// Click the "Log" tab: the right pane switches to the diagnostics log.
	logTab := s.rightPane.tabRect(tabLog)
	s.HandleClick(logTab.X+logTab.W/2, logTab.Y+logTab.H/2)
	if !s.showLog() || s.rightPane.active != tabLog {
		t.Fatalf("Log tab did not switch the right pane to the log view")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // exercises the log view draw path
	// The undefined-command/env/math diagnostics are all warnings: the Log paints
	// amber (LogWarn) ink somewhere in the content area.
	if !bufHasColor(buf, testW, s.rightPane.contentRect(), logWarnInk) {
		t.Fatalf("Log tab did not paint any amber warning ink")
	}
	// Click the "Rendered" tab to switch back.
	renderTab := s.rightPane.tabRect(tabRender)
	s.HandleClick(renderTab.X+renderTab.W/2, renderTab.Y+renderTab.H/2)
	if s.showLog() || s.rightPane.active != tabRender {
		t.Fatalf("Rendered tab did not restore the render view")
	}
}

func TestLogCleanCompile(t *testing.T) {
	s := newTestState(t, false) // the sample is a clean compile
	if got := diagIssueCount(s.diag, s.errText); got != 0 {
		t.Fatalf("sample should be a clean compile, issueCount=%d", got)
	}
	// The clean compile still logs its outcome ("compiled 1 page"), so the history
	// is non-empty from the start.
	if s.LogEntryCount() < 1 {
		t.Fatalf("clean compile should still log an outcome entry, got %d", s.LogEntryCount())
	}
}

// --- #2 syntax colour-scheme picker ------------------------------------------

func TestSchemePickerChangesColours(t *testing.T) {
	s := newTestState(t, false)
	line := []string{`\section{Intro}`}

	defColor := colourOf(s.editor.Syntax.Highlight("latex", line, s.theme), `\section`, line[0])

	// Select "Monokai" (index of it in ThemeNames).
	names := rougelex.ThemeNames()
	mi := indexOf(names, "Monokai")
	if mi < 0 {
		t.Fatalf("Monokai not among %v", names)
	}
	s.schemePicker.Select(mi) // fires OnSelect -> applyScheme

	monColor := colourOf(s.editor.Syntax.Highlight("latex", line, s.theme), `\section`, line[0])
	if monColor == defColor {
		t.Fatalf("scheme change did not alter the command colour (%v)", monColor)
	}
	// Monokai's command colour is theme-independent.
	monDark := colourOf(s.editor.Syntax.Highlight("latex", line, toolkit.DefaultDark()), `\section`, line[0])
	if monColor != monDark {
		t.Fatalf("Monokai command colour should be theme-independent: light %v dark %v", monColor, monDark)
	}

	// An out-of-range applyScheme is a no-op.
	prev := s.editor.Syntax
	s.applyScheme(9999)
	if s.editor.Syntax != prev {
		t.Fatalf("out-of-range applyScheme changed the highlighter")
	}
}

func TestSchemePickerPopover(t *testing.T) {
	s := newTestState(t, false)
	pb := s.schemePicker.Bounds()
	// Open the popover by clicking the picker.
	s.HandleClick(pb.X+2, pb.Y+2)
	if !s.schemePicker.Open().Get() {
		t.Fatalf("clicking the picker did not open it")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // draws the popover
	// A second click inside the popover selects a row (and closes it).
	pop := s.schemePicker.PopoverBounds()
	s.HandleClick(pop.X+4, pop.Y+s.schemePicker.PopoverBounds().H-2)
	if s.schemePicker.Open().Get() {
		t.Fatalf("popover click did not close it")
	}
}

// --- #7 HiDPI scale ----------------------------------------------------------

func TestSetupTextScalesChrome(t *testing.T) {
	defer SetupText(1)
	SetupText(2)
	if toolkit.MetricScale() != 2 {
		t.Fatalf("SetupText(2) did not set metric scale, got %v", toolkit.MetricScale())
	}
	s := NewState(1200, 800, false)
	if s.toolbarH != toolkit.Scaled(30) || s.toolbarH != 60 {
		t.Fatalf("toolbar height did not scale to 2x: %d (Scaled(30)=%d)", s.toolbarH, toolkit.Scaled(30))
	}
	SetupText(0) // non-positive is clamped to 1
	if toolkit.MetricScale() != 1 {
		t.Fatalf("SetupText(0) should clamp to scale 1, got %v", toolkit.MetricScale())
	}
}

// --- helpers -----------------------------------------------------------------

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// colourOf returns the colour of the span covering the first rune of token in
// line, given a per-line span set for a single line.
func colourOf(spans [][]toolkit.TextSpan, token, line string) toolkit.RGBA {
	if len(spans) == 0 {
		return toolkit.RGBA{}
	}
	start := strings.Index(line, token)
	if start < 0 {
		return toolkit.RGBA{}
	}
	runeStart := len([]rune(line[:start]))
	for _, sp := range spans[0] {
		if runeStart >= sp.Start && runeStart < sp.End {
			return sp.Color
		}
	}
	return toolkit.RGBA{}
}
