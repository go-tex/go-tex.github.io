// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// This file holds the two chrome bands that used to live as HTML around the
// canvas and now belong to the toolkit scene itself, so the wasm app fills the
// whole viewport below the host page's blue header:
//
//   - topZone: a compact status line ABOVE the toolbar — the moved ".pg-bar"
//     (a ready dot + "engine ready …"). The build SHA/time stamp stays in the
//     status bar (see SetBuildInfo), so it is deliberately NOT duplicated here.
//   - bottomZone: the moved ".pg-note" prose + "<footer>" line at the very
//     bottom, with the three links (Back to go-tex, go-tex/engine, go-tex/brand)
//     made clickable — a click in a link's rect drives the host navigate hook.
//
// Both are painted with toolkit widgets only (Backdrop grounds, a tiny Backdrop
// for the dot, Label for every text run — no hand-drawn rectangles or DrawText),
// so they carry theming/HiDPI like the rest of the scene and pass bricolint.

// readyDotColor is the green "engine ready" indicator, the toolkit equivalent of
// the host page's `.pg-bar .dot.ok` (which was the brand gradient start). It is a
// fixed hue rather than a theme colour so "ready" reads the same in light + dark.
var readyDotColor = toolkit.RGBA{R: 0x43, G: 0xA0, B: 0x47, A: 0xFF}

// topZoneStatus is the compact status line the topZone shows once the app paints
// (the wasm is, by definition, ready the moment it draws a frame). The separator
// is " - ", NOT a middot: the toolkit's ASCII 5x7 bitmap fallback has no "·"
// glyph, and the OpenType body face is only an opt-in on top of it, so an ASCII
// separator reads correctly under either face.
const topZoneStatus = "engine ready - pure-Go go-widgets canvas"

// loadingDotColor is the amber shown while a background asset is still
// downloading, so "ready" and "still fetching" are told apart at a glance
// without reading the line.
var loadingDotColor = toolkit.RGBA{R: 0xE8, G: 0xA3, B: 0x3D, A: 0xFF}

// statusLine is what the band says: the asset still downloading, if any, else
// that the engine is ready.
func (z *topZone) statusLine() (string, toolkit.RGBA) {
	if a := z.s.assetLoading; a != "" {
		return "loading " + a + " …", loadingDotColor
	}
	return topZoneStatus, readyDotColor
}

// engineURL / brandURL are the two external repositories the bottomZone links to;
// the third link (Back to go-tex) targets the deployment's own site root, which is
// dynamic (State.siteRoot), so it is read at layout time rather than a constant.
const (
	engineURL = "https://github.com/go-tex/engine"
	brandURL  = "https://github.com/go-tex/brand"
)

// defaultSiteRoot is the "Back to go-tex" target when the host has not supplied a
// live one. The js layer overrides it at startup with location.origin + "/" via
// [State.SetSiteRoot]; a native build keeps this canonical default.
const defaultSiteRoot = "https://go-tex.github.io/"

// topZone is the status band above the toolbar. It is passive (no interactive
// controls), so it only lays out and draws.
type topZone struct {
	s      *State
	bg     *toolkit.Backdrop // the band's ground
	rule   *toolkit.Backdrop // the bottom hairline separating it from the toolbar
	dot    *toolkit.Backdrop // the small "ready" indicator
	lbl    *toolkit.Label
	bounds toolkit.Rect
}

// newTopZone builds the band's persistent widgets.
func newTopZone(s *State) *topZone {
	return &topZone{
		s:    s,
		bg:   &toolkit.Backdrop{},
		rule: &toolkit.Backdrop{},
		dot:  &toolkit.Backdrop{},
		lbl:  toolkit.NewLabel(topZoneStatus),
	}
}

// height is the band's fixed device height (one text line plus padding), scaled
// to the active HiDPI metric scale.
func (z *topZone) height() int { return toolkit.Scaled(24) }

// setBounds records the band's placement (computed by State.layout).
func (z *topZone) setBounds(r toolkit.Rect) { z.bounds = r }

// draw paints the ground, the bottom hairline, the ready dot and the status
// label, all as toolkit widgets.
func (z *topZone) draw(p painter.Painter, theme *toolkit.Theme) {
	r := z.bounds
	if r.W <= 0 || r.H <= 0 {
		return
	}
	z.bg.Fill = theme.Surface
	z.bg.SetBounds(r)
	z.bg.Draw(p, theme)

	z.rule.Fill = theme.Border
	z.rule.SetBounds(toolkit.Rect{X: r.X, Y: r.Y + r.H - toolkit.Scaled(1), W: r.W, H: toolkit.Scaled(1)})
	z.rule.Draw(p, theme)

	pad := toolkit.Scaled(10)
	d := toolkit.Scaled(8)
	line, dot := z.statusLine()
	z.lbl.Text().Set(line)
	z.dot.Fill = dot
	z.dot.SetBounds(toolkit.Rect{X: r.X + pad, Y: r.Y + (r.H-d)/2, W: d, H: d})
	z.dot.Draw(p, theme)

	gap := toolkit.Scaled(8)
	lx := r.X + pad + d + gap
	z.lbl.SetBounds(toolkit.Rect{X: lx, Y: r.Y, W: r.X + r.W - pad - lx, H: r.H})
	z.lbl.Ink = theme.OnSurface
	z.lbl.VAlign = toolkit.VMiddle
	z.lbl.Draw(p, theme)
}

// zoneToken is one whitespace-delimited run of the bottomZone prose. A non-empty
// url makes it a clickable link (painted in the accent ink); the text may itself
// contain spaces for a multi-word link ("<- Back to go-tex"), in which case it is
// treated as one atomic, unwrapped token.
type zoneToken struct {
	text string
	url  string
}

// placedToken is a token positioned during layout: its device-pixel rect and the
// token it carries.
type placedToken struct {
	rect toolkit.Rect
	tok  zoneToken
}

// zoneLink is a laid-out link — the rect a click is tested against, the url to
// navigate to and the visible caption (so the reusable toolkit.Link that paints
// it can be positioned and labelled at layout time, before the next paint).
type zoneLink struct {
	rect toolkit.Rect
	url  string
	text string
}

// bottomZone is the footer band at the very bottom of the canvas: the moved
// ".pg-note" description and "<footer>" line, wrapped across toolkit Labels
// (which do not wrap themselves) with the three links clickable.
type bottomZone struct {
	s      *State
	bg     *toolkit.Backdrop
	rule   *toolkit.Backdrop // the top hairline separating it from the status bar
	bounds toolkit.Rect

	// placedRel are the tokens positioned by measure() with their final X but a Y
	// relative to the band's top; place() offsets them to absolute coordinates in
	// placed and derives the clickable links.
	placedRel []placedToken
	placed    []placedToken
	links     []zoneLink
	contentH  int

	// labelPool is one reused Label per drawn token (grown on demand), so a token
	// keeps a stable widget across frames rather than allocating per paint.
	labelPool []*toolkit.Label

	// linkWidgets is one reused toolkit.Link per laid-out link (parallel to
	// links), grown + positioned in place(). Each carries its own hovered state
	// so it draws the accent underline while the pointer is over it; handleMove
	// forwards pointer-move to them and draw() paints them.
	linkWidgets []*toolkit.Link
}

// newBottomZone builds the band's persistent ground widgets.
func newBottomZone(s *State) *bottomZone {
	return &bottomZone{s: s, bg: &toolkit.Backdrop{}, rule: &toolkit.Backdrop{}}
}

// plainTokens splits s into non-link word tokens.
func plainTokens(s string) []zoneToken {
	fields := strings.Fields(s)
	toks := make([]zoneToken, len(fields))
	for i, f := range fields {
		toks[i] = zoneToken{text: f}
	}
	return toks
}

// paragraphs is the band's content as token streams, one paragraph per line
// group: the description prose (with the go-tex/engine link inline), the Back
// link on its own line, and the footer line (with the go-tex/brand link). The
// Back link's target is read from the live site root each layout.
func (z *bottomZone) paragraphs() [][]zoneToken {
	prose := plainTokens("A single go-widgets/toolkit canvas app driving the pure-Go")
	prose = append(prose, zoneToken{text: "go-tex/engine", url: engineURL})
	prose = append(prose, plainTokens("- the render pane is a shared go-widgets PagedView with continuous / paginated modes, zoom, and full wheel + keyboard page navigation. Click the render to jump the caret to that source line, and move the caret to scroll the render to its output.")...)

	back := []zoneToken{{text: "<- Back to go-tex", url: z.s.siteRoot}}

	footer := plainTokens("Built with Hugo - Pure Go, CGO=0 - Branding from")
	footer = append(footer, zoneToken{text: "go-tex/brand", url: brandURL})

	return [][]zoneToken{prose, back, footer}
}

// wrapTokens greedily wraps a token stream into lines no wider than maxW device
// pixels, measured through the active font (the same face the Labels paint with),
// with a single space between tokens. A token wider than maxW still gets its own
// line unbroken.
func wrapTokens(toks []zoneToken, maxW, spaceW int) [][]zoneToken {
	var lines [][]zoneToken
	var line []zoneToken
	lineW := 0
	for _, t := range toks {
		tw := toolkit.TextWidth(t.text)
		switch {
		case len(line) == 0:
			line = []zoneToken{t}
			lineW = tw
		case lineW+spaceW+tw <= maxW:
			line = append(line, t)
			lineW += spaceW + tw
		default:
			lines = append(lines, line)
			line = []zoneToken{t}
			lineW = tw
		}
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

// measure wraps every paragraph to width w, positions each token (centred per
// line, X absolute, Y relative to the band's top) and returns the total content
// height. Call place() next to fix the absolute Y and the link rects.
func (z *bottomZone) measure(w int) int {
	z.placedRel = z.placedRel[:0]
	pad := toolkit.Scaled(12)
	lineH := toolkit.Scaled(baseFontPx + 4)
	paraGap := toolkit.Scaled(4)
	spaceW := toolkit.TextWidth(" ")
	maxW := w - 2*pad
	if maxW < 1 {
		maxW = 1
	}
	y := pad
	for pi, toks := range z.paragraphs() {
		if pi > 0 {
			y += paraGap
		}
		for _, line := range wrapTokens(toks, maxW, spaceW) {
			lw := 0
			for i, t := range line {
				if i > 0 {
					lw += spaceW
				}
				lw += toolkit.TextWidth(t.text)
			}
			x := (w - lw) / 2
			if x < pad {
				x = pad
			}
			cx := x
			for i, t := range line {
				if i > 0 {
					cx += spaceW
				}
				tw := toolkit.TextWidth(t.text)
				z.placedRel = append(z.placedRel, placedToken{
					rect: toolkit.Rect{X: cx, Y: y, W: tw, H: lineH},
					tok:  t,
				})
				cx += tw
			}
			y += lineH
		}
	}
	y += pad
	z.contentH = y
	return z.contentH
}

// place offsets the measured tokens to absolute coordinates for the band's final
// bounds and derives the clickable link rects. r.W must match the last measure(w)
// and r.H its returned height.
func (z *bottomZone) place(r toolkit.Rect) {
	z.bounds = r
	z.placed = z.placed[:0]
	z.links = z.links[:0]
	for _, pt := range z.placedRel {
		abs := pt
		abs.rect.Y += r.Y
		z.placed = append(z.placed, abs)
		if pt.tok.url != "" {
			z.links = append(z.links, zoneLink{rect: abs.rect, url: pt.tok.url, text: pt.tok.text})
		}
	}

	// Grow + position one reusable toolkit.Link per link, preserving hover state
	// across frames (the pool is reused, not rebuilt). Positioned here — not only
	// in draw — so a pointer-move that arrives before the next paint still
	// hit-tests against the right bounds. The click path stays in handleClick, so
	// these widgets carry no OnClick; they render the accent ink + hover underline
	// and track hover only.
	for len(z.linkWidgets) < len(z.links) {
		z.linkWidgets = append(z.linkWidgets, &toolkit.Link{})
	}
	for i, ln := range z.links {
		w := z.linkWidgets[i]
		w.Text().Set(ln.text)
		w.SetBounds(ln.rect)
		w.VAlign = toolkit.VMiddle
	}
}

// draw paints the ground, the top hairline and every token Label (links in the
// accent ink so they read as links, prose in the on-surface ink).
func (z *bottomZone) draw(p painter.Painter, theme *toolkit.Theme) {
	r := z.bounds
	if r.W <= 0 || r.H <= 0 {
		return
	}
	z.bg.Fill = theme.Background
	z.bg.SetBounds(r)
	z.bg.Draw(p, theme)

	z.rule.Fill = theme.Border
	z.rule.SetBounds(toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: toolkit.Scaled(1)})
	z.rule.Draw(p, theme)

	for len(z.labelPool) < len(z.placed) {
		z.labelPool = append(z.labelPool, &toolkit.Label{})
	}
	// Link tokens are painted by their reusable toolkit.Link (accent ink, plus an
	// underline while hovered — positioned in place()); plain prose keeps its
	// Label. li walks the links in placed order, parallel to z.links / linkWidgets.
	li := 0
	for i, pt := range z.placed {
		if pt.tok.url != "" {
			z.linkWidgets[li].Draw(p, theme)
			li++
			continue
		}
		lb := z.labelPool[i]
		lb.Text().Set(pt.tok.text)
		lb.SetBounds(pt.rect)
		lb.VAlign = toolkit.VMiddle
		lb.Ink = theme.OnSurface
		lb.Draw(p, theme)
	}
}

// handleMove forwards a pointer-move (surface coordinates) to every link widget
// so the one under the pointer raises its hover underline and the others clear
// theirs. It reports whether any link's hover state changed — the caller repaints
// (and treats the move as consumed) only on a change, and otherwise keeps routing
// the move to whatever else needs it (a drag in progress).
func (z *bottomZone) handleMove(x, y int) bool {
	changed := false
	for i, ln := range z.links {
		if i >= len(z.linkWidgets) {
			break
		}
		w := z.linkWidgets[i]
		was := w.Hovered()
		w.OnEvent(toolkit.Event{Kind: toolkit.EventMouseMove, X: x - ln.rect.X, Y: y - ln.rect.Y})
		if w.Hovered() != was {
			changed = true
		}
	}
	return changed
}

// handleClick navigates when a click lands inside one of the link rects, and
// reports whether it consumed the press. A click elsewhere in the band is left
// unconsumed (there is nothing else interactive here).
func (z *bottomZone) handleClick(x, y int) bool {
	for _, ln := range z.links {
		if ln.rect.Contains(x, y) {
			z.s.doNavigate(ln.url)
			return true
		}
	}
	return false
}

// The following are host- and headless-harness-facing introspection: the topZone
// status line, both bands' device-pixel [x,y,w,h] bounds, and the parallel url +
// rect of every bottomZone link. The browser render proof reads these to assert
// the bands paint at the right place and the canvas fills the height, and the
// wasm shell surfaces them through the gotexZones() hook.

// TopZoneStatusText is the topZone's status line.
func (s *State) TopZoneStatusText() string {
	line, _ := s.topZone.statusLine()
	return line
}

// TopZoneRect / BottomZoneRect are the two bands' device-pixel [x,y,w,h] bounds.
func (s *State) TopZoneRect() [4]int {
	r := s.topZone.bounds
	return [4]int{r.X, r.Y, r.W, r.H}
}

func (s *State) BottomZoneRect() [4]int {
	r := s.bottomZone.bounds
	return [4]int{r.X, r.Y, r.W, r.H}
}

// BottomZoneLinkURLs / BottomZoneLinkRects are the target url and device-pixel
// [x,y,w,h] rect of every bottomZone link, in the same order — so a harness can
// click each rect and know which url it should drive.
func (s *State) BottomZoneLinkURLs() []string {
	out := make([]string, len(s.bottomZone.links))
	for i, ln := range s.bottomZone.links {
		out[i] = ln.url
	}
	return out
}

func (s *State) BottomZoneLinkRects() [][4]int {
	out := make([][4]int, len(s.bottomZone.links))
	for i, ln := range s.bottomZone.links {
		out[i] = [4]int{ln.rect.X, ln.rect.Y, ln.rect.W, ln.rect.H}
	}
	return out
}

// BottomZoneHoveredLink is the index (into BottomZoneLinkURLs / BottomZoneLinkRects)
// of the footer link currently showing its hover underline, or -1 when none is
// hovered. The render proof reads it to assert a pointer-move over a link rect
// raised exactly that link's underline.
func (s *State) BottomZoneHoveredLink() int {
	for i := range s.bottomZone.links {
		if i < len(s.bottomZone.linkWidgets) && s.bottomZone.linkWidgets[i].Hovered() {
			return i
		}
	}
	return -1
}

// ReadyDotRGB is the topZone ready indicator's colour as [r,g,b], so the render
// proof can look for that exact hue in the painted band.
func (s *State) ReadyDotRGB() [3]int {
	return [3]int{int(readyDotColor.R), int(readyDotColor.G), int(readyDotColor.B)}
}
