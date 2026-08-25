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

// The render pane blits rasterised pages to the canvas. A bitmap is not text,
// so the preview could not be searched, read aloud or copied. This places the
// page's characters — which the engine now carries in an invisible <text> layer
// — over the card as real DOM, in the page's own coordinate system, so the
// browser knows what the pixels say.
//
// Nothing about the canvas changes. The overlay is inert to the pointer (the
// stylesheet says pointer-events:none), so a click still reaches the canvas and
// still moves the caret to the matching source line.

// textLayerHost owns the overlay elements: one per page currently on screen.
//
// It DIFFS rather than rebuilds. Rewriting the container every frame would
// destroy the browser's find highlight and any selection the reader is holding,
// which are the very things the overlay exists to provide — so an element's
// markup is written only when its page's text changed, and its geometry only
// when the pane moved it.
type textLayerHost struct {
	doc  js.Value
	root js.Value
	live map[int]js.Value
	svg  map[int]string
	geom map[int]string
}

// newTextLayerHost creates the overlay container as a sibling of the canvas, so
// it shares the canvas's box and moves and resizes with it. The container's own
// geometry is set HERE rather than in the host page's stylesheet: the overlay
// only works if it is absolutely positioned over the canvas, inert to the
// pointer and clipped to the frame, and a page that forgot one of those would
// fail in a way that looks like a bug in the toolkit. The host page's only
// obligation is that the canvas's parent is positioned.
//
// It returns nil when there is no canvas to hang it on, and every method
// tolerates that.
func newTextLayerHost(doc js.Value) *textLayerHost {
	canvas := doc.Call("getElementById", canvasID)
	if canvas.IsUndefined() || canvas.IsNull() {
		return nil
	}
	parent := canvas.Get("parentElement")
	if parent.IsUndefined() || parent.IsNull() {
		return nil
	}
	root := doc.Call("createElement", "div")
	root.Set("id", "gotex-textlayer")
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
	return &textLayerHost{
		doc: doc, root: root,
		live: map[int]js.Value{},
		svg:  map[int]string{},
		geom: map[int]string{},
	}
}

// sync brings the DOM in line with what the render pane currently shows. dpr is
// the device pixel ratio: the overlays arrive in the surface's device pixels and
// the page lays out in CSS pixels.
func (h *textLayerHost) sync(overlays []playground.TextOverlay, dpr float64) {
	if h == nil {
		return
	}
	if dpr <= 0 {
		dpr = 1
	}
	seen := map[int]bool{}
	for _, o := range overlays {
		seen[o.Page] = true
		el, ok := h.live[o.Page]
		if !ok {
			el = h.doc.Call("createElement", "div")
			el.Set("className", "pg-textpage")
			el.Get("style").Set("position", "absolute")
			el.Get("style").Set("overflow", "hidden")
			h.root.Call("appendChild", el)
			h.live[o.Page] = el
		}
		// The clip is the window the pane still shows; the card starts wherever
		// the scroll put it, which is what makes a half-scrolled page look
		// scrolled rather than merely cropped.
		g := geomKey(o, dpr)
		if h.geom[o.Page] != g {
			setBox(el.Get("style"), o.Clip, dpr)
			h.geom[o.Page] = g
		}
		if h.svg[o.Page] != o.SVG {
			el.Set("innerHTML", o.SVG)
			h.svg[o.Page] = o.SVG
		}
		if inner := el.Get("firstElementChild"); !inner.IsNull() && !inner.IsUndefined() {
			st := inner.Get("style")
			st.Set("position", "absolute")
			st.Set("left", cssPx(o.Rect.X-o.Clip.X, dpr))
			st.Set("top", cssPx(o.Rect.Y-o.Clip.Y, dpr))
			st.Set("width", cssPx(o.Rect.W, dpr))
			st.Set("height", cssPx(o.Rect.H, dpr))
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

// geomKey is the placement a page is currently drawn at, as a comparable
// string, so an unmoved page is not restyled every frame.
func geomKey(o playground.TextOverlay, dpr float64) string {
	return strconv.Itoa(o.Rect.X) + "," + strconv.Itoa(o.Rect.Y) + "," +
		strconv.Itoa(o.Rect.W) + "," + strconv.Itoa(o.Rect.H) + "|" +
		strconv.Itoa(o.Clip.X) + "," + strconv.Itoa(o.Clip.Y) + "," +
		strconv.Itoa(o.Clip.W) + "," + strconv.Itoa(o.Clip.H) + "@" +
		strconv.FormatFloat(dpr, 'f', 3, 64)
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
