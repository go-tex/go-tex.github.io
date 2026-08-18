// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"

	"github.com/go-tex/go-tex.github.io/playground/latexhl"
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

// --- #6 scrollbar: dragging the render pane's thumb scrolls it ----------------

func TestScrollbarThumbDragScrollsRender(t *testing.T) {
	s := newTestState(t, false)
	rr := s.renderView.contentRect()
	// The sample render is far taller than the viewport, so a draggable thumb
	// exists at the top (OffsetY 0). Press ON the thumb (right gutter, near top).
	thumbX := rr.X + rr.W - 3
	thumbY := rr.Y + 3

	if s.rscroll().OffsetY != 0 {
		t.Fatalf("precondition: render should start unscrolled, got OffsetY=%d", s.rscroll().OffsetY)
	}
	if !s.HandleClick(thumbX, thumbY) {
		t.Fatalf("press on scrollbar thumb not consumed")
	}
	if s.pressKind != pressRight {
		t.Fatalf("press did not capture the right pane (pressKind=%d)", s.pressKind)
	}
	// Drag the thumb down by 200 device px.
	if !s.HandleMove(thumbX, thumbY+200) {
		t.Fatalf("thumb drag (move) not consumed")
	}
	got := s.rscroll().OffsetY
	if !s.HandleRelease(thumbX, thumbY+200) {
		t.Fatalf("thumb release not consumed")
	}
	// The pointer-driven drag must have scrolled the render pane. RED without the
	// HandleMove routing (the old no-op stub left OffsetY at 0).
	if got <= 0 {
		t.Fatalf("dragging the scrollbar thumb did not scroll the render: OffsetY=%d", got)
	}
}

// A wheel scroll is the already-working control (pointer-independent path).
func TestWheelScrollControl(t *testing.T) {
	s := newTestState(t, false)
	rr := s.renderView.contentRect()
	before := s.rscroll().OffsetY
	if !s.HandleScroll(rr.X+10, rr.Y+10, 5) {
		t.Fatalf("wheel scroll not consumed")
	}
	if s.rscroll().OffsetY <= before {
		t.Fatalf("wheel did not scroll render: %d -> %d", before, s.rscroll().OffsetY)
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
	if s.logView.alarmCount() < 3 {
		t.Fatalf("alarm count too low: %d", s.logView.alarmCount())
	}
	// The Log view lists each with its role, and the status bar shows an issue count.
	var text strings.Builder
	for _, e := range s.logView.rows() {
		text.WriteString(e.text)
		text.WriteString("\n")
	}
	body := text.String()
	for _, want := range []string{"undefinedcmd", "mysteryenv", "nosuchmath", "Undefined command", "Undefined environment", "Dropped equation"} {
		if !strings.Contains(body, want) {
			t.Fatalf("log body missing %q; got:\n%s", want, body)
		}
	}
	if !strings.Contains(s.status.Segments[3], "issue") {
		t.Fatalf("status issue segment = %q", s.status.Segments[3])
	}

	// Click the "Log" tab: the right pane switches to the diagnostics log.
	tb := s.rightPane.tabs.Bounds()
	logSegX := tb.X + tb.W*3/4 // right half = second segment ("Log")
	tabY := tb.Y + tb.H/2
	s.HandleClick(logSegX, tabY)
	if !s.showLog() || s.rightPane.active != tabLog {
		t.Fatalf("Log tab did not switch the right pane to the log view")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // exercises the log view draw path
	// Click the "Rendered" tab (left half) to switch back.
	s.HandleClick(tb.X+tb.W/4, tabY)
	if s.showLog() || s.rightPane.active != tabRender {
		t.Fatalf("Rendered tab did not restore the render view")
	}
}

func TestLogCleanCompile(t *testing.T) {
	s := newTestState(t, false) // the sample is a clean compile
	if s.logView.alarmCount() != 0 {
		t.Fatalf("sample should be a clean compile, alarmCount=%d", s.logView.alarmCount())
	}
	rows := s.logView.rows()
	if len(rows) != 1 || !strings.Contains(rows[0].text, "clean compile") {
		t.Fatalf("clean-compile row not produced: %+v", rows)
	}
}

// --- #2 syntax colour-scheme picker ------------------------------------------

func TestSchemePickerChangesColours(t *testing.T) {
	s := newTestState(t, false)
	line := []string{`\section{Intro}`}

	defColor := colourOf(s.editor.Syntax.Highlight("latex", line, s.theme), `\section`, line[0])

	// Select "Monokai" (index of it in ThemeNames).
	names := latexhl.ThemeNames()
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
	if !s.schemePicker.Open {
		t.Fatalf("clicking the picker did not open it")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // draws the popover
	// A second click inside the popover selects a row (and closes it).
	pop := s.schemePicker.PopoverBounds()
	s.HandleClick(pop.X+4, pop.Y+s.schemePicker.PopoverBounds().H-2)
	if s.schemePicker.Open {
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
