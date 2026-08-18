// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strconv"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// renderView is the composed "Rendered" tab widget: a small zoom toolbar
// (zoom -, a percentage readout, zoom +, Fit-to-width) stacked above a
// scrollable, zoomable image of the rasterized gotex pages. It owns the toolbar
// buttons and the inner ScrollView+Image and routes its own pointer events, so
// State only holds it, feeds it a render (SetContent) and lays it out — the
// zoom/scroll logic stays inside this one widget instead of being scattered
// across State.
//
// Zoom scales the already-rasterized render by stretching the blit (the inner
// Image nearest-neighbour-samples its base pixels onto its zoomed bounds); the
// base pixel buffer is rasterized once per compile. Fit resets the zoom so one
// page fits the pane width.
type renderView struct {
	toolkit.Base

	img    *toolkit.Image
	scroll *toolkit.ScrollView

	zoomOut *toolkit.Button
	zoomIn  *toolkit.Button
	fitBtn  *toolkit.Button

	basePix      []byte // base RGBA render, baseW*baseH*4
	baseW, baseH int    // base render size (device px at the engine raster scale)
	zoom         float64

	toolbarH    int
	readoutRect toolkit.Rect

	press int // which sub-target captured the current drag
}

// renderView pointer-capture targets.
const (
	rvPressNone = iota
	rvPressButton
	rvPressScroll
)

// Zoom limits and the multiplicative step of the +/- buttons.
const (
	zoomStep = 1.25
	zoomMin  = 0.1
	zoomMax  = 8.0
)

// rvScrollbarW is the device width reserved for the render pane's vertical
// scrollbar gutter, so a press there scrolls rather than navigating to source.
const rvScrollbarW = 16

// newRenderView builds the composed render widget with an empty render and a
// 1:1 (100%) zoom.
func newRenderView() *renderView {
	rv := &renderView{zoom: 1}
	rv.img = toolkit.NewImage(nil, 0, 0)
	rv.scroll = toolkit.NewScrollView(rv.img)
	// Icons (drawn via the Button.Icon seam) replace the text; the labels stay for
	// accessibility naming.
	rv.zoomOut = toolkit.NewButton("Zoom out", func() { rv.zoomBy(1.0 / zoomStep) })
	rv.zoomOut.Icon = func(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) { drawZoomIcon(p, r, ink, false) }
	rv.zoomIn = toolkit.NewButton("Zoom in", func() { rv.zoomBy(zoomStep) })
	rv.zoomIn.Icon = func(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) { drawZoomIcon(p, r, ink, true) }
	rv.fitBtn = toolkit.NewButton("Fit width", func() { rv.Fit() })
	rv.fitBtn.Icon = func(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) { drawFitIcon(p, r, ink) }
	return rv
}

// SetContent installs a freshly rasterized render (or clears it with nil,0,0)
// and re-applies the current zoom so the scroll content size tracks it.
func (rv *renderView) SetContent(pix []byte, w, h int) {
	rv.basePix, rv.baseW, rv.baseH = pix, w, h
	rv.img.Pixels, rv.img.W, rv.img.H = pix, w, h
	rv.applyZoom()
}

// applyZoom recomputes the zoomed image bounds and the scroll content size from
// the base size and the current zoom. An empty render collapses to zero.
func (rv *renderView) applyZoom() {
	zw := int(float64(rv.baseW)*rv.zoom + 0.5)
	zh := int(float64(rv.baseH)*rv.zoom + 0.5)
	if rv.baseW <= 0 || rv.baseH <= 0 {
		zw, zh = 0, 0
	}
	rv.img.SetBounds(toolkit.Rect{W: zw, H: zh})
	rv.scroll.SetContentSize(zw, zh)
}

// zoomBy multiplies the zoom by f (clamped); setZoom re-applies the sizing.
func (rv *renderView) zoomBy(f float64) { rv.setZoom(rv.zoom * f) }

func (rv *renderView) setZoom(z float64) {
	if z < zoomMin {
		z = zoomMin
	}
	if z > zoomMax {
		z = zoomMax
	}
	rv.zoom = z
	rv.applyZoom()
}

// Fit sets the zoom so one page fits the content viewport width. With no render
// (or an unmeasured viewport) it falls back to 100%.
func (rv *renderView) Fit() {
	if rv.baseW <= 0 {
		rv.setZoom(1)
		return
	}
	vw := rv.scroll.Bounds().W
	if vw <= 0 {
		rv.setZoom(1)
		return
	}
	rv.setZoom(float64(vw) / float64(rv.baseW))
}

// Zoom returns the current zoom factor; ZoomPercent its rounded percentage.
func (rv *renderView) Zoom() float64 { return rv.zoom }
func (rv *renderView) ZoomPercent() int {
	return int(rv.zoom*100 + 0.5)
}

// offsetY / offsetX are the inner scroll offsets (device px), for introspection.
func (rv *renderView) offsetY() int { return rv.scroll.OffsetY }
func (rv *renderView) offsetX() int { return rv.scroll.OffsetX }

// contentRect is the scrollable image area (below the toolbar), where the error
// overlay and the source-navigation hit-test live.
func (rv *renderView) contentRect() toolkit.Rect { return rv.scroll.Bounds() }

// SetBounds lays out the zoom toolbar row and the scroll area beneath it.
func (rv *renderView) SetBounds(r toolkit.Rect) {
	rv.Base.SetBounds(r)
	rv.toolbarH = toolkit.Scaled(28)
	if rv.toolbarH > r.H {
		rv.toolbarH = r.H
	}
	pad := toolkit.Scaled(4)
	gap := toolkit.Scaled(4)
	bh := rv.toolbarH - 2*toolkit.Scaled(3)
	if bh < 1 {
		bh = rv.toolbarH
	}
	by := r.Y + (rv.toolbarH-bh)/2
	bw := toolkit.Scaled(26)
	x := r.X + pad
	rv.zoomOut.SetBounds(toolkit.Rect{X: x, Y: by, W: bw, H: bh})
	x += bw + gap
	readoutW := toolkit.Scaled(48)
	rv.readoutRect = toolkit.Rect{X: x, Y: r.Y, W: readoutW, H: rv.toolbarH}
	x += readoutW + gap
	rv.zoomIn.SetBounds(toolkit.Rect{X: x, Y: by, W: bw, H: bh})
	x += bw + gap
	rv.fitBtn.SetBounds(toolkit.Rect{X: x, Y: by, W: bw, H: bh})

	cy := r.Y + rv.toolbarH
	ch := r.H - rv.toolbarH // >= 0: toolbarH is clamped to r.H above
	rv.scroll.SetBounds(toolkit.Rect{X: r.X, Y: cy, W: r.W, H: ch})
}

// Draw paints the zoom toolbar and the scrollable zoomed render.
func (rv *renderView) Draw(p painter.Painter, theme *toolkit.Theme) {
	r := rv.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	p.FillRect(toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: rv.toolbarH}, theme.Surface)
	p.FillRect(toolkit.Rect{X: r.X, Y: r.Y + rv.toolbarH - toolkit.Scaled(1), W: r.W, H: toolkit.Scaled(1)}, theme.Border)

	rv.zoomOut.Draw(p, theme)
	rv.zoomIn.Draw(p, theme)
	rv.fitBtn.Draw(p, theme)

	txt := strconv.Itoa(rv.ZoomPercent()) + "%"
	tw := toolkit.TextWidth(txt)
	tx := rv.readoutRect.X + (rv.readoutRect.W-tw)/2
	ty := rv.readoutRect.Y + (rv.readoutRect.H-toolkit.GlyphHeight())/2
	toolkit.DrawText(p, tx, ty, txt, theme.OnSurface)

	rv.scroll.Draw(p, theme)
}

// inToolbar reports whether surface-y falls in the zoom toolbar row.
func (rv *renderView) inToolbar(y int) bool {
	r := rv.Bounds()
	return y >= r.Y && y < r.Y+rv.toolbarH
}

// click routes a surface-coordinate press. A toolbar-button hit fires the button
// (buttons act on press); a press in the scroll area is forwarded to the inner
// ScrollView (grabbing a thumb or starting a pan). It captures the target for
// the following drag/release.
func (rv *renderView) click(x, y int) bool {
	rv.press = rvPressNone
	if rv.inToolbar(y) {
		for _, b := range []*toolkit.Button{rv.zoomOut, rv.zoomIn, rv.fitBtn} {
			bb := b.Bounds()
			if bb.Contains(x, y) {
				b.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - bb.X, Y: y - bb.Y})
				rv.press = rvPressButton
				return true
			}
		}
		return true // empty toolbar area: consumed, nothing to do
	}
	sb := rv.scroll.Bounds()
	if sb.Contains(x, y) {
		rv.scroll.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - sb.X, Y: y - sb.Y})
		rv.press = rvPressScroll
		return true
	}
	return false
}

// drag forwards a captured drag to the inner ScrollView (a grabbed scrollbar
// thumb or a content pan). Buttons ignore drags.
func (rv *renderView) drag(x, y int) bool {
	if rv.press == rvPressScroll {
		sb := rv.scroll.Bounds()
		rv.scroll.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - sb.X, Y: y - sb.Y})
		return true
	}
	return false
}

// release ends a captured drag, delivering the mouse-up to the ScrollView.
func (rv *renderView) release(x, y int) bool {
	was := rv.press
	if rv.press == rvPressScroll {
		sb := rv.scroll.Bounds()
		rv.scroll.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - sb.X, Y: y - sb.Y})
	}
	rv.press = rvPressNone
	return was != rvPressNone
}

// scrollWheel forwards a wheel/two-finger scroll (dx/dy in rows) to the
// ScrollView when the pointer is over the content area. A horizontal swipe (dx)
// moves the horizontal scrollbar; a vertical wheel (dy) moves the vertical one.
func (rv *renderView) scrollWheel(x, y, dx, dy int) bool {
	sb := rv.scroll.Bounds()
	if sb.Contains(x, y) {
		rv.scroll.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, X: x - sb.X, Y: y - sb.Y, Delta: dy, DeltaX: dx})
		return true
	}
	return false
}

// scrollToBaseY scrolls so the given BASE-render Y (a line band top, before
// zoom) sits about a third down the viewport.
func (rv *renderView) scrollToBaseY(baseY int) {
	zy := int(float64(baseY)*rv.zoom + 0.5)
	target := zy - rv.scroll.Bounds().H/3
	if target < 0 {
		target = 0
	}
	rv.scroll.Scroll(0, target-rv.scroll.OffsetY)
}

// baseContentYAt maps a surface point in the content area to a BASE-render Y
// (undoing the scroll offset and the zoom), for click-to-source navigation. It
// reports ok=false for a point outside the content area or over the scrollbar
// gutter.
func (rv *renderView) baseContentYAt(x, y int) (int, bool) {
	sb := rv.scroll.Bounds()
	if !sb.Contains(x, y) {
		return 0, false
	}
	if x >= sb.X+sb.W-toolkit.Scaled(rvScrollbarW) {
		return 0, false
	}
	contentY := (y - sb.Y) + rv.scroll.OffsetY
	if rv.zoom <= 0 {
		return contentY, true
	}
	return int(float64(contentY)/rv.zoom + 0.5), true
}
