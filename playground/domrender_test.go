// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// A compiled page comes back as an SVG for the host to draw, sized in the same
// device pixels a bitmap of it would have had — that factor is what keeps the
// render pane's paging, zoom and fit-width behaving exactly as before.
func TestCompileYieldsSizedPages(t *testing.T) {
	res := compileLaTeX(`\documentclass{article}\begin{document}Findable words here.\end{document}`,
		toolkit.DefaultLight(), nil)
	if len(res.svgs) == 0 {
		t.Fatalf("no page compiled: %q", res.errText)
	}
	if len(res.sizes) != len(res.svgs) {
		t.Fatalf("%d sizes for %d pages — they must stay parallel", len(res.sizes), len(res.svgs))
	}
	if res.sizes[0].X <= 0 || res.sizes[0].Y <= 0 {
		t.Errorf("page size = %+v, want a real size", res.sizes[0])
	}
	if !strings.Contains(res.svgs[0], "<text") {
		t.Errorf("the page must carry its own searchable text:\n%.200s", res.svgs[0])
	}
}

// The natural size is the SVG's own point size scaled by rasterScale, so a page
// lays out exactly where its bitmap used to.
func TestNaturalSize(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="489pt" height="787.2pt" viewBox="0 0 489 787.2">`
	if got, want := naturalSize(svg), (image.Point{X: 978, Y: 1574}); got != want {
		t.Errorf("naturalSize = %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		``,
		`<svg viewBox="0 0 1 1">`,               // no width/height at all
		`<svg width="489pt" viewBox="0 0 1 1">`, // height missing
		`<svg width="wide" height="tall">`,      // unparsable
		`<svg width="489pt height="787.2pt"`,    // unterminated attribute
	} {
		if got := naturalSize(bad); got.X > 0 && got.Y > 0 {
			t.Errorf("naturalSize(%q) = %+v, want an undrawable page", bad, got)
		}
	}
}

// The engine paints a page on white with black glyphs. On a dark scheme that
// would be a white sheet in a dark window, so the page is recoloured — which is
// what the rasteriser's Ink and Paper options used to do.
func TestThemeSVGRecoloursPaperAndInk(t *testing.T) {
	page := `<svg><rect width="100%" height="100%" fill="white"/><g fill="black">` +
		`<path fill="#ff0000" d="M0 0"/></g></svg>`
	dark := toolkit.DefaultDark()
	got := themeSVG(page, dark)

	if strings.Contains(got, `fill="white"`) || strings.Contains(got, `fill="black"`) {
		t.Errorf("paper and ink must both be themed: %s", got)
	}
	if !strings.Contains(got, cssColor(dark.Surface)) || !strings.Contains(got, cssColor(dark.OnSurface)) {
		t.Errorf("themed page carries neither the surface nor the ink: %s", got)
	}
	if !strings.Contains(got, `fill="#ff0000"`) {
		t.Errorf("coloured text keeps its own colour: %s", got)
	}
}

func TestCSSColor(t *testing.T) {
	if got := cssColor(toolkit.RGB(0x0A, 0xB1, 0xFF)); got != "#0ab1ff" {
		t.Errorf("cssColor = %q", got)
	}
}

// The host measures where each source line ended up and reports it here; the
// linking downstream is unchanged, because this is the space it always used.
func TestSetLineBandsFeedsTheLinking(t *testing.T) {
	s := newTestState(t, false)
	s.lineMaps = nil
	s.SetLineBands(1, []int{3, 4, 3}, []int{10, 40, 20}, []int{30, 60, 35})

	// Line 3 appeared twice: the band is the union.
	if line, ok := s.lineAtPageY(1, 12); !ok || line != 3 {
		t.Errorf("y=12 resolved to line %d (ok=%v), want 3", line, ok)
	}
	if line, ok := s.lineAtPageY(1, 50); !ok || line != 4 {
		t.Errorf("y=50 resolved to line %d (ok=%v), want 4", line, ok)
	}
	page, y, ok := s.pageYForLine(4)
	if !ok || page != 1 || y != 40 {
		t.Errorf("line 4 is at page %d y %d (ok=%v), want page 1 y 40", page, y, ok)
	}
}

// A ragged or impossible measurement is ignored rather than half-applied: a bad
// report from the host must not half-break the linking.
func TestSetLineBandsRefusesTheUnusable(t *testing.T) {
	s := newTestState(t, false)
	s.lineMaps = nil
	s.SetLineBands(0, []int{1}, []int{0}, []int{1})    // no such page
	s.SetLineBands(1, []int{1, 2}, []int{0}, []int{1}) // ragged
	s.SetLineBands(1, []int{1}, []int{0}, []int{1})    // fine, but…
	if len(s.lineMaps) != 1 {
		t.Fatalf("lineMaps = %d, want the one good report", len(s.lineMaps))
	}
	s.lineMaps = nil
	s.SetLineBands(2, []int{0, 5}, []int{0, 90}, []int{10, 80}) // line<=0, and bot<=top
	if len(s.lineMaps) != 2 || len(s.lineMaps[1].bands) != 0 {
		t.Errorf("nothing measurable must yield no bands: %+v", s.lineMaps)
	}
}

// PageRenders shows only what is on screen: nothing while the Log tab is in
// front, and one entry per visible page otherwise.
func TestPageRendersFollowsTheTab(t *testing.T) {
	s := newTestState(t, false)
	if len(s.PageRenders()) == 0 {
		t.Fatal("the Rendered tab must offer the page it is showing")
	}
	r := s.PageRenders()[0]
	if r.Page != 1 || r.SVG == "" || r.Natural.W <= 0 || r.Clip.W <= 0 {
		t.Errorf("page render = %+v", r)
	}
	s.rightPane.tabs.Selected().Set(tabLog)
	if got := s.PageRenders(); got != nil {
		t.Errorf("the Log tab shows no pages, got %d", len(got))
	}
}
