// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package svgraster rasterizes the SVG pages emitted by the go-tex engine into
// straight-alpha RGBA bitmaps that can be painted into a go-widgets canvas.
//
// It understands only the specific SVG subset the engine generates: a root
// <svg> with a viewBox, nested <g> groups carrying transforms, per-source-line
// <g data-l="N"> markers, a white background <rect>, glyph/rule <path> outlines,
// nested <svg> islands for mathematics, embedded raster <image> data URIs and
// the occasional <circle>. It is not a general-purpose SVG renderer.
package svgraster

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg" // register JPEG decoder for embedded images
	_ "image/png"  // register PNG decoder for embedded images
	"math"
	"strings"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
)

// Options controls rasterization.
type Options struct {
	Scale float64    // device pixels per SVG user-unit (pt). <=0 defaults to 2.0
	Ink   color.RGBA // colour used for the default black / currentColor fills
	Paper color.RGBA // page background fill (the engine's fill="white" rect). If Paper.A==0, no background is painted (transparent).
}

// Page is a rasterized SVG page.
type Page struct {
	Pixels []byte         // straight-alpha RGBA, len == W*H*4, row-major top-to-bottom
	W, H   int            // device pixel dimensions
	Lines  map[int][2]int // 1-based source line -> [topY, bottomY) device-pixel band covered by that line's <g data-l="N"> content (for click-to-source nav). Only lines that drew something appear.
}

// xnode is a generic XML element used to load the whole page tree.
type xnode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []xnode    `xml:",any"`
}

// attr returns the value of the named attribute (namespace-insensitive) and
// whether it was present.
func (n *xnode) attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

// attrOr returns the named attribute or def when absent.
func (n *xnode) attrOr(name, def string) string {
	if v, ok := n.attr(name); ok {
		return v
	}
	return def
}

// errNoSVG is returned when the document has no parseable <svg> root.
var errNoSVG = errors.New("svgraster: no parseable <svg> root")

// state is the inherited rendering context flowing down the element tree.
type state struct {
	m        matrix     // composed user->device transform
	fill     color.RGBA // resolved current fill colour
	paint    bool       // whether the current fill paints anything
	line     int        // current data-l source line (0 = none)
	vpW, vpH float64    // current viewport size, for percentage lengths
}

// renderer holds the target surface and per-page options.
type renderer struct {
	img   *raster.Image
	opt   Options
	ink   color.RGBA
	paper color.RGBA
	lines map[int][2]int
}

// Rasterize parses ONE engine SVG page document and renders it to a Page.
// It returns an error only for a document with no parseable <svg> root.
func Rasterize(svgDoc string, opt Options) (*Page, error) {
	var root xnode
	if err := xml.Unmarshal([]byte(svgDoc), &root); err != nil {
		return nil, errNoSVG
	}
	if root.XMLName.Local != "svg" {
		return nil, errNoSVG
	}

	scale := opt.Scale
	if scale <= 0 {
		scale = 2.0
	}

	vbW, vbH, ok := rootViewport(&root)
	if !ok {
		return nil, errNoSVG
	}

	w := int(math.Round(vbW * scale))
	h := int(math.Round(vbH * scale))
	if w <= 0 || h <= 0 {
		return nil, errNoSVG
	}

	ink := opt.Ink
	if ink == (color.RGBA{}) {
		ink = color.RGBA{0, 0, 0, 255}
	}

	r := &renderer{
		img:   raster.New(w, h),
		opt:   opt,
		ink:   ink,
		paper: opt.Paper,
		lines: make(map[int][2]int),
	}

	// Opaque background: prefill the whole surface with Paper.
	if opt.Paper.A > 0 {
		for i := 0; i+3 < len(r.img.Pix); i += 4 {
			r.img.Pix[i] = opt.Paper.R
			r.img.Pix[i+1] = opt.Paper.G
			r.img.Pix[i+2] = opt.Paper.B
			r.img.Pix[i+3] = opt.Paper.A
		}
	}

	st := state{
		m:     matrix{scale, 0, 0, scale, 0, 0},
		fill:  ink,
		paint: true,
		line:  0,
		vpW:   vbW,
		vpH:   vbH,
	}
	for i := range root.Children {
		r.render(&root.Children[i], st)
	}

	return &Page{
		Pixels: r.img.Pix,
		W:      w,
		H:      h,
		Lines:  r.lines,
	}, nil
}

// rootViewport determines the SVG user-space dimensions from viewBox (preferred)
// or width/height. The viewBox minimum is assumed to be 0 0.
func rootViewport(root *xnode) (float64, float64, bool) {
	if vb, ok := root.attr("viewBox"); ok {
		f := parseFloats(vb)
		if len(f) == 4 && f[2] > 0 && f[3] > 0 {
			return f[2], f[3], true
		}
	}
	w := parseLen(root.attrOr("width", ""), 0)
	h := parseLen(root.attrOr("height", ""), 0)
	if w > 0 && h > 0 {
		return w, h, true
	}
	return 0, 0, false
}

// render walks one element, deriving a child state and drawing leaf shapes.
func (r *renderer) render(n *xnode, parent state) {
	st := parent

	if v, ok := n.attr("transform"); ok {
		st.m = parent.m.mul(parseTransform(v))
	}
	if v, ok := n.attr("data-l"); ok {
		if ln := atoiSafe(v); ln > 0 {
			st.line = ln
		}
	}
	if v, ok := n.attr("fill"); ok {
		st.fill, st.paint = resolveFill(v, parent.fill, parent.paint, r.ink, r.paper)
	}

	switch n.XMLName.Local {
	case "path":
		r.drawPath(n, st)
	case "rect":
		r.drawRect(n, st)
	case "circle":
		r.drawCircle(n, st)
	case "image":
		r.drawImage(n, st)
	case "svg":
		// A nested <svg> island: apply x/y offset and viewBox scaling, then
		// descend into its children as a group.
		st.m = r.nestedTransform(n, st)
		if vb, ok := n.attr("viewBox"); ok {
			if f := parseFloats(vb); len(f) == 4 && f[2] > 0 && f[3] > 0 {
				st.vpW, st.vpH = f[2], f[3]
			}
		}
		for i := range n.Children {
			r.render(&n.Children[i], st)
		}
	default:
		// <g> and any other container: just descend.
		for i := range n.Children {
			r.render(&n.Children[i], st)
		}
	}
}

// nestedTransform builds the transform for a nested <svg>: the element's own
// transform (already folded into st.m by render) composed with translate(x,y)
// and, if a viewBox is present, an extra scale mapping the viewBox onto
// width/height.
func (r *renderer) nestedTransform(n *xnode, st state) matrix {
	x := parseLen(n.attrOr("x", "0"), st.vpW)
	y := parseLen(n.attrOr("y", "0"), st.vpH)
	m := st.m.mul(matrix{1, 0, 0, 1, x, y})
	if vb, ok := n.attr("viewBox"); ok {
		f := parseFloats(vb)
		if len(f) == 4 && f[2] > 0 && f[3] > 0 {
			w := parseLen(n.attrOr("width", ""), st.vpW)
			h := parseLen(n.attrOr("height", ""), st.vpH)
			if w > 0 && h > 0 {
				m = m.mul(matrix{w / f[2], 0, 0, h / f[3], 0, 0})
			}
		}
	}
	return m
}

// fillPath rasterizes and composites a device-space path, recording the covered
// Y band against the current source line.
func (r *renderer) fillPath(p *vector.Path, st state) {
	if !st.paint {
		return
	}
	var rz vector.Rasterizer
	cov, ox, oy, w, h, ok := rz.Fill(p, vector.NonZero, r.img.W, r.img.H)
	if !ok {
		return
	}
	c := st.fill
	c.A = 255
	vector.Composite(r.img, cov, ox, oy, w, h, vector.SolidPaint{Color: c})
	r.mark(st.line, oy, oy+h)
}

// drawPath fills a <path> element.
func (r *renderer) drawPath(n *xnode, st state) {
	d, ok := n.attr("d")
	if !ok {
		return
	}
	p, ok := buildPath(d, st.m)
	if !ok {
		return
	}
	r.fillPath(p, st)
}

// drawRect fills a <rect> element. A full-page (100% x 100%) rect is the page
// background and is skipped when transparent.
func (r *renderer) drawRect(n *xnode, st state) {
	if !st.paint {
		return
	}
	x := parseLen(n.attrOr("x", "0"), st.vpW)
	y := parseLen(n.attrOr("y", "0"), st.vpH)
	w := parseLen(n.attrOr("width", "0"), st.vpW)
	h := parseLen(n.attrOr("height", "0"), st.vpH)
	if w <= 0 || h <= 0 {
		return
	}
	p := vector.NewPath()
	x0, y0 := st.m.apply(x, y)
	x1, y1 := st.m.apply(x+w, y)
	x2, y2 := st.m.apply(x+w, y+h)
	x3, y3 := st.m.apply(x, y+h)
	p.MoveTo(x0, y0)
	p.LineTo(x1, y1)
	p.LineTo(x2, y2)
	p.LineTo(x3, y3)
	p.Close()
	r.fillPath(p, st)
}

// drawCircle fills a <circle> element, approximating the disc with four cubics.
func (r *renderer) drawCircle(n *xnode, st state) {
	if !st.paint {
		return
	}
	cx := parseLen(n.attrOr("cx", "0"), st.vpW)
	cy := parseLen(n.attrOr("cy", "0"), st.vpH)
	rr := parseLen(n.attrOr("r", "0"), st.vpW)
	if rr <= 0 {
		return
	}
	const k = 0.5522847498307936
	p := vector.NewPath()
	move := func(x, y float64) { dx, dy := st.m.apply(x, y); p.MoveTo(dx, dy) }
	cube := func(c1x, c1y, c2x, c2y, x, y float64) {
		a1x, a1y := st.m.apply(c1x, c1y)
		a2x, a2y := st.m.apply(c2x, c2y)
		ex, ey := st.m.apply(x, y)
		p.CubicTo(a1x, a1y, a2x, a2y, ex, ey)
	}
	move(cx+rr, cy)
	cube(cx+rr, cy+k*rr, cx+k*rr, cy+rr, cx, cy+rr)
	cube(cx-k*rr, cy+rr, cx-rr, cy+k*rr, cx-rr, cy)
	cube(cx-rr, cy-k*rr, cx-k*rr, cy-rr, cx, cy-rr)
	cube(cx+k*rr, cy-rr, cx+rr, cy-k*rr, cx+rr, cy)
	p.Close()
	r.fillPath(p, st)
}

// drawImage decodes an embedded raster <image> and blits it into the transformed
// destination rectangle with nearest-neighbour sampling. Decode failures are
// silently skipped.
func (r *renderer) drawImage(n *xnode, st state) {
	href, ok := n.attr("href")
	if !ok {
		if href, ok = n.attr("xlink:href"); !ok {
			return
		}
	}
	data, ok := decodeDataURI(href)
	if !ok {
		return
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	x := parseLen(n.attrOr("x", "0"), st.vpW)
	y := parseLen(n.attrOr("y", "0"), st.vpH)
	w := parseLen(n.attrOr("width", "0"), st.vpW)
	h := parseLen(n.attrOr("height", "0"), st.vpH)
	if w <= 0 || h <= 0 {
		return
	}

	// Destination bounding box from the four transformed corners.
	xs := []float64{}
	ys := []float64{}
	for _, c := range [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}} {
		dx, dy := st.m.apply(c[0], c[1])
		xs = append(xs, dx)
		ys = append(ys, dy)
	}
	dx0 := int(math.Floor(minf(xs)))
	dy0 := int(math.Floor(minf(ys)))
	dx1 := int(math.Ceil(maxf(xs)))
	dy1 := int(math.Ceil(maxf(ys)))
	dx0, dy0 = clampi(dx0, 0, r.img.W), clampi(dy0, 0, r.img.H)
	dx1, dy1 = clampi(dx1, 0, r.img.W), clampi(dy1, 0, r.img.H)
	if dx1 <= dx0 || dy1 <= dy0 {
		return
	}

	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	spanX := float64(dx1 - dx0)
	spanY := float64(dy1 - dy0)
	// Nearest-neighbour: map each destination pixel centre onto a source index
	// in [0, sw-1] / [0, sh-1]. Rounding against (dim-1) keeps the index in range
	// without a boundary clamp.
	for py := dy0; py < dy1; py++ {
		fy := (float64(py-dy0) + 0.5) / spanY
		syc := sb.Min.Y + int(math.Round(fy*float64(sh-1)))
		for px := dx0; px < dx1; px++ {
			fx := (float64(px-dx0) + 0.5) / spanX
			sxc := sb.Min.X + int(math.Round(fx*float64(sw-1)))
			cr, cg, cb, ca := src.At(sxc, syc).RGBA()
			off := r.img.PixOffset(px, py)
			blendOver(r.img.Pix[off:off+4], uint8(cr>>8), uint8(cg>>8), uint8(cb>>8), uint8(ca>>8))
		}
	}
	r.mark(st.line, dy0, dy1)
}

// blendOver composites a straight-alpha source pixel over a straight-alpha
// destination pixel in place.
func blendOver(dst []uint8, sr, sg, sb, sa uint8) {
	if sa == 0 {
		return
	}
	if sa == 255 {
		dst[0], dst[1], dst[2], dst[3] = sr, sg, sb, 255
		return
	}
	af := float64(sa) / 255
	da := float64(dst[3]) / 255
	outA := af + da*(1-af) // > 0 because af > 0 here
	blend := func(s, d uint8) uint8 {
		sv := float64(s) / 255 * af
		dv := float64(d) / 255 * da * (1 - af)
		return uint8(math.Round((sv + dv) / outA * 255))
	}
	dst[0] = blend(sr, dst[0])
	dst[1] = blend(sg, dst[1])
	dst[2] = blend(sb, dst[2])
	dst[3] = uint8(math.Round(outA * 255))
}

// mark unions [top, bottom) into the band recorded for source line ln.
func (r *renderer) mark(ln, top, bottom int) {
	if ln <= 0 || bottom <= top {
		return
	}
	if top < 0 {
		top = 0
	}
	if bottom > r.img.H {
		bottom = r.img.H
	}
	if bottom <= top {
		return
	}
	if cur, ok := r.lines[ln]; ok {
		if top > cur[0] {
			top = cur[0]
		}
		if bottom < cur[1] {
			bottom = cur[1]
		}
	}
	r.lines[ln] = [2]int{top, bottom}
}

// decodeDataURI extracts the raw bytes of a base64 data: URI. It reports
// ok=false for non-data or non-base64 URIs.
func decodeDataURI(href string) ([]byte, bool) {
	href = strings.TrimSpace(href)
	if !strings.HasPrefix(href, "data:") {
		return nil, false
	}
	comma := strings.IndexByte(href, ',')
	if comma < 0 {
		return nil, false
	}
	meta := href[len("data:"):comma]
	if !strings.Contains(meta, "base64") {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(href[comma+1:]))
	if err != nil {
		return nil, false
	}
	return data, true
}

// atoiSafe parses a non-negative integer, returning 0 on any error.
func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if len(s) == 0 {
		return 0
	}
	return n
}

func minf(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxf(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
