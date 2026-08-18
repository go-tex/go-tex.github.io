// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// rightPane is the composed right-hand side of the split: a small ViewSwitcher
// tab strip (Rendered | Log) pinned to its top, with the active tab's content
// filling the rest — the composed renderView on the Rendered tab, the diagnostics
// logView on the Log tab. Selecting a tab (a click on the strip) swaps which
// content shows; the tabs replace the former standalone "Log" toolbar toggle.
//
// It is the Second child of the Paned, so the Paned sizes and draws it; State
// drives its pointer input through surface-coordinate methods (matching how
// State routes every other region) and reads the active tab / render for the
// app-level source-navigation links.
type rightPane struct {
	toolkit.Base

	tabs   *toolkit.ViewSwitcher
	render *renderView
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

// newRightPane composes the tab strip over the given render and log views.
func newRightPane(rv *renderView, lv *logView) *rightPane {
	rp := &rightPane{render: rv, log: lv}
	rp.tabs = toolkit.NewViewSwitcher([]string{"Rendered", "Log"}, tabRender)
	rp.tabs.OnChange = func(i int) { rp.active = i }
	return rp
}

// isLog reports whether the Log tab is active.
func (rp *rightPane) isLog() bool { return rp.active == tabLog }

// setActive selects a tab and keeps the switcher's highlight in sync (used when
// the tab changes from code rather than a strip click).
func (rp *rightPane) setActive(tab int) {
	rp.active = tab
	rp.tabs.Current = tab
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
	rp.tabH = toolkit.ViewSwitcherHeight()
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

// click routes a surface-coordinate press: onto the tab strip (switch tabs), or
// into the active content. It captures the target for the following drag.
func (rp *rightPane) click(x, y int) bool {
	rp.press = rpPressNone
	if rp.inTabs(y) {
		tb := rp.tabs.Bounds()
		rp.tabs.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - tb.X, Y: y - tb.Y})
		rp.press = rpPressTabs
		return true
	}
	if rp.isLog() {
		return true // the log view is scroll-only: consume the press, nothing to route
	}
	if rp.render.click(x, y) {
		rp.press = rpPressRender
		return true
	}
	return false
}

// drag forwards a captured drag to the render content (the tab strip and the log
// view have nothing to drag).
func (rp *rightPane) drag(x, y int) bool {
	if rp.press == rpPressRender {
		return rp.render.drag(x, y)
	}
	return false
}

// release ends a captured drag.
func (rp *rightPane) release(x, y int) bool {
	was := rp.press
	if rp.press == rpPressRender {
		rp.render.release(x, y)
	}
	rp.press = rpPressNone
	return was != rpPressNone
}

// scrollWheel forwards a wheel scroll to the active content (the log view scrolls
// its own offset; the render view scrolls its inner ScrollView). A scroll on the
// tab strip is consumed but inert.
func (rp *rightPane) scrollWheel(x, y, delta int) bool {
	if !rp.Bounds().Contains(x, y) {
		return false
	}
	if rp.inTabs(y) {
		return true
	}
	if rp.isLog() {
		rp.log.offset += delta * rowStep
		if rp.log.offset < 0 {
			rp.log.offset = 0
		}
		return true
	}
	return rp.render.scrollWheel(x, y, delta)
}
