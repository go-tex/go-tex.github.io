// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "github.com/go-widgets/toolkit"

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
