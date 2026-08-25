// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"image/color"
	"sort"
	"strconv"

	gfxsvg "github.com/go-gfx/gfx/svg"
	engine "github.com/go-tex/engine"
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
// and a source line into a scroll target (the band's top). Built from the shared
// rasteriser's per-<g> [gfxsvg.Group] bounds (filtered by the go-tex-specific
// data-l attribute) and kept parallel to [compileResult.bitmaps], so page i of
// the PagedView is described by lineMaps[i].
type pageLineMap struct {
	bands []lineBand
}

// linesFromGroups rebuilds the go-tex-specific source-line→device-Y band map from
// the shared rasteriser's per-<g> groups. The engine tags each source line's glyph
// run as <g data-l="N">; the shared [gfxsvg] package reports every <g> with the
// device-pixel bounds of everything it drew but assigns no meaning to any
// attribute, so the data-l interpretation lives HERE (it is go-tex-specific). For
// each group carrying a positive data-l, its bounds' [Min.Y, Max.Y) contributes to
// that line's band; a line appearing in several groups is unioned on Y. Empty
// groups (which drew nothing) and non-positive / unparsable data-l are skipped.
func linesFromGroups(groups []gfxsvg.Group) map[int][2]int {
	lines := map[int][2]int{}
	for _, g := range groups {
		if g.Bounds.Empty() {
			continue
		}
		ln := atoiSafe(g.Attrs["data-l"])
		if ln <= 0 {
			continue
		}
		b := [2]int{g.Bounds.Min.Y, g.Bounds.Max.Y}
		if cur, ok := lines[ln]; ok {
			if cur[0] < b[0] {
				b[0] = cur[0]
			}
			if cur[1] > b[1] {
				b[1] = cur[1]
			}
		}
		lines[ln] = b
	}
	return lines
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

// compileResult is the outcome of one compile+rasterize pass: the per-page
// bitmaps (natural device size, fed straight to the render pane's
// [toolkit.PagedView]), the per-page source-line maps (parallel to bitmaps, for
// source↔render linking), the engine's logical page count, how many pages
// actually rasterized, and either an error string or nil.
type compileResult struct {
	bitmaps    []*image.RGBA      // one natural-size RGBA per drawable page
	lineMaps   []pageLineMap      // source-line Y-bands per drawn page, parallel to bitmaps
	textLayers []string           // per drawn page, the searchable <text> lifted out of the SVG
	pages      int                // engine (logical) page count
	drawnPages int                // pages actually rasterized
	errText    string             // "" on success; a human message on a hard compile error
	diag       engine.Diagnostics // undefined commands/environments, dropped math, alarms
}

// toColor converts a toolkit/painter RGBA to an image/color.RGBA (both are
// straight-alpha 8-bit), the form gfxsvg.Options consumes.
func toColor(c toolkit.RGBA) color.RGBA { return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A} }

// compileFn is the engine entry point compileLaTeX calls, kept as a package var
// so a test can substitute a stub to exercise the hard-error and
// unrasterizable-page branches the tolerant real engine does not reach.
var compileFn = engine.CompileToSVGPagesDiag

// compileLaTeX runs the pure-Go TeX engine on src and rasterizes every SVG page
// with the shared [gfxsvg] rasteriser themed from the given theme (paper =
// Surface, ink = OnSurface) into a natural-size RGBA bitmap. The bitmaps are
// handed to the render pane's [toolkit.PagedView], which owns all paging, zoom,
// card decoration and scroll. It never panics on bad input: a hard compile error
// yields a result whose errText is set and whose bitmap slice is nil.
func compileLaTeX(src string, theme *toolkit.Theme) compileResult {
	opt := engine.Options{Size: 11, Lenient: true}
	pages, diag, err := compileFn([]byte(src), opt)
	if err != nil {
		return compileResult{errText: err.Error()}
	}

	ropt := gfxsvg.Options{
		Scale: rasterScale,
		Ink:   toColor(theme.OnSurface),
		Paper: toColor(theme.Surface),
	}

	var bitmaps []*image.RGBA
	var lineMaps []pageLineMap
	var textLayers []string
	for _, svg := range pages {
		res, perr := gfxsvg.Rasterize(svg, ropt)
		if perr != nil || res == nil || res.Image == nil || res.Image.W <= 0 || res.Image.H <= 0 {
			continue
		}
		// res.Image.Pix is a straight-alpha RGBA buffer of exactly W*H*4 bytes, the
		// same layout the render pane feeds SetPages, so it wraps directly as an
		// *image.RGBA with no copy.
		bitmaps = append(bitmaps, &image.RGBA{
			Pix:    res.Image.Pix,
			Stride: res.Image.W * 4,
			Rect:   image.Rect(0, 0, res.Image.W, res.Image.H),
		})
		// The <g data-l="N"> Y-bands, rebuilt from the shared rasteriser's group
		// bounds and kept parallel to the bitmap so the click↔caret linking can map
		// this page's natural pixels to source lines and back.
		lineMaps = append(lineMaps, makeLineMap(linesFromGroups(res.Groups)))
		// The same page as DOM-placeable text: the bitmap says how the page
		// looks, this says what it says.
		textLayers = append(textLayers, textOverlaySVG(svg))
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
		textLayers: textLayers,
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
