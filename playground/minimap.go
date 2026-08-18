// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// minimap is a scaled overview of the editor buffer: one thin bar per source
// line (width proportional to the line's trimmed length, comments dimmed), with
// a translucent viewport indicator over the currently-visible line range. It is
// a scroll thumbnail — the host maps a click/drag on it back to a buffer line
// and scrolls the editor there (see State.minimapScrollTo).
//
// It is draw-only: State refreshes lines/top/visible before each paint and owns
// the pointer mapping, so the widget stays a passive overview.
type minimap struct {
	toolkit.Base

	lines   []string // reference to the editor buffer lines
	top     int      // first visible buffer line (editor ScrollLine)
	visible int      // number of lines visible in the editor viewport
}

// update refreshes the overview inputs before a paint.
func (m *minimap) update(lines []string, top, visible int) {
	m.lines, m.top, m.visible = lines, top, visible
}

// lineAtY maps a widget-local y (device pixels) to a 0-based buffer line.
func (m *minimap) lineAtY(y int) int {
	r := m.Bounds()
	n := len(m.lines)
	if n == 0 || r.H <= 0 {
		return 0
	}
	rel := y - r.Y
	if rel < 0 {
		rel = 0
	}
	line := rel * n / r.H
	if line >= n {
		line = n - 1
	}
	return line
}

// Draw paints the overview and the viewport indicator.
func (m *minimap) Draw(p painter.Painter, theme *toolkit.Theme) {
	r := m.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	p.FillRect(r, theme.SurfaceAlt)
	p.FillRect(toolkit.Rect{X: r.X, Y: r.Y, W: toolkit.Scaled(1), H: r.H}, theme.Border) // left divider

	n := len(m.lines)
	if n == 0 {
		return
	}
	maxLen := 1
	for _, ln := range m.lines {
		if l := len([]rune(ln)); l > maxLen {
			maxLen = l
		}
	}
	pad := toolkit.Scaled(3)
	usableW := r.W - 2*pad
	if usableW < 1 {
		usableW = 1
	}
	ink := theme.OnSurface
	dim := blend(theme.OnSurface, theme.SurfaceAlt, 0.55)
	rowH := r.H / n
	if rowH < 1 {
		rowH = 1
	}
	barH := rowH - 1
	if barH < 1 {
		barH = 1
	}
	for i, ln := range m.lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		w := len([]rune(ln)) * usableW / maxLen
		if w < 1 {
			w = 1
		}
		col := ink
		if strings.HasPrefix(t, "%") {
			col = dim
		}
		y := r.Y + i*r.H/n
		p.FillRect(toolkit.Rect{X: r.X + pad, Y: y, W: w, H: barH}, col)
	}

	// Viewport indicator: a translucent accent band over the visible range.
	if m.visible > 0 {
		vy := r.Y + m.top*r.H/n
		vh := m.visible * r.H / n
		if vh < toolkit.Scaled(6) {
			vh = toolkit.Scaled(6)
		}
		if vy+vh > r.Y+r.H {
			vh = r.Y + r.H - vy
		}
		band := toolkit.Rect{X: r.X, Y: vy, W: r.W, H: vh}
		p.FillRect(band, toolkit.RGBA{R: theme.Accent.R, G: theme.Accent.G, B: theme.Accent.B, A: 0x33})
		p.StrokeRect(band, theme.Accent, toolkit.Scaled(1))
	}
}

// blend mixes a toward b by t in [0,1] (opaque result).
func blend(a, b toolkit.RGBA, t float64) toolkit.RGBA {
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return toolkit.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 0xFF}
}
