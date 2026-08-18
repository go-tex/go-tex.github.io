// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// This file draws the render toolbar's icons as simple vector shapes through the
// toolkit painter — no icon fonts, no bitmaps. A magnifier (loupe: a circle plus
// a diagonal handle) with a + or - inside stands for zoom-in / zoom-out; a framed
// double-headed horizontal arrow stands for fit-to-width. They are handed to each
// Button via its Icon seam, so the buttons stay ordinary clickable/keyboard
// controls (the label is kept for accessibility; the icon just replaces the
// glyph).

// absI is |v|.
func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// stroke is the icon line weight in device pixels.
func stroke() int { return atLeast1(toolkit.Scaled(1)) }

// putDot paints a lineW×lineW square at (x,y): the thick "pen" the line/segment
// helpers stamp along their path.
func putDot(p painter.Painter, x, y, lineW int, ink toolkit.RGBA) {
	p.FillRect(toolkit.Rect{X: x, Y: y, W: lineW, H: lineW}, ink)
}

// drawLine stamps a lineW-thick line from (x0,y0) to (x1,y1) using integer DDA.
func drawLine(p painter.Painter, x0, y0, x1, y1, lineW int, ink toolkit.RGBA) {
	dx, dy := x1-x0, y1-y0
	steps := absI(dx)
	if absI(dy) > steps {
		steps = absI(dy)
	}
	if steps == 0 {
		putDot(p, x0, y0, lineW, ink)
		return
	}
	for i := 0; i <= steps; i++ {
		putDot(p, x0+dx*i/steps, y0+dy*i/steps, lineW, ink)
	}
}

// iconSquare returns the centred square (x, y, side) the icon draws inside r: the
// smaller side scaled to ~60% so the glyph has padding within the button.
func iconSquare(r toolkit.Rect) (x, y, d int) {
	d = r.H
	if r.W < d {
		d = r.W
	}
	d = d * 3 / 5
	if d < 6 {
		d = 6
	}
	x = r.X + (r.W-d)/2
	y = r.Y + (r.H-d)/2
	return x, y, d
}

// drawZoomIcon paints a magnifier with a + (plus) or - inside the lens.
func drawZoomIcon(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA, plus bool) {
	x, y, d := iconSquare(r)
	lw := stroke()
	lens := d * 2 / 3 // lens diameter
	lr := lens / 2
	// Lens: a circle approximated by a max-radius rounded square.
	p.StrokeRoundRect(toolkit.Rect{X: x, Y: y, W: lens, H: lens}, lr, ink, lw)
	// Handle: a diagonal from the lens's lower-right toward the icon's corner.
	hx := x + lens - lens/6
	hy := y + lens - lens/6
	drawLine(p, hx, hy, x+d, y+d, lw, ink)
	// +/- inside the lens.
	cx, cy := x+lr, y+lr
	bar := lr // full bar span across the lens interior
	p.FillRect(toolkit.Rect{X: cx - bar/2, Y: cy - lw/2, W: bar, H: lw}, ink)
	if plus {
		p.FillRect(toolkit.Rect{X: cx - lw/2, Y: cy - bar/2, W: lw, H: bar}, ink)
	}
}

// drawFitIcon paints a framed double-headed horizontal arrow — "fit to width".
func drawFitIcon(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) {
	x, y, d := iconSquare(r)
	lw := stroke()
	frame := toolkit.Rect{X: x, Y: y + d/6, W: d, H: d - d/3}
	p.StrokeRoundRect(frame, toolkit.Scaled(2), ink, lw)
	midY := frame.Y + frame.H/2
	pad := atLeast1(d / 6)
	leftX := frame.X + pad
	rightX := frame.X + frame.W - pad
	ah := atLeast1(d / 6) // arrowhead size
	// Shaft.
	drawLine(p, leftX, midY, rightX, midY, lw, ink)
	// Left arrowhead (points left).
	drawLine(p, leftX, midY, leftX+ah, midY-ah, lw, ink)
	drawLine(p, leftX, midY, leftX+ah, midY+ah, lw, ink)
	// Right arrowhead (points right).
	drawLine(p, rightX, midY, rightX-ah, midY-ah, lw, ink)
	drawLine(p, rightX, midY, rightX-ah, midY+ah, lw, ink)
}

// drawChevron paints a < or > chevron (page Prev / Next). right=true points right.
func drawChevron(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA, right bool) {
	x, y, d := iconSquare(r)
	lw := stroke()
	cy := y + d/2
	// The tip and the two arms of the chevron.
	tipX, backX := x+d/3, x+2*d/3
	if right {
		tipX, backX = x+2*d/3, x+d/3
	}
	drawLine(p, backX, y+d/4, tipX, cy, lw, ink)
	drawLine(p, backX, y+3*d/4, tipX, cy, lw, ink)
}
