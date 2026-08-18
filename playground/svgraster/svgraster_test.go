// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package svgraster

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gfx/gfx/raster"
)

var (
	black = color.RGBA{0, 0, 0, 255}
	white = color.RGBA{255, 255, 255, 255}
	red   = color.RGBA{255, 0, 0, 255}
)

// pixAt returns the RGBA at (x,y) of a page.
func pixAt(p *Page, x, y int) color.RGBA {
	i := (y*p.W + x) * 4
	return color.RGBA{p.Pixels[i], p.Pixels[i+1], p.Pixels[i+2], p.Pixels[i+3]}
}

func TestRasterizeFixture(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("testdata", "page.svg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p, err := Rasterize(string(doc), Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	// viewBox is 489 x 153.4 at scale 2.
	if p.W != 978 || p.H != int(math.Round(153.4*2)) {
		t.Fatalf("dims = %dx%d, want 978x%d", p.W, p.H, int(math.Round(153.4*2)))
	}
	if len(p.Pixels) != p.W*p.H*4 {
		t.Fatalf("Pixels len = %d, want %d", len(p.Pixels), p.W*p.H*4)
	}

	// Page must not be blank: some pixels differ from Paper.
	ink := 0
	for i := 0; i+3 < len(p.Pixels); i += 4 {
		if p.Pixels[i] != white.R || p.Pixels[i+1] != white.G || p.Pixels[i+2] != white.B {
			ink++
		}
	}
	if ink == 0 {
		t.Fatal("page is blank: no ink pixels")
	}

	// The first glyph sits at translate(87,81.3) -> device ~ (174,163). Check a
	// band around there carries ink.
	if !regionHasInk(p, 168, 150, 220, 175) {
		t.Fatal("expected ink in the first glyph region")
	}

	// Lines map must be non-empty and every band well formed and in range.
	if len(p.Lines) == 0 {
		t.Fatal("Lines map is empty")
	}
	for ln, band := range p.Lines {
		if band[0] >= band[1] {
			t.Errorf("line %d: band %v not top<bottom", ln, band)
		}
		if band[0] < 0 || band[1] > p.H {
			t.Errorf("line %d: band %v out of [0,%d]", ln, band, p.H)
		}
	}
}

func regionHasInk(p *Page, x0, y0, x1, y1 int) bool {
	for y := y0; y < y1 && y < p.H; y++ {
		for x := x0; x < x1 && x < p.W; x++ {
			c := pixAt(p, x, y)
			if c.R < 200 || c.G < 200 || c.B < 200 {
				return true
			}
		}
	}
	return false
}

func TestPaperTransparentAndOpaque(t *testing.T) {
	doc := `<svg viewBox="0 0 10 10"><g fill="black"></g></svg>`
	// Transparent paper: corner alpha 0.
	p, err := Rasterize(doc, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := pixAt(p, 0, 0); got.A != 0 {
		t.Fatalf("transparent corner alpha = %d, want 0", got.A)
	}
	// Opaque paper: corner equals Paper.
	p2, err := Rasterize(doc, Options{Scale: 1, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if got := pixAt(p2, 0, 0); got != white {
		t.Fatalf("opaque corner = %v, want %v", got, white)
	}
}

func TestInkRecolouring(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("testdata", "page.svg"))
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Rasterize(string(doc), Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := Rasterize(string(doc), Options{Scale: 2, Ink: red, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	// Find a strongly inked (dark) pixel in the black render.
	fx, fy, found := -1, -1, false
	for y := 0; y < pb.H && !found; y++ {
		for x := 0; x < pb.W; x++ {
			c := pixAt(pb, x, y)
			if c.R < 60 && c.G < 60 && c.B < 60 {
				fx, fy, found = x, y, true
				break
			}
		}
	}
	if !found {
		t.Fatal("no dark inked pixel found in black render")
	}
	cr := pixAt(pr, fx, fy)
	cb := pixAt(pb, fx, fy)
	if cr == cb {
		t.Fatal("ink recolouring did not change the pixel")
	}
	if !(cr.R > cr.G && cr.R > cr.B) {
		t.Fatalf("recoloured pixel %v is not reddish", cr)
	}
}

func TestLiteralColourNotRemapped(t *testing.T) {
	doc := `<svg viewBox="0 0 10 10"><rect x="0" y="0" width="10" height="10" fill="#3366cc"/></svg>`
	p, err := Rasterize(doc, Options{Scale: 1, Ink: red, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	c := pixAt(p, 5, 5)
	if !near(c.R, 0x33) || !near(c.G, 0x66) || !near(c.B, 0xcc) {
		t.Fatalf("literal colour = %v, want ~ (51,102,204)", c)
	}
}

func near(a, b uint8) bool {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return d <= 4
}

func TestRasterizeErrors(t *testing.T) {
	for _, in := range []string{"not svg", "", "<html></html>", "<svg></svg>", `<svg foo="bar"></svg>`} {
		if _, err := Rasterize(in, Options{}); err == nil {
			t.Errorf("Rasterize(%q) = nil error, want error", in)
		}
	}
}

func TestScaleDefault(t *testing.T) {
	doc := `<svg viewBox="0 0 10 10"><rect width="100%" height="100%" fill="white"/></svg>`
	p, err := Rasterize(doc, Options{Paper: white}) // Scale<=0 -> 2.0
	if err != nil {
		t.Fatal(err)
	}
	if p.W != 20 || p.H != 20 {
		t.Fatalf("default scale dims = %dx%d, want 20x20", p.W, p.H)
	}
}

func TestRootViewportFallback(t *testing.T) {
	// No viewBox: fall back to width/height in pt.
	doc := `<svg width="20pt" height="10pt"><rect width="100%" height="100%" fill="white"/></svg>`
	p, err := Rasterize(doc, Options{Scale: 1, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if p.W != 20 || p.H != 10 {
		t.Fatalf("fallback dims = %dx%d, want 20x10", p.W, p.H)
	}
	// Malformed viewBox (3 numbers) then width/height fallback.
	doc2 := `<svg viewBox="0 0 5" width="8" height="4"/>`
	p2, err := Rasterize(doc2, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p2.W != 8 || p2.H != 4 {
		t.Fatalf("bad-viewBox fallback dims = %dx%d, want 8x4", p2.W, p2.H)
	}
}

func TestZeroSizedDeviceIsError(t *testing.T) {
	doc := `<svg viewBox="0 0 1 1"><rect width="100%" height="100%" fill="white"/></svg>`
	if _, err := Rasterize(doc, Options{Scale: 0.1}); err == nil {
		t.Fatal("expected error for zero-sized device")
	}
}

func TestPathCommands(t *testing.T) {
	// Exercise every command family incl. relatives, and confirm ink appears.
	docs := []string{
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 L18 2 L18 18 L2 18 Z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="m2 2 l16 0 l0 16 l-16 0 z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 H18 V18 H2 Z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="m2 2 h16 v16 h-16 z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 10 C2 2 18 2 18 10 C18 18 2 18 2 10 Z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 10 c0 -8 16 -8 16 0 s-16 8 -16 0 z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 10 S10 2 18 10 Z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 10 Q10 2 18 10 T2 10 Z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 10 q8 -8 16 0 t-16 0 z"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 18 2 18 18 Z"/></svg>`,                 // implicit lineto after M
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 L8 2 L8 8 Z m4 4 l6 0 l0 6 z"/></svg>`, // relative m re-open after Z
	}
	for i, d := range docs {
		p, err := Rasterize(d, Options{Scale: 4, Ink: black, Paper: white})
		if err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
		if !regionHasInk(p, 0, 0, p.W, p.H) {
			t.Errorf("doc %d produced no ink: %s", i, d)
		}
	}
}

func TestMalformedPathsSkipped(t *testing.T) {
	// Arc, unstarted drawing command, bad number, move-only, garbage command.
	docs := []string{
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 A5 5 0 0 1 10 10"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="L2 2 10 10"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 L1.2.3 5"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 X9"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d=""/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black"/></svg>`, // no d at all
		`<svg viewBox="0 0 20 20"><path fill="black" d="H5"/></svg>`,
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 C1 1 2"/></svg>`,     // truncated cubic
		`<svg viewBox="0 0 20 20"><path fill="black" d="M2"/></svg>`,              // truncated moveto
		`<svg viewBox="0 0 20 20"><path fill="black" d="M0 0 H"/></svg>`,          // truncated H
		`<svg viewBox="0 0 20 20"><path fill="black" d="V5"/></svg>`,              // V without move
		`<svg viewBox="0 0 20 20"><path fill="black" d="M0 0 V"/></svg>`,          // truncated V
		`<svg viewBox="0 0 20 20"><path fill="black" d="C1 1 2 2 3 3"/></svg>`,    // C without move
		`<svg viewBox="0 0 20 20"><path fill="black" d="S1 1 2 2"/></svg>`,        // S without move
		`<svg viewBox="0 0 20 20"><path fill="black" d="M0 0 S1 1 2"/></svg>`,     // truncated S
		`<svg viewBox="0 0 20 20"><path fill="black" d="Q1 1 2 2"/></svg>`,        // Q without move
		`<svg viewBox="0 0 20 20"><path fill="black" d="M0 0 Q1 1 2"/></svg>`,     // truncated Q
		`<svg viewBox="0 0 20 20"><path fill="black" d="T1 1"/></svg>`,            // T without move
		`<svg viewBox="0 0 20 20"><path fill="black" d="M0 0 T1"/></svg>`,         // truncated T
		`<svg viewBox="0 0 20 20"><path fill="none" d="M2 2 L8 2 L8 8 Z"/></svg>`, // fill=none path -> not painted
	}
	for i, d := range docs {
		p, err := Rasterize(d, Options{Scale: 2, Ink: black, Paper: white})
		if err != nil {
			t.Fatalf("doc %d: unexpected error %v", i, err)
		}
		if p == nil {
			t.Fatalf("doc %d: nil page", i)
		}
	}
}

func TestFillResolution(t *testing.T) {
	// none -> nothing painted; unknown -> inherit black -> Ink.
	none := `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="none"/></svg>`
	p, err := Rasterize(none, Options{Scale: 1, Ink: red, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if pixAt(p, 5, 5) != white {
		t.Fatalf("fill=none painted something: %v", pixAt(p, 5, 5))
	}

	// currentColor and inherited default both map to Ink.
	cc := `<svg viewBox="0 0 10 10"><g fill="black"><rect width="10" height="10" fill="currentColor"/></g></svg>`
	p2, _ := Rasterize(cc, Options{Scale: 1, Ink: red, Paper: white})
	if !near(pixAt(p2, 5, 5).R, 255) || pixAt(p2, 5, 5).G > 20 {
		t.Fatalf("currentColor not mapped to red Ink: %v", pixAt(p2, 5, 5))
	}

	// #rgb short hex.
	sh := `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="#f00"/></svg>`
	p3, _ := Rasterize(sh, Options{Scale: 1, Ink: black, Paper: white})
	if !near(pixAt(p3, 5, 5).R, 255) || pixAt(p3, 5, 5).G > 8 {
		t.Fatalf("#f00 wrong: %v", pixAt(p3, 5, 5))
	}

	// unknown fill keyword inherits (black default -> Ink).
	uk := `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="chartreuse"/></svg>`
	p4, _ := Rasterize(uk, Options{Scale: 1, Ink: red, Paper: white})
	if pixAt(p4, 5, 5).R < 200 {
		t.Fatalf("unknown fill did not inherit Ink: %v", pixAt(p4, 5, 5))
	}
}

func TestTransformsAndNestedSVG(t *testing.T) {
	// translate + scale + matrix, plus a nested <svg> with its own viewBox.
	doc := `<svg viewBox="0 0 40 40">` +
		`<g transform="translate(5,5) scale(2)"><rect width="3" height="3" fill="black"/></g>` +
		`<g transform="matrix(1,0,0,1,20,20)"><rect width="4" height="4" fill="black"/></g>` +
		`<g transform="translate(0,30)"><svg x="0" y="0" width="10" height="8" viewBox="0 0 5 4">` +
		`<rect width="5" height="4" fill="black"/></svg></g>` +
		`</svg>`
	p, err := Rasterize(doc, Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	// Scaled rect: user (5,5)-(11,11) -> device (10,10)-(22,22).
	if pixAt(p, 15, 15).R > 100 {
		t.Errorf("scaled rect not painted: %v", pixAt(p, 15, 15))
	}
	// matrix rect: user (20,20)-(24,24) -> device (40,40)-(48,48).
	if pixAt(p, 44, 44).R > 100 {
		t.Errorf("matrix rect not painted: %v", pixAt(p, 44, 44))
	}
	// nested svg content should paint near device y ~ 60+.
	if !regionHasInk(p, 0, 60, 25, p.H) {
		t.Errorf("nested svg content not painted")
	}
}

func TestNestedSVGWithoutViewBox(t *testing.T) {
	doc := `<svg viewBox="0 0 20 20"><g transform="translate(2,2)">` +
		`<svg x="0" y="0" width="10" height="10"><rect width="10" height="10" fill="black"/></svg>` +
		`</g></svg>`
	p, err := Rasterize(doc, Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if pixAt(p, 10, 10).R > 100 {
		t.Errorf("nested svg without viewBox not painted: %v", pixAt(p, 10, 10))
	}
	// Nested svg with viewBox but missing width -> no extra scaling applied.
	doc2 := `<svg viewBox="0 0 20 20"><g transform="translate(2,2)">` +
		`<svg x="0" y="0" viewBox="0 0 10 10"><rect width="10" height="10" fill="black"/></svg>` +
		`</g></svg>`
	if _, err := Rasterize(doc2, Options{Scale: 2, Paper: white}); err != nil {
		t.Fatal(err)
	}
}

func TestCircle(t *testing.T) {
	doc := `<svg viewBox="0 0 20 20"><circle cx="10" cy="10" r="6" fill="black"/></svg>`
	p, err := Rasterize(doc, Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if pixAt(p, 20, 20).R > 100 {
		t.Errorf("circle centre not painted: %v", pixAt(p, 20, 20))
	}
	// r<=0 circle draws nothing (and must not panic).
	doc2 := `<svg viewBox="0 0 20 20"><circle cx="10" cy="10" r="0" fill="black"/></svg>`
	if _, err := Rasterize(doc2, Options{Scale: 2, Paper: white}); err != nil {
		t.Fatal(err)
	}
	// fill=none circle paints nothing.
	doc3 := `<svg viewBox="0 0 20 20"><circle cx="10" cy="10" r="6" fill="none"/></svg>`
	p3, err := Rasterize(doc3, Options{Scale: 2, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if pixAt(p3, 20, 20) != white {
		t.Fatalf("fill=none circle painted: %v", pixAt(p3, 20, 20))
	}
}

func TestEmbeddedImage(t *testing.T) {
	// Build a 2x2 image: opaque red and a half-transparent pixel.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	src.Set(1, 0, color.NRGBA{0, 255, 0, 255})
	src.Set(0, 1, color.NRGBA{0, 0, 255, 255})
	src.Set(1, 1, color.NRGBA{0, 0, 0, 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	doc := `<svg viewBox="0 0 20 20"><g data-l="3"><image x="4" y="4" width="12" height="12" href="` + uri + `"/></g></svg>`
	p, err := Rasterize(doc, Options{Scale: 2, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	// Somewhere in the destination there should be a strongly red pixel.
	foundRed := false
	for y := 0; y < p.H && !foundRed; y++ {
		for x := 0; x < p.W; x++ {
			c := pixAt(p, x, y)
			if c.R > 200 && c.G < 80 && c.B < 80 {
				foundRed = true
				break
			}
		}
	}
	if !foundRed {
		t.Fatal("embedded image not blitted (no red pixel)")
	}
	if len(p.Lines) == 0 {
		t.Fatal("image did not record a data-l band")
	}
}

func TestImageXlinkHrefAndFailures(t *testing.T) {
	// xlink:href fallback with a valid tiny PNG.
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.Set(0, 0, color.NRGBA{10, 20, 30, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	doc := `<svg viewBox="0 0 10 10"><image x="0" y="0" width="10" height="10" xlink:href="` + uri + `"/></svg>`
	if _, err := Rasterize(doc, Options{Scale: 1, Paper: white}); err != nil {
		t.Fatal(err)
	}

	// Various skip paths: no href, non-data uri, non-base64, bad base64,
	// undecodable payload, zero-sized rect.
	bad := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))
	docs := []string{
		`<svg viewBox="0 0 10 10"><image x="0" y="0" width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="http://example.com/x.png" width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="data:image/png,notbase64" width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="data:image/png;base64,@@@@" width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="` + bad + `" width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="` + uri + `" width="0" height="0"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="data:image/png;base64," width="10" height="10"/></svg>`,
		`<svg viewBox="0 0 10 10"><image href="data:image/png;base64" width="10" height="10"/></svg>`, // no comma
	}
	for i, d := range docs {
		if _, err := Rasterize(d, Options{Scale: 1, Paper: white}); err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
	}
}

func TestImageClampedOffscreen(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	// Image placed fully outside the viewport via a large translate.
	doc := `<svg viewBox="0 0 10 10"><g transform="translate(100,100)">` +
		`<image x="0" y="0" width="10" height="10" href="` + uri + `"/></g></svg>`
	if _, err := Rasterize(doc, Options{Scale: 1, Paper: white}); err != nil {
		t.Fatal(err)
	}
}

func TestRectEdgeCases(t *testing.T) {
	// fill=none rect and zero-dimension rect both paint nothing.
	docs := []string{
		`<svg viewBox="0 0 10 10"><rect width="0" height="0" fill="black"/></svg>`,
		`<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="none"/></svg>`,
	}
	for i, d := range docs {
		p, err := Rasterize(d, Options{Scale: 1, Ink: black, Paper: white})
		if err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
		if pixAt(p, 5, 5) != white {
			t.Errorf("doc %d painted unexpectedly: %v", i, pixAt(p, 5, 5))
		}
	}
}

func TestFillPathClampedOut(t *testing.T) {
	// A valid path entirely off-surface -> Fill returns ok=false, no panic.
	doc := `<svg viewBox="0 0 10 10"><g transform="translate(-100,-100)">` +
		`<path fill="black" d="M2 2 L8 2 L8 8 Z"/></g></svg>`
	if _, err := Rasterize(doc, Options{Scale: 1, Paper: white}); err != nil {
		t.Fatal(err)
	}
}

func TestDataLZeroIgnored(t *testing.T) {
	// data-l="0" and non-numeric data-l must not create a Lines entry.
	doc := `<svg viewBox="0 0 10 10"><g data-l="0"><rect width="10" height="10" fill="black"/></g>` +
		`<g data-l="x"><rect width="2" height="2" fill="black"/></g></svg>`
	p, err := Rasterize(doc, Options{Scale: 1, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 0 {
		t.Fatalf("expected no Lines, got %v", p.Lines)
	}
}

// --- white-box unit tests for helpers and defensive branches ---

func TestParseTransformCases(t *testing.T) {
	cases := []struct {
		in     string
		x, y   float64
		wx, wy float64
	}{
		{"translate(5)", 0, 0, 5, 0},
		{"translate(5,6)", 0, 0, 5, 6},
		{"scale(2)", 3, 4, 6, 8},
		{"scale(2,3)", 3, 4, 6, 12},
		{"matrix(1,0,0,1,7,8)", 0, 0, 7, 8},
		{"rotate(0)", 1, 1, 1, 1},     // unknown primitive ignored
		{"foo", 1, 1, 1, 1},           // no '(' -> break
		{"translate(1,2", 1, 1, 1, 1}, // no ')' -> break
		{"matrix(1,2,3)", 1, 1, 1, 1}, // too few args -> ignored
	}
	for _, c := range cases {
		m := parseTransform(c.in)
		gx, gy := m.apply(c.x, c.y)
		if !almost(gx, c.wx) || !almost(gy, c.wy) {
			t.Errorf("%q: got (%g,%g) want (%g,%g)", c.in, gx, gy, c.wx, c.wy)
		}
	}
}

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestParseLen(t *testing.T) {
	cases := []struct {
		s   string
		ref float64
		w   float64
	}{
		{"", 100, 0},
		{"50%", 100, 50},
		{"bad%", 100, 0},
		{"12pt", 0, 12},
		{"12px", 0, 12},
		{"7", 0, 7},
		{"garbage", 0, 0},
	}
	for _, c := range cases {
		if got := parseLen(c.s, c.ref); !almost(got, c.w) {
			t.Errorf("parseLen(%q,%g) = %g, want %g", c.s, c.ref, got, c.w)
		}
	}
}

func TestParseFloatsSkipsBad(t *testing.T) {
	got := parseFloats("1 x 2,,3")
	want := []float64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestResolveFillAndHex(t *testing.T) {
	if _, paint := resolveFill("none", black, true, black, white); paint {
		t.Error("none should not paint")
	}
	if c, _ := resolveFill("black", black, true, red, white); c != red {
		t.Error("black should map to ink")
	}
	if c, _ := resolveFill("white", black, true, red, white); c != white {
		t.Error("white should map to paper")
	}
	if c, _ := resolveFill("", red, true, black, white); c != red {
		t.Error("empty should inherit")
	}
	if c, _ := resolveFill("#abc", black, true, black, white); c != (color.RGBA{0xaa, 0xbb, 0xcc, 255}) {
		t.Errorf("#abc wrong: %v", c)
	}

	// parseHexColor edge cases.
	bad := []string{"", "red", "#12", "#12g", "#12345g", "#1234567"}
	for _, s := range bad {
		if _, ok := parseHexColor(s); ok {
			t.Errorf("parseHexColor(%q) unexpectedly ok", s)
		}
	}
	if c, ok := parseHexColor("#ABCDEF"); !ok || c != (color.RGBA{0xab, 0xcd, 0xef, 255}) {
		t.Errorf("#ABCDEF = %v ok=%v", c, ok)
	}
}

func TestBlendOver(t *testing.T) {
	dst := []uint8{255, 255, 255, 255}
	blendOver(dst, 0, 0, 0, 0) // sa==0 -> unchanged
	if dst[0] != 255 {
		t.Error("sa=0 changed dst")
	}
	blendOver(dst, 10, 20, 30, 255) // opaque replace
	if dst[0] != 10 || dst[3] != 255 {
		t.Errorf("opaque over failed: %v", dst)
	}
	dst2 := []uint8{200, 200, 200, 255}
	blendOver(dst2, 0, 0, 0, 128) // partial over
	if dst2[0] == 200 || dst2[0] == 0 {
		t.Errorf("partial blend not applied: %v", dst2)
	}
	// Partial over a transparent destination exercises the outA math.
	dst3 := []uint8{0, 0, 0, 0}
	blendOver(dst3, 255, 0, 0, 128)
	if dst3[3] == 0 {
		t.Errorf("partial over transparent left alpha 0: %v", dst3)
	}
}

func TestMarkDefensive(t *testing.T) {
	r := &renderer{img: raster.New(10, 10), lines: map[int][2]int{}}
	r.mark(0, 1, 2)   // ln<=0 ignored
	r.mark(5, 3, 3)   // bottom<=top ignored
	r.mark(5, -5, 20) // clamps to [0,10]
	if got := r.lines[5]; got != [2]int{0, 10} {
		t.Fatalf("clamped band = %v, want [0 10]", got)
	}
	r.mark(5, 2, 4) // union widens/narrows correctly -> stays [0,10]
	if got := r.lines[5]; got != [2]int{0, 10} {
		t.Fatalf("union band = %v, want [0 10]", got)
	}
	// A fully out-of-range band (top>=bottom after clamp) is ignored.
	r.mark(6, 20, 25)
	if _, ok := r.lines[6]; ok {
		t.Fatalf("out-of-range band recorded: %v", r.lines[6])
	}
	if len(r.lines) != 1 {
		t.Fatalf("unexpected lines: %v", r.lines)
	}
}

func TestAtoiSafe(t *testing.T) {
	if atoiSafe("") != 0 || atoiSafe("12a") != 0 || atoiSafe("034") != 34 {
		t.Fatalf("atoiSafe wrong")
	}
}

func TestMinMaxClamp(t *testing.T) {
	if minf([]float64{3, 1, 2}) != 1 || maxf([]float64{3, 1, 2}) != 3 {
		t.Fatal("minf/maxf wrong")
	}
	if clampi(-1, 0, 10) != 0 || clampi(11, 0, 10) != 10 || clampi(5, 0, 10) != 5 {
		t.Fatal("clampi wrong")
	}
}

func TestNumberScientific(t *testing.T) {
	// Path using scientific notation exercises the e/E branch of the scanner.
	doc := `<svg viewBox="0 0 20 20"><path fill="black" d="M2 2 L1.8e+1 2 L1.8e+1 1.8e1 Z"/></svg>`
	p, err := Rasterize(doc, Options{Scale: 2, Ink: black, Paper: white})
	if err != nil {
		t.Fatal(err)
	}
	if !regionHasInk(p, 0, 0, p.W, p.H) {
		t.Fatal("scientific-notation path produced no ink")
	}
}
