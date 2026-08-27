// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"sort"
	"strconv"
	"strings"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

// rasterScale is how many device pixels one of the engine SVG's points counts
// for. Nothing is rasterised any more — the host draws the SVG — but the factor
// stays, because it is what the render pane's page geometry, zoom percentages
// and fit-width were calibrated against.
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
// and a source line into a scroll target (the band's top). Built from the shared
// rasteriser's per-<g> [gfxsvg.Group] bounds (filtered by the go-tex-specific
// data-l attribute) and kept parallel to [compileResult.bitmaps], so page i of
// the PagedView is described by lineMaps[i].
type pageLineMap struct {
	bands []lineBand
}

// atoiSafe parses a non-negative integer, returning 0 on any error (an empty or
// non-digit string). It guards the data-l parse so a malformed marker never picks
// up a spurious source line.
func atoiSafe(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// makeLineMap converts a per-line Y band map (keyed by 1-based source line) into
// an ordered pageLineMap. Ordering is deterministic: by yTop, then by source line
// as a tie-break, so the same document always yields the same map.
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

// compileResult is the outcome of one compile: the per-page SVG the engine
// produced, each page's natural size (what a bitmap of it would have measured),
// the engine's logical page count, how many pages are drawable, and either an
// error string or nil.
type compileResult struct {
	svgs       []string           // one themed SVG per drawable page, for the host to render
	sizes      []image.Point      // each page's natural size in device pixels, parallel to svgs
	pages      int                // engine (logical) page count
	drawnPages int                // pages with a usable size
	errText    string             // "" on success; a human message on a hard compile error
	diag       engine.Diagnostics // undefined commands/environments, dropped math, alarms
}

// compileFn is the engine entry point compileLaTeX calls, kept as a package var
// so a test can substitute a stub to exercise the hard-error and
// unrasterizable-page branches the tolerant real engine does not reach.
var compileFn = engine.CompileToSVGPagesDiag

// compileLaTeX runs the pure-Go TeX engine on src and prepares each SVG page for
// the HOST to render, themed from the given theme (paper = Surface, ink =
// OnSurface). It does not rasterise: the render pane lays the pages out (paging,
// zoom, card decoration, scroll) and the host draws the content over them.
//
// That is the whole point of the change. Rasterising in wasm was 70-75% of what
// this app spent between a keystroke and a rendered page, and the browser draws
// the same SVG 2.6x to 5.6x faster — measured in wasm against the browser it
// runs in. The bitmaps also pinned the preview to one resolution and held 17.6 MB
// of RGBA for a three-page document; an SVG in the DOM is crisp at any zoom and
// carries its own selectable text.
//
// It never panics on bad input: a hard compile error yields a result whose
// errText is set and whose page slices are nil.
func compileLaTeX(src string, theme *toolkit.Theme, resolve func(string) ([]byte, bool)) compileResult {
	opt := engine.Options{Size: 11, Lenient: true, Resolve: resolve}
	pages, diag, err := compileFn([]byte(src), opt)
	if err != nil {
		return compileResult{errText: err.Error()}
	}

	var svgs []string
	var sizes []image.Point
	for _, svg := range pages {
		sz := naturalSize(svg)
		if sz.X <= 0 || sz.Y <= 0 {
			continue // a page whose size cannot be read draws nothing
		}
		svgs = append(svgs, themeSVG(svg, theme))
		sizes = append(sizes, sz)
	}
	if len(svgs) == 0 {
		// A compile that produced no drawable page (e.g. an empty document):
		// report it as a valid zero-page result rather than a blank crash.
		return compileResult{
			pages:   len(pages),
			errText: diagSummaryEmpty(diag),
			diag:    diag,
		}
	}

	return compileResult{
		svgs:       svgs,
		sizes:      sizes,
		pages:      len(pages),
		drawnPages: len(svgs),
		diag:       diag,
	}
}

// naturalSize is the page's size in the DEVICE pixels a bitmap of it would have
// had: the SVG's own point size times [rasterScale]. Keeping that factor is what
// makes the change invisible to the layout — the render pane pages, zooms and
// fits exactly as it did when the pixels were real.
func naturalSize(svg string) image.Point {
	w := ptAttr(svg, ` width="`)
	h := ptAttr(svg, ` height="`)
	return image.Point{X: int(w*rasterScale + 0.5), Y: int(h*rasterScale + 0.5)}
}

// ptAttr reads a "NNNpt" attribute off the SVG root. Zero when absent or
// unparsable, which naturalSize reports as an undrawable page.
func ptAttr(svg, attr string) float64 {
	i := strings.Index(svg, attr)
	if i < 0 {
		return 0
	}
	rest := svg[i+len(attr):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(rest[:j], "pt"), 64)
	if err != nil {
		return 0
	}
	return v
}

// themeSVG recolours a page for the app's theme, which is what the rasteriser
// used to do through its Ink and Paper options. The engine paints a page on
// white with black glyphs; on a dark scheme that would be a white sheet in a
// dark window.
//
// The two substitutions cover everything the engine emits: the full-bleed page
// rect is fill="white", and both the glyph group and the colour go-tex/math
// bakes in are fill="black". Coloured text carries its own fill and is left
// alone, as it was under the rasteriser.
func themeSVG(svg string, theme *toolkit.Theme) string {
	svg = strings.ReplaceAll(svg, `fill="white"`, `fill="`+cssColor(theme.Surface)+`"`)
	return strings.ReplaceAll(svg, `fill="black"`, `fill="`+cssColor(theme.OnSurface)+`"`)
}

// cssColor renders a toolkit colour as #rrggbb.
func cssColor(c toolkit.RGBA) string {
	const hex = "0123456789abcdef"
	return string([]byte{'#',
		hex[c.R>>4], hex[c.R&0xF],
		hex[c.G>>4], hex[c.G&0xF],
		hex[c.B>>4], hex[c.B&0xF]})
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
