// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"
)

// The render pane used to blit a rasterised bitmap of each page and lay an
// invisible text layer over it. It now lays the pages out and paints their
// paper, and the HOST draws the SVG itself.
//
// Measured in wasm against the browser it runs in: rasterising was 70-75% of
// what this app spent between a keystroke and a rendered page, and the browser
// draws the same SVG 2.6x (small document) to 5.6x (per page of a big one)
// faster. The bitmaps also pinned the preview to one resolution and held 17.6 MB
// of RGBA for three pages; the SVG is crisp at any zoom and carries its own
// selectable text, so the separate text layer is gone with the rasteriser.
//
// The overlay stays inert to the pointer (see the host's styles): a click still
// reaches the canvas and still moves the caret to the matching source line.

// PageRender is one page's SVG and where to put it. The host reads it from
// [State.PageRenders] and turns it into DOM.
type PageRender struct {
	// Page is 1-based, matching PagedView.
	Page int
	// SVG is the whole themed page.
	SVG string
	// Natural is the page's un-zoomed size in device pixels — what the SVG's
	// viewBox maps onto, and the space [State.SetLineBands] reports in.
	Natural toolkit.Rect
	// Rect is where the card is drawn and Clip the part the pane still shows,
	// both in the surface's DEVICE pixels: a host laying out in CSS pixels
	// divides by the device pixel ratio.
	Rect, Clip toolkit.Rect
}

// PageRenders returns every page currently ON SCREEN in the render pane, with
// where to put it. The host creates one element per entry and removes the rest.
//
// It is empty whenever nothing should be shown: the Log tab is in front, the
// pane has no room, or the compile produced no page. Reading it every frame is
// what keeps each page glued to its card through a scroll, a zoom, a page flip
// or a pane resize — the placement comes from [toolkit.PagedView.PageRect], the
// same layout the pane paints its paper from, so the two cannot drift.
func (s *State) PageRenders() []PageRender {
	if s.rightPane == nil || s.rightPane.activeTab() != tabRender {
		return nil
	}
	var out []PageRender
	for i, svg := range s.svgs {
		if svg == "" {
			continue
		}
		rect, clip, ok := s.renderView.PageRect(i + 1)
		if !ok || clip.W <= 0 || clip.H <= 0 {
			continue // no such page, or none of it is on screen
		}
		nat := toolkit.Rect{}
		if i < len(s.pageSizes) {
			nat = toolkit.Rect{W: s.pageSizes[i].X, H: s.pageSizes[i].Y}
		}
		out = append(out, PageRender{Page: i + 1, SVG: svg, Natural: nat, Rect: rect, Clip: clip})
	}
	return out
}

// SetLineBands records where a page's source lines were actually drawn, MEASURED
// FROM THE DOM by the host.
//
// The bands used to come from the rasteriser's per-<g> bounds. With no
// rasteriser there is nothing in Go that knows where a line ended up — the
// browser laid the text out, so the browser is what measures it. The host reads
// every <g data-l="N"> element's box, converts it to this page's natural
// pixels, and reports it here; everything downstream (click to caret, caret to
// scroll) is unchanged, because this is the space it always worked in.
//
// page is 1-based. lines, tops and bots are parallel; a short or ragged set is
// ignored rather than half-applied, so a bad measurement cannot half-break the
// linking.
func (s *State) SetLineBands(page int, lines, tops, bots []int) {
	if page < 1 || len(lines) != len(tops) || len(lines) != len(bots) {
		return
	}
	for len(s.lineMaps) < page {
		s.lineMaps = append(s.lineMaps, pageLineMap{})
	}
	byLine := make(map[int][2]int, len(lines))
	for i, ln := range lines {
		if ln <= 0 || bots[i] <= tops[i] {
			continue
		}
		if cur, ok := byLine[ln]; ok {
			byLine[ln] = [2]int{min(cur[0], tops[i]), max(cur[1], bots[i])}
			continue
		}
		byLine[ln] = [2]int{tops[i], bots[i]}
	}
	s.lineMaps[page-1] = makeLineMap(byLine)
}

// CanvasOverlays are the windows the canvas paints ON TOP of everything — the
// find-and-replace panel, the Git panel, the Collaborate panel — in the
// surface's device pixels. Empty when none is open.
//
// A host that draws page content as DOM MUST punch these out of it. The canvas
// and the overlay are siblings in the page, and a DOM element is unconditionally
// above a canvas: a window painted into the canvas cannot come forward over an
// element, however the application stacks it internally. Dragging the find modal
// onto the render pane sliced it off at the page's edge until this existed.
//
// Each overlay carries the corner radius of the window drawn there, so the host
// punches a hole that follows the window's rounded corners. The find modal is a
// rounded toolkit.Dialog; a SQUARE hole behind it cut the page out at the
// corners while the canvas left them transparent, and the void between the two
// read as black notches in the rounded corners over the preview. The Git and
// Collaborate panels are square-cornered Backdrops, so their radius is 0.
//
// ⛔ The PATH is built by [webcanvas.ClipPath] now, not here. The go-pdfkit
// workbench needs the same holes for the same reason — it is about to show its
// pages as DOM so their text can be selected, with its ribbon and panels
// painted into the canvas over them — and the only part of this that belongs to
// this application is which windows it has open. The shape of the path, the
// evenodd two-subpaths-per-hole rule and the corner arcs went to
// go-widgets/webcanvas, which exists so the DOM plumbing is written once.
func (s *State) CanvasOverlays() []webcanvas.Overlay {
	var out []webcanvas.Overlay
	add := func(r toolkit.Rect, radius int) {
		// An unlaid panel has nothing to punch out. webcanvas drops empty
		// rectangles too; this is about THIS application's state — a window
		// that is open but has not been laid out yet is not a window on screen.
		if r.W > 0 && r.H > 0 {
			out = append(out, webcanvas.Overlay{X: r.X, Y: r.Y, W: r.W, H: r.H, Radius: radius})
		}
	}
	add(s.findPanelRect(), toolkit.Scaled(toolkit.DialogRadius))
	if s.git != nil && s.git.open {
		add(s.git.panel, 0)
	}
	if s.collab != nil && s.collab.open {
		add(s.collab.panel, 0)
	}
	return out
}

