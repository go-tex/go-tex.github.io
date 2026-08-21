// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// --- rightPane: tab strip over the render PagedView | log --------------------

func newTestRightPane(t *testing.T) *rightPane {
	t.Helper()
	SetupText(1)
	rv := toolkit.NewPagedView(testBitmaps(6))
	rp := newRightPane(rv, toolkit.NewLogView())
	rp.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 320})
	return rp
}

// contentPoint returns a point well inside the render content (below the tab
// strip AND the PagedView's own toolbar), so a press lands on the scroll pane.
func contentPoint(rp *rightPane) (int, int) {
	cr := rp.contentRect()
	return cr.X + cr.W/2, cr.Y + toolkit.Scaled(30) + 20
}

func TestRightPaneTabSwitchAndRouting(t *testing.T) {
	rp := newTestRightPane(t)
	logTab := rp.tabRect(tabLog)

	// Click the Log tab: switch to the log.
	if !rp.click(logTab.X+logTab.W/2, logTab.Y+logTab.H/2) {
		t.Fatalf("tab click not consumed")
	}
	if rp.press != rpPressTabs || !rp.isLog() {
		t.Fatalf("Log tab did not activate (press=%d, isLog=%v)", rp.press, rp.isLog())
	}

	// A click in the log content is forwarded to the LogView and captures it (so a
	// following drag pans its scrollback); drag + release round-trip to the widget.
	cx, cy := contentPoint(rp)
	if !rp.click(cx, cy) {
		t.Fatalf("log content click should be consumed")
	}
	if rp.press != rpPressLog {
		t.Fatalf("log content click should capture the log (press=%d)", rp.press)
	}
	if !rp.drag(cx, cy+30) {
		t.Fatalf("log drag not routed to the LogView")
	}
	if !rp.release(cx, cy+30) {
		t.Fatalf("log release not routed to the LogView")
	}
	if rp.press != rpPressNone {
		t.Fatalf("log capture not cleared after release")
	}
	// keyDown on the Log tab is not routed to the render viewer.
	if rp.keyDown("PageDown") {
		t.Fatalf("keyDown on the Log tab should not route to the render viewer")
	}

	// Wheel over the log is consumed (the LogView owns its scroll); wheel on the
	// tab strip is inert; wheel outside is not consumed.
	if !rp.scrollWheel(cx, cy, 0, 4) {
		t.Fatalf("log wheel not consumed")
	}
	strip := rp.Bounds()
	if !rp.scrollWheel(strip.X+2, strip.Y+2, 0, 1) {
		t.Fatalf("wheel on the tab strip should be consumed (inert)")
	}
	if rp.scrollWheel(-5, -5, 0, 1) {
		t.Fatalf("wheel outside the pane should not be consumed")
	}

	// Back to the render tab: a press in the render content captures the render,
	// then drag + release forward to the PagedView.
	rp.setActive(tabRender)
	cx, cy = contentPoint(rp)
	if !rp.click(cx, cy) || rp.press != rpPressRender {
		t.Fatalf("render content press did not capture the render (press=%d)", rp.press)
	}
	if !rp.drag(cx, cy+40) {
		t.Fatalf("render drag not routed")
	}
	if !rp.release(cx, cy+40) {
		t.Fatalf("render release not routed")
	}
	// Idle drag / release report not-handled.
	if rp.drag(1, 1) || rp.release(1, 1) {
		t.Fatalf("idle drag/release should be no-ops")
	}
	// A wheel over the render content routes to the PagedView.
	if !rp.scrollWheel(cx, cy, 0, 3) {
		t.Fatalf("render wheel not consumed")
	}
	// keyDown on the render tab is routed to the viewer.
	if !rp.keyDown("PageDown") {
		t.Fatalf("keyDown on the render tab should route to the viewer")
	}
}

func TestRightPaneRenderClickMiss(t *testing.T) {
	rp := newTestRightPane(t)
	// A press below the whole pane (past the render content) is not consumed.
	b := rp.Bounds()
	if rp.click(b.X+5, b.Y+b.H+50) {
		t.Fatalf("press outside the pane content should not be consumed")
	}
}

func TestRightPaneContentRectClampsAndTinyHeight(t *testing.T) {
	rp := newTestRightPane(t)
	// A height smaller than the tab strip clamps the strip to the pane height.
	rp.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 200, H: 6})
	if rp.tabH != 6 {
		t.Fatalf("tab strip not clamped to the tiny height: %d", rp.tabH)
	}
	// Shrinking the bounds under the (unrecomputed) tab height drives the
	// defensive ch<0 clamp in contentRect to zero.
	rp.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 200, H: 300}) // tabH back to full
	rp.Base.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 200, H: 4})
	if cr := rp.contentRect(); cr.H != 0 {
		t.Fatalf("contentRect height should clamp to 0, got %d", cr.H)
	}
}

func TestRightPaneDrawBothTabs(t *testing.T) {
	rp := newTestRightPane(t)
	buf := make([]byte, 240*320*4)
	p := painter.NewPixelPainter(buf, 240, 320)
	th := toolkit.DefaultLight()
	rp.Draw(p, th) // render tab
	rp.setActive(tabLog)
	rp.Draw(p, th) // log tab
}

// --- app introspection + tab toggle ------------------------------------------

func TestRound3Introspection(t *testing.T) {
	s := newTestState(t, false)
	if s.SelectedScheme() != 0 {
		t.Fatalf("initial scheme = %d, want 0", s.SelectedScheme())
	}
	if s.ZoomPercent() != 100 {
		t.Fatalf("initial zoom = %d%%, want 100%%", s.ZoomPercent())
	}
	if s.ActiveTab() != tabRender {
		t.Fatalf("initial tab = %d, want Rendered", s.ActiveTab())
	}
	if s.RenderMode() != int(toolkit.PagedContinuous) {
		t.Fatalf("initial render mode should be continuous")
	}
	// The initial compile already seeded the accumulating Log (history, not a
	// cleared-each-compile panel).
	if n := s.LogEntryCount(); n <= 0 {
		t.Fatalf("initial compile should have seeded the Log, got %d entries", n)
	}

	// toggleLog both directions (covers the render->log and log->render branches).
	s.toggleLog()
	if !s.showLog() || s.ActiveTab() != tabLog {
		t.Fatalf("toggleLog did not switch to the Log tab")
	}
	s.toggleLog()
	if s.showLog() || s.ActiveTab() != tabRender {
		t.Fatalf("toggleLog did not switch back to the Rendered tab")
	}

	// A scheme change is reflected in SelectedScheme.
	s.schemePicker.Select(2)
	if s.SelectedScheme() != 2 {
		t.Fatalf("SelectedScheme after Select(2) = %d", s.SelectedScheme())
	}
}

func TestDebugRects(t *testing.T) {
	s := newTestState(t, false)
	r := s.DebugRects()
	for _, name := range []string{"picker", "popover", "renderTab", "logTab", "renderPane", "renderContent", "sourceTab", "wysiwygTab"} {
		v, ok := r[name]
		if !ok {
			t.Fatalf("DebugRects missing %q", name)
		}
		if v[2] <= 0 || v[3] <= 0 {
			t.Fatalf("DebugRects[%q] has a non-positive size: %v", name, v)
		}
	}
	// The Rendered tab sits to the left of the Log tab with no overlap (they are
	// compact, label-sized tabs, not a full-width split).
	rt, lt := r["renderTab"], r["logTab"]
	if rt[0]+rt[2] > lt[0] {
		t.Fatalf("tabs overlap: %v | %v", rt, lt)
	}
}

// TestClickTabsAndFocusThroughState drives tab switching AND the render-focus
// hand-off through the full State router: clicking the render content focuses
// the PagedView (nav keys flip pages), clicking the editor takes focus back.
func TestClickTabsAndFocusThroughState(t *testing.T) {
	s := newTestState(t, false)
	s.SetSource(multiPageDoc())
	s.renderView.Mode().Set(toolkit.PagedPaginated)

	// Clicking the Log tab through the full State router switches the pane.
	logTab := s.rightPane.tabRect(tabLog)
	if !s.HandleClick(logTab.X+logTab.W/2, logTab.Y+logTab.H/2) {
		t.Fatalf("tab click not consumed by State")
	}
	if !s.showLog() {
		t.Fatalf("State did not switch to the Log tab")
	}
	s.HandleRelease(logTab.X+logTab.W/2, logTab.Y+logTab.H/2)

	// Back to Rendered, then click the render content to focus the PagedView.
	renderTab := s.rightPane.tabRect(tabRender)
	s.HandleClick(renderTab.X+renderTab.W/2, renderTab.Y+renderTab.H/2)
	rr := s.renderRect()
	if !s.HandleClick(rr.X+rr.W/2, rr.Y+toolkit.Scaled(30)+40) {
		t.Fatalf("render content click not consumed by State")
	}
	if !s.RenderFocused() || s.editor.Focused().Get() {
		t.Fatalf("render click did not move focus to the viewer (focused=%v editorFocused=%v)",
			s.RenderFocused(), s.editor.Focused().Get())
	}
	s.HandleRelease(rr.X+rr.W/2, rr.Y+toolkit.Scaled(30)+40)

	// A nav key now flips the page instead of moving the editor caret.
	before := s.RenderCurrentPage()
	if !s.HandleKeyDown("PageDown") {
		t.Fatalf("PageDown not consumed while the viewer is focused")
	}
	if s.RenderCurrentPage() <= before {
		t.Fatalf("PageDown did not advance the page: %d -> %d", before, s.RenderCurrentPage())
	}

	// Clicking the editor takes focus back; typing reaches the editor again.
	er := s.editorRect()
	s.HandleClick(er.X+20, er.Y+20)
	if s.RenderFocused() || !s.editor.Focused().Get() {
		t.Fatalf("editor click did not restore editor focus")
	}
	page := s.RenderCurrentPage()
	if !s.HandleChar("Z") {
		t.Fatalf("editor did not accept typing after regaining focus")
	}
	if s.RenderCurrentPage() != page {
		t.Fatalf("typing stole a page flip: %d -> %d", page, s.RenderCurrentPage())
	}
}
