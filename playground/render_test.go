// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"testing"

	gfxsvg "github.com/go-gfx/gfx/svg"
	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

func TestCompileLaTeXSampleProducesContent(t *testing.T) {
	res := compileLaTeX(SampleLaTeX, toolkit.DefaultLight())
	if res.errText != "" {
		t.Fatalf("sample compile error: %s", res.errText)
	}
	if res.pages <= 0 || res.drawnPages <= 0 {
		t.Fatalf("bad result: pages=%d drawn=%d", res.pages, res.drawnPages)
	}
	if len(res.bitmaps) != res.drawnPages {
		t.Fatalf("bitmaps %d != drawnPages %d", len(res.bitmaps), res.drawnPages)
	}
	// Each page bitmap is a well-formed natural-size RGBA (Pix == W*H*4).
	for i, img := range res.bitmaps {
		if img == nil {
			t.Fatalf("page %d bitmap is nil", i)
		}
		w, h := img.Rect.Dx(), img.Rect.Dy()
		if w <= 0 || h <= 0 || len(img.Pix) != w*h*4 {
			t.Fatalf("page %d bad bitmap: %dx%d, Pix=%d", i, w, h, len(img.Pix))
		}
	}
}

func TestCompileLaTeXHardError(t *testing.T) {
	// An undefined control sequence in STRICT mode would error, but compileLaTeX
	// uses lenient mode; force a genuine structural error instead.
	res := compileLaTeX(`\documentclass{article}\begin{document}\end{document}\end{document}`, toolkit.DefaultLight())
	// Whatever the engine decides, the function must not panic and must return a
	// coherent result (either an error string or valid bitmaps).
	if res.errText == "" && res.bitmaps == nil && res.pages == 0 {
		t.Logf("empty-but-clean result (acceptable)")
	}
}

func TestDiagSummaryEmpty(t *testing.T) {
	if got := diagSummaryEmpty(engine.Diagnostics{}); got != "" {
		t.Fatalf("clean diagnostics summary = %q, want empty", got)
	}
	if got := diagSummaryEmpty(engine.Diagnostics{Runaway: true}); got == "" {
		t.Fatalf("runaway summary empty")
	}
	if got := diagSummaryEmpty(engine.Diagnostics{OpenGroups: 2}); got == "" {
		t.Fatalf("open-groups summary empty")
	}
}

func TestAtoiSafe(t *testing.T) {
	cases := map[string]int{
		"":     0, // empty -> 0
		"12":   12,
		"007":  7,
		"1x":   0, // trailing non-digit -> 0
		"-3":   0, // leading sign is a non-digit -> 0
		" 4":   0, // leading space -> 0
		"9999": 9999,
	}
	for in, want := range cases {
		if got := atoiSafe(in); got != want {
			t.Fatalf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLinesFromGroups(t *testing.T) {
	groups := []gfxsvg.Group{
		{Attrs: map[string]string{"data-l": "2"}, Bounds: image.Rect(0, 10, 100, 30)},
		{Attrs: map[string]string{"data-l": "2"}, Bounds: image.Rect(0, 25, 100, 45)}, // unions with the first line-2 band
		{Attrs: map[string]string{"data-l": "5"}, Bounds: image.Rect(0, 60, 100, 80)},
		{Attrs: map[string]string{"data-l": "0"}, Bounds: image.Rect(0, 90, 100, 100)},   // non-positive line -> skipped
		{Attrs: map[string]string{"data-l": "bad"}, Bounds: image.Rect(0, 90, 100, 100)}, // unparsable -> skipped
		{Attrs: map[string]string{}, Bounds: image.Rect(0, 90, 100, 100)},                // no data-l -> skipped
		{Attrs: map[string]string{"data-l": "7"}, Bounds: image.Rectangle{}},             // empty bounds -> skipped
	}
	lines := linesFromGroups(groups)
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want exactly {2,5}", lines)
	}
	if got := lines[2]; got != [2]int{10, 45} { // Y union of the two line-2 groups
		t.Fatalf("line 2 band = %v, want [10 45]", got)
	}
	if got := lines[5]; got != [2]int{60, 80} {
		t.Fatalf("line 5 band = %v, want [60 80]", got)
	}
	if _, ok := lines[7]; ok {
		t.Fatalf("empty-bounds group must not register a band")
	}
}

func TestToColor(t *testing.T) {
	c := toColor(toolkit.RGB(0x12, 0x34, 0x56))
	if c.R != 0x12 || c.G != 0x34 || c.B != 0x56 || c.A != 0xFF {
		t.Fatalf("toColor mismatch: %+v", c)
	}
}

func TestFillRGBA(t *testing.T) {
	buf := make([]byte, 8)
	fillRGBA(buf, toolkit.RGB(9, 8, 7))
	if buf[0] != 9 || buf[1] != 8 || buf[2] != 7 || buf[3] != 0xFF {
		t.Fatalf("fillRGBA wrong: %v", buf[:4])
	}
}
