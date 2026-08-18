// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image/color"
	"strconv"

	engine "github.com/go-tex/engine"
	"github.com/go-tex/go-tex.github.io/playground/svgraster"
	"github.com/go-widgets/toolkit"
)

// pageGap is the vertical gap (in device pixels) between stacked pages in the
// render pane's content image.
const pageGap = 18

// rasterScale is how many device pixels the engine SVG's point is rasterized to.
// 2.0 gives crisp glyph outlines without an unreasonably large buffer.
const rasterScale = 2.0

// pageShadow is the device-pixel offset of each page's drop shadow (down + right),
// giving the stacked sheets a subtle lift so consecutive pages read as separate
// cards in the continuous scroll.
const pageShadow = 4

// compileResult is the outcome of one compile+rasterize pass: the composed
// content image for the render pane, the per-source-line vertical bands (for
// click navigation), a page count and either an error string or nil.
type compileResult struct {
	pixels     []byte             // RGBA content image, contentW*contentH*4
	w, h       int                // content image dimensions in device pixels
	lineBands  map[int][2]int     // 1-based source line -> [topY, bottomY) in content px
	pages      int                // engine (logical) page count
	drawnPages int                // pages actually rasterized + stacked
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

// compileLaTeX runs the pure-Go TeX engine on src, rasterizes every SVG page
// with svgraster themed from the given theme (paper = Surface, ink = OnSurface),
// and stacks the pages into a single tall content image with the pane's
// Background showing between them. It never panics on bad input: a hard compile
// error yields a result whose errText is set and whose image is empty.
func compileLaTeX(src string, theme *toolkit.Theme) compileResult {
	opt := engine.Options{Size: 11, Lenient: true}
	pages, diag, err := compileFn([]byte(src), opt)
	if err != nil {
		return compileResult{errText: err.Error(), lineBands: map[int][2]int{}}
	}

	ropt := svgraster.Options{
		Scale: rasterScale,
		Ink:   toColor(theme.OnSurface),
		Paper: toColor(theme.Surface),
	}

	var raster []*svgraster.Page
	maxW, totalH := 0, 0
	for _, svg := range pages {
		pg, perr := svgraster.Rasterize(svg, ropt)
		if perr != nil || pg == nil {
			continue
		}
		raster = append(raster, pg)
		if pg.W > maxW {
			maxW = pg.W
		}
		totalH += pg.H + pageGap
	}
	if len(raster) == 0 || maxW == 0 || totalH == 0 {
		// A compile that produced no drawable page (e.g. an empty document):
		// report it as a valid zero-page result rather than a blank crash.
		return compileResult{
			lineBands: map[int][2]int{},
			pages:     len(pages),
			errText:   diagSummaryEmpty(diag),
			diag:      diag,
		}
	}

	content := make([]byte, maxW*totalH*4)
	fillRGBA(content, theme.Background)

	// Card decoration: a subtle drop shadow behind each sheet and a 1px border
	// around it, so the stacked pages read as separate cards (PDF-viewer style).
	shadowCol := mixRGBA(theme.Background, theme.OnSurface, 0.18)
	borderCol := theme.Border

	bands := map[int][2]int{}
	y := 0
	for _, pg := range raster {
		ox := (maxW - pg.W) / 2
		// Shadow first (offset down-right); the page then covers all but the
		// offset L-shaped band, leaving a soft drop shadow.
		fillRectBuf(content, maxW, totalH, toolkit.Rect{X: ox + pageShadow, Y: y + pageShadow, W: pg.W, H: pg.H}, shadowCol)
		blit(content, maxW, totalH, pg.Pixels, pg.W, pg.H, ox, y)
		strokeRectBuf(content, maxW, totalH, toolkit.Rect{X: ox, Y: y, W: pg.W, H: pg.H}, borderCol, 1)
		for line, b := range pg.Lines {
			if _, seen := bands[line]; !seen {
				bands[line] = [2]int{y + b[0], y + b[1]}
			}
		}
		y += pg.H + pageGap
	}

	return compileResult{
		pixels:     content,
		w:          maxW,
		h:          totalH,
		lineBands:  bands,
		pages:      len(pages),
		drawnPages: len(raster),
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

// fillRGBA paints the whole RGBA buffer with c (opaque).
func fillRGBA(buf []byte, c toolkit.RGBA) {
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = c.R, c.G, c.B, c.A
	}
}

// fillRectBuf fills the (clipped) rectangle r of the dst RGBA buffer with c.
func fillRectBuf(dst []byte, dstW, dstH int, r toolkit.Rect, c toolkit.RGBA) {
	for yy := r.Y; yy < r.Y+r.H; yy++ {
		if yy < 0 || yy >= dstH {
			continue
		}
		for xx := r.X; xx < r.X+r.W; xx++ {
			if xx < 0 || xx >= dstW {
				continue
			}
			i := (yy*dstW + xx) * 4
			dst[i], dst[i+1], dst[i+2], dst[i+3] = c.R, c.G, c.B, c.A
		}
	}
}

// strokeRectBuf draws a t-thick border around r into the dst RGBA buffer (four
// filled bands, each clipped by fillRectBuf).
func strokeRectBuf(dst []byte, dstW, dstH int, r toolkit.Rect, c toolkit.RGBA, t int) {
	if t < 1 {
		t = 1
	}
	fillRectBuf(dst, dstW, dstH, toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: t}, c)           // top
	fillRectBuf(dst, dstW, dstH, toolkit.Rect{X: r.X, Y: r.Y + r.H - t, W: r.W, H: t}, c) // bottom
	fillRectBuf(dst, dstW, dstH, toolkit.Rect{X: r.X, Y: r.Y, W: t, H: r.H}, c)           // left
	fillRectBuf(dst, dstW, dstH, toolkit.Rect{X: r.X + r.W - t, Y: r.Y, W: t, H: r.H}, c) // right
}

// blit copies a srcW*srcH RGBA image into dst (dstW*dstH) at (ox, oy),
// row by row, clipping to dst. src is treated as opaque (straight copy); the
// engine pages already carry their own paper background.
func blit(dst []byte, dstW, dstH int, src []byte, srcW, srcH, ox, oy int) {
	for sy := 0; sy < srcH; sy++ {
		dy := oy + sy
		if dy < 0 || dy >= dstH {
			continue
		}
		for sx := 0; sx < srcW; sx++ {
			dx := ox + sx
			if dx < 0 || dx >= dstW {
				continue
			}
			si := (sy*srcW + sx) * 4
			di := (dy*dstW + dx) * 4
			dst[di], dst[di+1], dst[di+2], dst[di+3] = src[si], src[si+1], src[si+2], src[si+3]
		}
	}
}
