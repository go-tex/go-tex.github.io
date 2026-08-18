// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// rightPane is the composed right-hand side of the split: a compact folder-style
// tab strip (Rendered | Log) pinned to its top, with the active tab's content
// filling the rest — the shared [toolkit.PagedView] on the Rendered tab, the
// diagnostics logView on the Log tab. Selecting a tab (a click on the strip)
// swaps which content shows.
//
// It is the Second child of the Paned, so the Paned sizes and draws it; State
// drives its pointer + keyboard input through surface-coordinate methods
// (matching how State routes every other region) and reads the active tab for
// the app-level tab links. The render tab's paging, zoom, toolbar and scroll are
// entirely owned by the PagedView — this pane only translates surface
// coordinates into the widget's local frame and forwards the event.
type rightPane struct {
	toolkit.Base

	tabs   *tabBar
	render *toolkit.PagedView
	log    *logView

	active int
	tabH   int
	press  int // pointer-capture target for the current drag
}

// right-pane tab indices and pointer-capture targets.
const (
	tabRender = 0
	tabLog    = 1

	rpPressNone = iota
	rpPressTabs
	rpPressRender
)

// newRightPane composes the tab strip over the given render (PagedView) and log
// views.
func newRightPane(rv *toolkit.PagedView, lv *logView) *rightPane {
	rp := &rightPane{render: rv, log: lv}
	rp.tabs = newTabBar([]string{"Rendered", "Log"}, func(i int) { rp.active = i })
	return rp
}

// tabRect returns the device rectangle of tab i (host introspection).
func (rp *rightPane) tabRect(i int) toolkit.Rect { return rp.tabs.tabRect(i) }

// isLog reports whether the Log tab is active.
func (rp *rightPane) isLog() bool { return rp.active == tabLog }

// setActive selects a tab and keeps the strip's highlight in sync (used when the
// tab changes from code rather than a strip click).
func (rp *rightPane) setActive(tab int) {
	rp.active = tab
	rp.tabs.active = tab
}

// contentRect is the region below the tab strip that the active content fills.
func (rp *rightPane) contentRect() toolkit.Rect {
	r := rp.Bounds()
	cy := r.Y + rp.tabH
	ch := r.H - rp.tabH
	if ch < 0 {
		ch = 0
	}
	return toolkit.Rect{X: r.X, Y: cy, W: r.W, H: ch}
}

// SetBounds pins the tab strip to the top and lays both content views into the
// remaining area (only the active one draws).
func (rp *rightPane) SetBounds(r toolkit.Rect) {
	rp.Base.SetBounds(r)
	rp.tabH = rp.tabs.height()
	if rp.tabH > r.H {
		rp.tabH = r.H
	}
	rp.tabs.SetBounds(toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: rp.tabH})
	content := rp.contentRect()
	rp.render.SetBounds(content)
	rp.log.SetBounds(content)
}

// Draw paints the tab strip then the active tab's content.
func (rp *rightPane) Draw(p painter.Painter, theme *toolkit.Theme) {
	rp.tabs.Draw(p, theme)
	if rp.isLog() {
		rp.log.Draw(p, theme)
	} else {
		rp.render.Draw(p, theme)
	}
}

// inTabs reports whether surface-y falls on the tab strip.
func (rp *rightPane) inTabs(y int) bool {
	r := rp.Bounds()
	return y >= r.Y && y < r.Y+rp.tabH
}

// local translates a surface point into the render PagedView's own coordinate
// frame (origin at its top-left), the space its OnEvent expects.
func (rp *rightPane) local(x, y int) (int, int) {
	b := rp.render.Bounds()
	return x - b.X, y - b.Y
}

// click routes a surface-coordinate press: onto the tab strip (switch tabs), or
// into the active content. On the render tab it forwards a widget-local
// EventClick to the PagedView (which routes it to a toolbar button or its inner
// scroll pane) and captures the render for the following drag.
func (rp *rightPane) click(x, y int) bool {
	rp.press = rpPressNone
	if rp.inTabs(y) {
		if idx := rp.tabs.tabAt(x, y); idx >= 0 {
			rp.tabs.setActive(idx) // onChange updates rp.active
		}
		rp.press = rpPressTabs
		return true
	}
	if rp.isLog() {
		return true // the log view is scroll-only: consume the press, nothing to route
	}
	if rp.render.Bounds().Contains(x, y) {
		lx, ly := rp.local(x, y)
		rp.render.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: lx, Y: ly})
		rp.press = rpPressRender
		return true
	}
	return false
}

// drag forwards a captured drag to the PagedView (a grabbed scrollbar thumb or a
// content pan). The tab strip and the log view have nothing to drag.
func (rp *rightPane) drag(x, y int) bool {
	if rp.press == rpPressRender {
		lx, ly := rp.local(x, y)
		rp.render.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: lx, Y: ly})
		return true
	}
	return false
}

// release ends a captured drag, delivering the mouse-up to the PagedView.
func (rp *rightPane) release(x, y int) bool {
	was := rp.press
	if rp.press == rpPressRender {
		lx, ly := rp.local(x, y)
		rp.render.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: lx, Y: ly})
	}
	rp.press = rpPressNone
	return was != rpPressNone
}

// scrollWheel forwards a wheel/two-finger scroll to the active content: the log
// view scrolls its own offset vertically; the render PagedView receives a
// widget-local EventScroll on BOTH axes (so a horizontal swipe pans the page and
// a vertical wheel scrolls-or-flips pages per its own edge logic). A scroll on
// the tab strip is consumed but inert.
func (rp *rightPane) scrollWheel(x, y, dx, dy int) bool {
	if !rp.Bounds().Contains(x, y) {
		return false
	}
	if rp.inTabs(y) {
		return true
	}
	if rp.isLog() {
		rp.log.offset += dy * rowStep
		if rp.log.offset < 0 {
			rp.log.offset = 0
		}
		return true
	}
	lx, ly := rp.local(x, y)
	rp.render.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, X: lx, Y: ly, Delta: dy, DeltaX: dx})
	return true
}

// keyDown forwards a navigation key to the render PagedView (PageDown / ArrowUp /
// Home / End / Space etc.), returning whether it was routed. It is called only
// when the render tab is active and holds keyboard focus, so a key never reaches
// both the editor and the page viewer.
func (rp *rightPane) keyDown(code string) bool {
	if rp.isLog() {
		return false
	}
	rp.render.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code})
	return true
}
