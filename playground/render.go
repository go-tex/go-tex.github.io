// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"image/color"
	"sort"
	"strconv"

	engine "github.com/go-tex/engine"
	"github.com/go-tex/go-tex.github.io/playground/svgraster"
	"github.com/go-widgets/toolkit"
)

// rasterScale is how many device pixels the engine SVG's point is rasterized to.
// 2.0 gives crisp glyph outlines without an unreasonably large buffer.
const rasterScale = 2.0

// lineBand is one source line's natural-pixel Y extent on a single rendered page.
// [yTop, yBot) are page-natural (un-zoomed bitmap) pixels — the exact coordinate
// space [toolkit.PagedView.PageAt] returns and [toolkit.PagedView.ScrollToPage]
// consumes, so a band maps to/from the widget with no rescaling.
type lineBand struct {
	line       int // 1-based source line
	yTop, yBot int // page-natural (un-zoomed bitmap) pixels
}

// pageLineMap is the source→render linking data for ONE rendered page: the list
// of source-line Y-bands present on it, ordered by yTop. The playground searches
// it to turn a render-pane click into a source line (Y-band containing the click)
// and a source line into a scroll target (the band's top). Built from
// [svgraster.Page.Lines] and kept parallel to [compileResult.bitmaps], so page i
// of the PagedView is described by lineMaps[i].
type pageLineMap struct {
	bands []lineBand
}

// makeLineMap converts svgraster's per-line Y bands (a map keyed by 1-based source
// line) into an ordered pageLineMap. Ordering is deterministic: by yTop, then by
// source line as a tie-break, so the same document always yields the same map.
func makeLineMap(lines map[int][2]int) pageLineMap {
	bands := make([]lineBand, 0, len(lines))
	for ln, band := range lines {
		bands = append(bands, lineBand{line: ln, yTop: band[0], yBot: band[1]})
	}
	sort.Slice(bands, func(i, j int) bool {
		if bands[i].yTop != bands[j].yTop {
			return bands[i].yTop < bands[j].yTop
		}
		return bands[i].line < bands[j].line
	})
	return pageLineMap{bands: bands}
}

// compileResult is the outcome of one compile+rasterize pass: the per-page
// bitmaps (natural device size, fed straight to the render pane's
// [toolkit.PagedView]), the per-page source-line maps (parallel to bitmaps, for
// source↔render linking), the engine's logical page count, how many pages
// actually rasterized, and either an error string or nil.
type compileResult struct {
	bitmaps    []*image.RGBA      // one natural-size RGBA per drawable page
	lineMaps   []pageLineMap      // source-line Y-bands per drawn page, parallel to bitmaps
	pages      int                // engine (logical) page count
	drawnPages int                // pages actually rasterized
	errText    string             // "" on success; a human message on a hard compile error
	diag       engine.Diagnostics // undefined commands/environments, dropped math, alarms
}

// toColor converts a toolkit/painter RGBA to an image/color.RGBA (both are
// straight-alpha 8-bit), the form svgraster.Options consumes.
func toColor(c toolkit.RGBA) color.RGBA { return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A} }

// compileFn is the engine entry point compileLaTeX calls, kept as a package var
// so a test can substitute a stub to exercise the hard-error and
// unrasterizable-page branches the tolerant real engine does not reach.
var compileFn = engine.CompileToSVGPagesDiag

// compileLaTeX runs the pure-Go TeX engine on src and rasterizes every SVG page
// with svgraster themed from the given theme (paper = Surface, ink = OnSurface)
// into a natural-size RGBA bitmap. The bitmaps are handed to the render pane's
// [toolkit.PagedView], which owns all paging, zoom, card decoration and scroll.
// It never panics on bad input: a hard compile error yields a result whose
// errText is set and whose bitmap slice is nil.
func compileLaTeX(src string, theme *toolkit.Theme) compileResult {
	opt := engine.Options{Size: 11, Lenient: true}
	pages, diag, err := compileFn([]byte(src), opt)
	if err != nil {
		return compileResult{errText: err.Error()}
	}

	ropt := svgraster.Options{
		Scale: rasterScale,
		Ink:   toColor(theme.OnSurface),
		Paper: toColor(theme.Surface),
	}

	var bitmaps []*image.RGBA
	var lineMaps []pageLineMap
	for _, svg := range pages {
		pg, perr := svgraster.Rasterize(svg, ropt)
		if perr != nil || pg == nil || pg.W <= 0 || pg.H <= 0 {
			continue
		}
		// pg.Pixels is a straight-alpha RGBA buffer of exactly W*H*4 bytes, so it
		// wraps directly as an *image.RGBA with no copy.
		bitmaps = append(bitmaps, &image.RGBA{
			Pix:    pg.Pixels,
			Stride: pg.W * 4,
			Rect:   image.Rect(0, 0, pg.W, pg.H),
		})
		// The <g data-l="N"> Y-bands svgraster recorded, kept parallel to the bitmap
		// so the click↔caret linking can map this page's natural pixels to source
		// lines and back.
		lineMaps = append(lineMaps, makeLineMap(pg.Lines))
	}
	if len(bitmaps) == 0 {
		// A compile that produced no drawable page (e.g. an empty document):
		// report it as a valid zero-page result rather than a blank crash.
		return compileResult{
			pages:   len(pages),
			errText: diagSummaryEmpty(diag),
			diag:    diag,
		}
	}

	return compileResult{
		bitmaps:    bitmaps,
		lineMaps:   lineMaps,
		pages:      len(pages),
		drawnPages: len(bitmaps),
		diag:       diag,
	}
}

// diagSummaryEmpty returns an explanatory message for a compile that yielded no
// page, distinguishing a genuinely empty document from one whose whole body was
// swallowed (a runaway or unbalanced group the diagnostics flagged).
func diagSummaryEmpty(d engine.Diagnostics) string {
	if d.Runaway {
		return "no pages: a runaway guard tripped (a loop or exponential scan was aborted)"
	}
	if d.OpenGroups > 0 {
		return "no pages: " + strconv.Itoa(d.OpenGroups) + " group(s) left open at end of document"
	}
	return ""
}

// fillRGBA paints the whole RGBA buffer with c (opaque). It is the app's
// full-frame background clear (State.Draw).
func fillRGBA(buf []byte, c toolkit.RGBA) {
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = c.R, c.G, c.B, c.A
	}
}
