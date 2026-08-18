// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// --- #4 renderView: composed zoom toolbar + scrollable zoomable image --------

func newTestRenderView(t *testing.T) *renderView {
	t.Helper()
	SetupText(1)
	rv := newRenderView()
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 320})
	pix := make([]byte, 100*400*4)
	rv.SetContent(pix, 100, 400)
	return rv
}

func TestRenderViewZoomButtons(t *testing.T) {
	rv := newTestRenderView(t)
	if rv.ZoomPercent() != 100 || rv.Zoom() != 1 {
		t.Fatalf("initial zoom = %d%% (%v), want 100%% (1.0)", rv.ZoomPercent(), rv.Zoom())
	}
	base := rv.img.Bounds().W

	// Zoom + (click the button through the surface-coord router).
	zin := rv.zoomIn.Bounds()
	if !rv.click(zin.X+zin.W/2, zin.Y+zin.H/2) {
		t.Fatalf("zoom-in click not consumed")
	}
	if rv.press != rvPressButton {
		t.Fatalf("zoom-in did not capture a button press")
	}
	if rv.ZoomPercent() != 125 {
		t.Fatalf("after zoom-in: %d%%, want 125%%", rv.ZoomPercent())
	}
	if rv.img.Bounds().W <= base {
		t.Fatalf("zoom-in did not grow the scroll content: %d -> %d", base, rv.img.Bounds().W)
	}
	// A release after a button press is consumed but scrolls nothing.
	if !rv.release(zin.X, zin.Y) {
		t.Fatalf("release after a button press should report handled")
	}

	// Zoom - back to 100%.
	zout := rv.zoomOut.Bounds()
	rv.click(zout.X+zout.W/2, zout.Y+zout.H/2)
	if rv.ZoomPercent() != 100 {
		t.Fatalf("after zoom-out: %d%%, want 100%%", rv.ZoomPercent())
	}

	// Fit: zoom so a page fits the content width (baseW=100, viewport W=240).
	fb := rv.fitBtn.Bounds()
	rv.click(fb.X+fb.W/2, fb.Y+fb.H/2)
	wantPct := int(float64(rv.scroll.Bounds().W)/100.0*100 + 0.5)
	if rv.ZoomPercent() != wantPct {
		t.Fatalf("after Fit: %d%%, want %d%% (fit width)", rv.ZoomPercent(), wantPct)
	}
}

func TestRenderViewZoomClamps(t *testing.T) {
	rv := newTestRenderView(t)
	rv.setZoom(1000) // clamps to the max
	if rv.ZoomPercent() != 800 {
		t.Fatalf("zoom did not clamp to max: %d%%", rv.ZoomPercent())
	}
	rv.setZoom(0.0001) // clamps to the min
	if rv.ZoomPercent() != 10 {
		t.Fatalf("zoom did not clamp to min: %d%%", rv.ZoomPercent())
	}
}

func TestRenderViewFitFallbacks(t *testing.T) {
	SetupText(1)
	rv := newRenderView()
	// No render at all: Fit falls back to 100%.
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 200, H: 200})
	rv.Fit()
	if rv.ZoomPercent() != 100 {
		t.Fatalf("Fit with no render = %d%%, want 100%%", rv.ZoomPercent())
	}
	// A render but an unmeasured viewport (zero bounds): also 100%.
	rv.SetContent(make([]byte, 10*10*4), 10, 10)
	rv.SetBounds(toolkit.Rect{})
	rv.Fit()
	if rv.ZoomPercent() != 100 {
		t.Fatalf("Fit with a zero viewport = %d%%, want 100%%", rv.ZoomPercent())
	}
}

func TestRenderViewEmptyContentCollapses(t *testing.T) {
	rv := newTestRenderView(t)
	rv.SetContent(nil, 0, 0)
	if rv.img.Bounds().W != 0 || rv.img.Bounds().H != 0 {
		t.Fatalf("empty content did not collapse the image size")
	}
}

func TestRenderViewScrollAreaDragAndWheel(t *testing.T) {
	rv := newTestRenderView(t)
	sb := rv.contentRect()

	// Press in the content area starts a pan capture; a drag then a release run
	// the scroll-forwarding branches.
	if !rv.click(sb.X+10, sb.Y+10) {
		t.Fatalf("content press not consumed")
	}
	if rv.press != rvPressScroll {
		t.Fatalf("content press did not capture the scroll")
	}
	if !rv.drag(sb.X+10, sb.Y+80) {
		t.Fatalf("scroll drag not consumed")
	}
	if !rv.release(sb.X+10, sb.Y+80) {
		t.Fatalf("scroll release not consumed")
	}
	// A drag with nothing captured is a no-op.
	if rv.drag(5, 5) {
		t.Fatalf("drag with no capture should be a no-op")
	}
	// A release with nothing captured reports not-handled.
	if rv.release(5, 5) {
		t.Fatalf("release with no capture should be a no-op")
	}

	// Wheel over the content scrolls; wheel outside is ignored.
	before := rv.offsetY()
	if !rv.scrollWheel(sb.X+10, sb.Y+10, 4) {
		t.Fatalf("wheel over content not consumed")
	}
	if rv.offsetY() <= before {
		t.Fatalf("wheel did not scroll: %d -> %d", before, rv.offsetY())
	}
	if rv.scrollWheel(-100, -100, 3) {
		t.Fatalf("wheel outside content should not be consumed")
	}
}

func TestRenderViewEmptyToolbarAndOutsideClick(t *testing.T) {
	rv := newTestRenderView(t)
	// A press in the toolbar row but on the (non-interactive) readout gap is
	// consumed with no capture.
	rr := rv.readoutRect
	if !rv.click(rr.X+rr.W/2, rr.Y+rr.H/2) {
		t.Fatalf("empty toolbar press should be consumed")
	}
	if rv.press != rvPressNone {
		t.Fatalf("empty toolbar press should capture nothing")
	}
	// A press below the whole widget is not consumed.
	b := rv.Bounds()
	if rv.click(b.X+5, b.Y+b.H+50) {
		t.Fatalf("press outside the render view should not be consumed")
	}
}

func TestRenderViewBaseContentYAt(t *testing.T) {
	rv := newTestRenderView(t)
	sb := rv.contentRect()

	// Inside the content, above the scrollbar gutter: maps to a base-render Y.
	if y, ok := rv.baseContentYAt(sb.X+5, sb.Y+10); !ok || y < 0 {
		t.Fatalf("baseContentYAt in content = (%d,%v), want ok with y>=0", y, ok)
	}
	// Over the scrollbar gutter: not ok.
	if _, ok := rv.baseContentYAt(sb.X+sb.W-1, sb.Y+10); ok {
		t.Fatalf("baseContentYAt over the scrollbar gutter should be not-ok")
	}
	// Outside the content: not ok.
	if _, ok := rv.baseContentYAt(-1, -1); ok {
		t.Fatalf("baseContentYAt outside the content should be not-ok")
	}
	// A degenerate zoom of 0 returns the raw content Y unscaled.
	rv.zoom = 0
	if y, ok := rv.baseContentYAt(sb.X+5, sb.Y+10); !ok || y < 0 {
		t.Fatalf("baseContentYAt with zoom 0 = (%d,%v)", y, ok)
	}
}

func TestRenderViewScrollToBaseY(t *testing.T) {
	rv := newTestRenderView(t)
	// A tiny target clamps to 0 (no negative scroll).
	rv.scrollToBaseY(0)
	if rv.offsetY() != 0 {
		t.Fatalf("scrollToBaseY(0) should stay at the top, got %d", rv.offsetY())
	}
	// A large target scrolls down.
	rv.scrollToBaseY(390)
	if rv.offsetY() <= 0 {
		t.Fatalf("scrollToBaseY(deep) did not scroll: %d", rv.offsetY())
	}
}

func TestRenderViewSetBoundsTinyHeight(t *testing.T) {
	SetupText(1)
	rv := newRenderView()
	// A height smaller than the toolbar clamps the toolbar and its button height.
	rv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 200, H: 4})
	if rv.toolbarH != 4 {
		t.Fatalf("toolbarH not clamped to the tiny height: %d", rv.toolbarH)
	}
}

func TestRenderViewDrawZeroBounds(t *testing.T) {
	SetupText(1)
	rv := newRenderView()
	rv.SetBounds(toolkit.Rect{})
	buf := make([]byte, 4)
	rv.Draw(painter.NewPixelPainter(buf, 1, 1), toolkit.DefaultLight()) // early-return, no panic
}

// --- #3 rightPane: tab strip over render | log -------------------------------

func newTestRightPane(t *testing.T) *rightPane {
	t.Helper()
	SetupText(1)
	rv := newRenderView()
	rp := newRightPane(rv, &logView{})
	rp.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 240, H: 320})
	rv.SetContent(make([]byte, 100*400*4), 100, 400)
	return rp
}

func TestRightPaneTabSwitchAndRouting(t *testing.T) {
	rp := newTestRightPane(t)
	tb := rp.tabs.Bounds()

	// Click the Log tab (right half): switch to the log.
	if !rp.click(tb.X+tb.W*3/4, tb.Y+tb.H/2) {
		t.Fatalf("tab click not consumed")
	}
	if rp.press != rpPressTabs || !rp.isLog() {
		t.Fatalf("Log tab did not activate (press=%d, isLog=%v)", rp.press, rp.isLog())
	}

	// A click in the log content is consumed but captures nothing (scroll-only).
	cr := rp.contentRect()
	if !rp.click(cr.X+10, cr.Y+10) {
		t.Fatalf("log content click should be consumed")
	}
	if rp.press != rpPressNone {
		t.Fatalf("log content click should capture nothing")
	}

	// Wheel over the log scrolls its own offset; wheel on the tab strip is inert;
	// wheel outside is not consumed.
	if !rp.scrollWheel(cr.X+10, cr.Y+10, 4) || rp.log.offset <= 0 {
		t.Fatalf("log wheel did not scroll: offset=%d", rp.log.offset)
	}
	if !rp.scrollWheel(tb.X+2, tb.Y+2, 1) {
		t.Fatalf("wheel on the tab strip should be consumed (inert)")
	}
	if rp.scrollWheel(-5, -5, 1) {
		t.Fatalf("wheel outside the pane should not be consumed")
	}

	// Back to the render tab: a press in the render's SCROLL area (below its zoom
	// toolbar) captures the render, then drag + release forward to it.
	rp.setActive(tabRender)
	rsb := rp.render.contentRect()
	if !rp.click(rsb.X+10, rsb.Y+10) || rp.press != rpPressRender {
		t.Fatalf("render content press did not capture the render (press=%d)", rp.press)
	}
	if !rp.drag(rsb.X+10, rsb.Y+40) {
		t.Fatalf("render drag not routed")
	}
	if !rp.release(rsb.X+10, rsb.Y+40) {
		t.Fatalf("render release not routed")
	}
	// Idle drag / release report not-handled.
	if rp.drag(1, 1) || rp.release(1, 1) {
		t.Fatalf("idle drag/release should be no-ops")
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

// --- #2 minimap helpers ------------------------------------------------------

func TestAtLeast1(t *testing.T) {
	if atLeast1(0) != 1 || atLeast1(-3) != 1 {
		t.Fatalf("atLeast1 did not floor non-positive values at 1")
	}
	if atLeast1(5) != 5 {
		t.Fatalf("atLeast1 changed a positive value")
	}
}

func TestMinimapMaxColsClampAtScale2(t *testing.T) {
	defer SetupText(1)
	SetupText(2) // charW = scaled(1) = 2, so a very narrow strip clamps maxCols to 1
	m := &minimap{}
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 5, H: 40})
	m.update([]string{"abcdef"}, nil, 0, 1)
	buf := make([]byte, 5*40*4)
	m.Draw(painter.NewPixelPainter(buf, 5, 40), toolkit.DefaultLight())
	if got := m.segmentCount(toolkit.DefaultLight().OnSurface); got != 1 {
		t.Fatalf("a narrow strip should clip to a single 1-char segment, got %d", got)
	}
}

func TestSpanColorAtFallback(t *testing.T) {
	fallback := toolkit.RGB(1, 2, 3)
	spans := []toolkit.TextSpan{{Start: 2, End: 4, Color: toolkit.RGB(9, 9, 9)}}
	if got := spanColorAt(spans, 3, fallback); got != (toolkit.RGB(9, 9, 9)) {
		t.Fatalf("spanColorAt inside a span = %v", got)
	}
	if got := spanColorAt(spans, 10, fallback); got != fallback {
		t.Fatalf("spanColorAt outside every span should fall back, got %v", got)
	}
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
	if n := s.MinimapSegments(); n <= 0 {
		t.Fatalf("sample document should paint minimap segments, got %d", n)
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
	for _, name := range []string{"picker", "popover", "renderTab", "logTab", "zoomOut", "zoomIn", "fit", "renderContent"} {
		v, ok := r[name]
		if !ok {
			t.Fatalf("DebugRects missing %q", name)
		}
		if v[2] <= 0 || v[3] <= 0 {
			t.Fatalf("DebugRects[%q] has a non-positive size: %v", name, v)
		}
	}
	// The two tabs partition the tab strip with no overlap.
	if r["renderTab"][0]+r["renderTab"][2] != r["logTab"][0] {
		t.Fatalf("tab rects should be contiguous: %v | %v", r["renderTab"], r["logTab"])
	}
}

func TestClickZoomAndTabThroughState(t *testing.T) {
	s := newTestState(t, false)

	// Clicking the Log tab through the full State router switches the pane.
	tb := s.rightPane.tabs.Bounds()
	if !s.HandleClick(tb.X+tb.W*3/4, tb.Y+tb.H/2) {
		t.Fatalf("tab click not consumed by State")
	}
	if !s.showLog() {
		t.Fatalf("State did not switch to the Log tab")
	}
	s.HandleRelease(tb.X+tb.W*3/4, tb.Y+tb.H/2)

	// Back to Rendered, then click the zoom-in button through State.
	s.HandleClick(tb.X+tb.W/4, tb.Y+tb.H/2)
	zin := s.renderView.zoomIn.Bounds()
	if !s.HandleClick(zin.X+zin.W/2, zin.Y+zin.H/2) {
		t.Fatalf("zoom-in click not consumed by State")
	}
	if s.ZoomPercent() != 125 {
		t.Fatalf("zoom-in through State = %d%%, want 125%%", s.ZoomPercent())
	}
	s.HandleMove(zin.X+zin.W/2, zin.Y+zin.H/2) // pressRight drag path (button: inert)
	s.HandleRelease(zin.X+zin.W/2, zin.Y+zin.H/2)
}
