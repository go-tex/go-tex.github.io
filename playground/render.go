// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"image/color"
	"strconv"

	engine "github.com/go-tex/engine"
	"github.com/go-tex/go-tex.github.io/playground/svgraster"
	"github.com/go-widgets/toolkit"
)

// rasterScale is how many device pixels the engine SVG's point is rasterized to.
// 2.0 gives crisp glyph outlines without an unreasonably large buffer.
const rasterScale = 2.0

// compileResult is the outcome of one compile+rasterize pass: the per-page
// bitmaps (natural device size, fed straight to the render pane's
// [toolkit.PagedView]), the engine's logical page count, how many pages actually
// rasterized, and either an error string or nil.
type compileResult struct {
	bitmaps    []*image.RGBA      // one natural-size RGBA per drawable page
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
