//go:build js && wasm

// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"strconv"
	"syscall/js"

	"github.com/go-tex/go-tex.github.io/playground"
	"github.com/go-widgets/toolkit"
)

// The render pane lays the typeset pages out and paints their paper; this puts
// the actual page there, as an <svg> the browser renders.
//
// It used to be a bitmap rasterised in wasm with an invisible text layer on top.
// Measured in wasm against the browser it runs in, rasterising was 70-75% of the
// time between a keystroke and a rendered page, and the browser draws the same
// SVG 2.6x to 5.6x faster — while being crisp at any zoom instead of pinned to
// one resolution, and carrying its own selectable text.
//
// Nothing about the canvas changes. The overlay is inert to the pointer, so a
// click still reaches the canvas and still moves the caret to the matching
// source line.

// domRenderHost owns the page elements: one per page currently on screen.
//
// It DIFFS rather than rebuilds. Rewriting the container every frame would
// destroy the browser's find highlight and any selection the reader is holding,
// which are among the reasons the page is DOM in the first place — so an
// element's markup is written only when its page's SVG changed, and its geometry
// only when the pane moved it.
type domRenderHost struct {
	doc  js.Value
	root js.Value
	live map[int]js.Value
	svg  map[int]string
	geom map[int]string
	// clip is the clip-path currently applied, so an unchanged one is not
	// rewritten every frame.
	clip string
}

// newDOMRenderHost creates the page container as a sibling of the canvas, so it
// shares the canvas's box and moves and resizes with it. The container's own
// geometry is set HERE rather than in the host page's stylesheet: the overlay
// only works if it is absolutely positioned over the canvas, inert to the
// pointer and clipped to the frame, and a page that forgot one of those would
// fail in a way that looks like a bug in the toolkit. The host page's only
// obligation is that the canvas's parent is positioned.
//
// It returns nil when there is no canvas to hang it on, and every method
// tolerates that.
func newDOMRenderHost(doc js.Value) *domRenderHost {
	canvas := doc.Call("getElementById", canvasID)
	if canvas.IsUndefined() || canvas.IsNull() {
		return nil
	}
	parent := canvas.Get("parentElement")
	if parent.IsUndefined() || parent.IsNull() {
		return nil
	}
	root := doc.Call("createElement", "div")
	root.Set("id", "gotex-pages")
	st := root.Get("style")
	st.Set("position", "absolute")
	st.Set("left", "0")
	st.Set("top", "0")
	st.Set("right", "0")
	st.Set("bottom", "0")
	st.Set("overflow", "hidden")
	// A click must still reach the canvas, where it moves the caret to the
	// matching source line. Find, the accessibility tree and a programmatic
	// selection read the text without the pointer; a drag does not select it.
	// That is the deliberate trade: the linking is an existing feature.
	st.Set("pointerEvents", "none")
	parent.Call("appendChild", root)
	return &domRenderHost{
		doc: doc, root: root,
		live: map[int]js.Value{},
		svg:  map[int]string{},
		geom: map[int]string{},
	}
}

// sync brings the DOM in line with what the render pane currently shows. dpr is
// the device pixel ratio: the placements arrive in the surface's device pixels
// and the page lays out in CSS pixels. measured is called with each page's
// freshly measured source-line bands whenever a page's content changed.
func (h *domRenderHost) sync(renders []playground.PageRender, dpr float64, measured func(page int, lines, tops, bots []int), overlays []playground.CanvasOverlay) {
	if h == nil {
		return
	}
	h.punchOut(overlays, dpr)
	if dpr <= 0 {
		dpr = 1
	}
	seen := map[int]bool{}
	for _, r := range renders {
		seen[r.Page] = true
		el, ok := h.live[r.Page]
		if !ok {
			el = h.doc.Call("createElement", "div")
			el.Set("className", "pg-page")
			el.Get("style").Set("position", "absolute")
			el.Get("style").Set("overflow", "hidden")
			h.root.Call("appendChild", el)
			h.live[r.Page] = el
		}
		if g := geomKey(r, dpr); h.geom[r.Page] != g {
			setBox(el.Get("style"), r.Clip, dpr)
			h.geom[r.Page] = g
		}
		fresh := h.svg[r.Page] != r.SVG
		if fresh {
			el.Set("innerHTML", r.SVG)
			h.svg[r.Page] = r.SVG
		}
		// The clip is the window the pane still shows; the card starts wherever
		// the scroll put it, which is what makes a half-scrolled page look
		// scrolled rather than merely cropped.
		inner := el.Get("firstElementChild")
		if inner.IsNull() || inner.IsUndefined() {
			continue
		}
		st := inner.Get("style")
		st.Set("position", "absolute")
		st.Set("left", cssPx(r.Rect.X-r.Clip.X, dpr))
		st.Set("top", cssPx(r.Rect.Y-r.Clip.Y, dpr))
		st.Set("width", cssPx(r.Rect.W, dpr))
		st.Set("height", cssPx(r.Rect.H, dpr))
		if fresh && measured != nil {
			// The browser has just laid the page out, so it is the one that knows
			// where each source line ended up. Measure once per content change,
			// not per frame: a scroll or a zoom moves the card, not the lines
			// within it, and the bands are in the page's own natural pixels.
			if lines, tops, bots := measureLineBands(inner, r.Natural); len(lines) > 0 {
				measured(r.Page, lines, tops, bots)
			}
		}
	}
	for page, el := range h.live {
		if seen[page] {
			continue
		}
		el.Call("remove")
		delete(h.live, page)
		delete(h.svg, page)
		delete(h.geom, page)
	}
}

// punchOut cuts holes in the page container where the canvas paints a window.
//
// A DOM element is unconditionally above a <canvas> sibling, so a dialog painted
// into the canvas cannot come forward over a rendered page: dragging the find
// modal onto the render pane sliced it off at the page's edge. Hiding the pages
// while a window is open would answer that too, and would blank the pane; this
// keeps every page visible and lets the window show through.
//
// The clip is TWO SUBPATHS, not one polygon with the holes appended. A single
// polygon has to travel from the outer ring to each hole and back, and that
// journey is drawn: it leaves a wedge cut out of the page. Measured — the
// polygon(evenodd, …) form notches the left edge, the path(evenodd, "M…Z M…Z")
// form does not.
func (h *domRenderHost) punchOut(overlays []playground.CanvasOverlay, dpr float64) {
	st := h.root.Get("style")
	clip := playground.CanvasClipPath(overlays, dpr)
	if clip == "" {
		if h.clip != "" {
			st.Set("clipPath", "none")
			h.clip = ""
		}
		return
	}
	if clip != h.clip {
		st.Set("clipPath", clip)
		h.clip = clip
	}
}

// measureLineBands reads every <g data-l="N"> element's box out of a rendered
// page and converts it to the page's NATURAL pixels — the space the source↔
// render linking has always worked in, and the space the rasteriser's group
// bounds used to report.
func measureLineBands(svgEl js.Value, natural toolkit.Rect) (lines, tops, bots []int) {
	box := svgEl.Call("getBoundingClientRect")
	h := box.Get("height").Float()
	if h <= 0 || natural.H <= 0 {
		return nil, nil, nil
	}
	// CSS pixels on screen -> the page's own natural pixels.
	scale := float64(natural.H) / h
	originY := box.Get("top").Float()

	groups := svgEl.Call("querySelectorAll", "[data-l]")
	n := groups.Get("length").Int()
	for i := 0; i < n; i++ {
		g := groups.Index(i)
		ln, err := strconv.Atoi(g.Call("getAttribute", "data-l").String())
		if err != nil || ln <= 0 {
			continue
		}
		r := g.Call("getBoundingClientRect")
		top := int((r.Get("top").Float() - originY) * scale)
		bot := int((r.Get("bottom").Float() - originY) * scale)
		if bot <= top {
			continue // drew nothing measurable
		}
		lines = append(lines, ln)
		tops = append(tops, top)
		bots = append(bots, bot)
	}
	return lines, tops, bots
}

// geomKey is the placement a page is currently drawn at, as a comparable
// string, so an unmoved page is not restyled every frame.
func geomKey(r playground.PageRender, dpr float64) string {
	return rectKey(r.Rect) + "|" + rectKey(r.Clip) + "@" + strconv.FormatFloat(dpr, 'f', 3, 64)
}

func rectKey(r toolkit.Rect) string {
	return strconv.Itoa(r.X) + "," + strconv.Itoa(r.Y) + "," + strconv.Itoa(r.W) + "," + strconv.Itoa(r.H)
}

// setBox positions an element over a device-pixel rectangle of the surface.
func setBox(style js.Value, r toolkit.Rect, dpr float64) {
	style.Set("left", cssPx(r.X, dpr))
	style.Set("top", cssPx(r.Y, dpr))
	style.Set("width", cssPx(r.W, dpr))
	style.Set("height", cssPx(r.H, dpr))
}

// cssPx converts a device-pixel length to the CSS pixels the page lays out in.
func cssPx(v int, dpr float64) string {
	return strconv.FormatFloat(float64(v)/dpr, 'f', 2, 64) + "px"
}
