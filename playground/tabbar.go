// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// tabBar is a compact, folder-style tab strip: a row of small tabs (sized to
// their label, not the full width) with rounded TOP corners sitting on a strip.
// The active tab is filled in the surface colour, carries an accent bar along
// its top edge, and covers the strip's bottom border so it reads as connected to
// the pane below; inactive tabs are dimmed and sit above the border line. This
// replaces the former full-width ViewSwitcher, which looked like two oversized
// buttons rather than tabs.
//
// It is draw-only + surface-coordinate hit-testing (tabAt), matching how the
// rest of the playground routes pointer input.
type tabBar struct {
	toolkit.Base

	labels   []string
	active   int
	onChange func(int)
}

// newTabBar builds a tab bar over labels with the first tab active.
func newTabBar(labels []string, onChange func(int)) *tabBar {
	return &tabBar{labels: labels, active: 0, onChange: onChange}
}

// height is the strip's device height — deliberately shorter than the old
// ViewSwitcher so the tabs read as slim tabs, not buttons.
func (t *tabBar) height() int { return toolkit.Scaled(24) }

// setActive selects tab i and fires onChange (nil-safe).
func (t *tabBar) setActive(i int) {
	if i < 0 || i >= len(t.labels) {
		return
	}
	t.active = i
	if t.onChange != nil {
		t.onChange(i)
	}
}

// tabTop is the y at which a tab's rounded body begins (a small gap above it
// keeps the tabs off the very top edge of the strip).
func (t *tabBar) tabTop() int { return t.Bounds().Y + toolkit.Scaled(3) }

// tabRect returns the device rectangle of tab i: sized to its label plus
// padding, laid left-to-right from a small left margin. Inactive tabs stop one
// device pixel above the strip's bottom border (so the border shows beneath
// them); the active tab is extended to the strip bottom in Draw so it covers the
// border and connects to the pane.
func (t *tabBar) tabRect(i int) toolkit.Rect {
	if i < 0 || i >= len(t.labels) {
		return toolkit.Rect{}
	}
	r := t.Bounds()
	padX := toolkit.Scaled(12)
	gap := toolkit.Scaled(3)
	x := r.X + toolkit.Scaled(6)
	for k := 0; k < i; k++ {
		x += toolkit.TextWidth(t.labels[k]) + 2*padX + gap
	}
	w := toolkit.TextWidth(t.labels[i]) + 2*padX
	top := t.tabTop()
	h := r.Y + r.H - top
	if h < 1 {
		h = 1
	}
	return toolkit.Rect{X: x, Y: top, W: w, H: h}
}

// tabAt returns the tab index under a surface point, or -1 when none.
func (t *tabBar) tabAt(x, y int) int {
	for i := range t.labels {
		if t.tabRect(i).Contains(x, y) {
			return i
		}
	}
	return -1
}

// Draw paints the strip, its bottom border, then each tab (active last so it
// covers the border and sits on top).
func (t *tabBar) Draw(p painter.Painter, theme *toolkit.Theme) {
	r := t.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	p.FillRect(r, theme.SurfaceAlt)
	borderH := atLeast1(toolkit.Scaled(1))
	// Strip bottom border (separates the strip from the pane below).
	p.FillRect(toolkit.Rect{X: r.X, Y: r.Y + r.H - borderH, W: r.W, H: borderH}, theme.Border)

	rad := toolkit.Scaled(6)
	dimFace := mixRGBA(theme.SurfaceAlt, theme.Background, 0.5)
	dimInk := mixRGBA(theme.OnSurface, theme.SurfaceAlt, 0.5)

	for i := range t.labels {
		if i == t.active {
			continue // drawn last, on top
		}
		t.drawTab(p, theme, i, dimFace, dimInk, false, rad)
	}
	if t.active >= 0 && t.active < len(t.labels) {
		t.drawTab(p, theme, t.active, theme.Surface, theme.OnSurface, true, rad)
	}
}

// drawTab paints one tab: a top-rounded body in face, its centred label in ink,
// and — for the active tab — an accent bar along the top and a body extended to
// cover the strip's bottom border.
func (t *tabBar) drawTab(p painter.Painter, theme *toolkit.Theme, i int, face, ink toolkit.RGBA, active bool, rad int) {
	tr := t.tabRect(i)
	r := t.Bounds()
	if active {
		tr.H = r.Y + r.H - tr.Y // extend over the border to connect to the pane
	} else {
		tr.H = atLeast1(tr.H - atLeast1(toolkit.Scaled(1))) // leave the border visible below
	}
	fillTopRoundRect(p, tr, rad, face)
	if active {
		p.FillRect(toolkit.Rect{X: tr.X, Y: tr.Y, W: tr.W, H: atLeast1(toolkit.Scaled(2))}, theme.Accent)
	}
	label := t.labels[i]
	tw := toolkit.TextWidth(label)
	tx := tr.X + (tr.W-tw)/2
	ty := tr.Y + (t.height()-toolkit.GlyphHeight())/2
	toolkit.DrawText(p, tx, ty, label, ink)
}

// fillTopRoundRect fills r with only its TOP corners rounded to rad: a rounded
// rect over-painted along its bottom rad to square the bottom corners.
func fillTopRoundRect(p painter.Painter, r toolkit.Rect, rad int, c toolkit.RGBA) {
	if rad < 1 || r.W <= 0 || r.H <= 0 {
		p.FillRect(r, c)
		return
	}
	p.FillRoundRect(r, rad, c)
	if r.H > rad {
		p.FillRect(toolkit.Rect{X: r.X, Y: r.Y + r.H - rad, W: r.W, H: rad}, c)
	}
}

// atLeast1 floors v at one device pixel, so a metric-scaled dimension never
// collapses to nothing (a 1px border stays visible at any scale).
func atLeast1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

// mixRGBA blends a toward b by t in [0,1], returning an opaque colour.
func mixRGBA(a, b toolkit.RGBA, t float64) toolkit.RGBA {
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return toolkit.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 0xFF}
}
