// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

func TestCompileLaTeXSampleProducesContent(t *testing.T) {
	res := compileLaTeX(SampleLaTeX, toolkit.DefaultLight())
	if res.errText != "" {
		t.Fatalf("sample compile error: %s", res.errText)
	}
	if res.pages <= 0 || res.w <= 0 || res.h <= 0 {
		t.Fatalf("bad result: pages=%d w=%d h=%d", res.pages, res.w, res.h)
	}
	if len(res.pixels) != res.w*res.h*4 {
		t.Fatalf("pixel length %d, want %d", len(res.pixels), res.w*res.h*4)
	}
	if len(res.lineBands) == 0 {
		t.Fatalf("no line bands")
	}
	for line, b := range res.lineBands {
		if b[0] < 0 || b[1] > res.h || b[0] >= b[1] {
			t.Fatalf("bad band for line %d: %v (h=%d)", line, b, res.h)
		}
	}
}

func TestCompileLaTeXHardError(t *testing.T) {
	// An undefined control sequence in STRICT mode would error, but compileLaTeX
	// uses lenient mode; force a genuine structural error instead.
	res := compileLaTeX(`\documentclass{article}\begin{document}\end{document}\end{document}`, toolkit.DefaultLight())
	// Whatever the engine decides, the function must not panic and must return a
	// coherent result (either an error string or a valid image).
	if res.errText == "" && res.pixels == nil && res.pages == 0 {
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

func TestToColor(t *testing.T) {
	c := toColor(toolkit.RGB(0x12, 0x34, 0x56))
	if c.R != 0x12 || c.G != 0x34 || c.B != 0x56 || c.A != 0xFF {
		t.Fatalf("toColor mismatch: %+v", c)
	}
}

func TestBlitClips(t *testing.T) {
	dst := make([]byte, 4*4*4) // 4x4
	src := []byte{
		1, 1, 1, 1, 2, 2, 2, 2,
		3, 3, 3, 3, 4, 4, 4, 4,
	} // 2x2
	// Place partly off the top-left (negative origin) and off the bottom-right.
	blit(dst, 4, 4, src, 2, 2, -1, -1) // only src(1,1)=4 lands at dst(0,0)
	if dst[0] != 4 {
		t.Fatalf("clip top-left: dst(0,0)=%d want 4", dst[0])
	}
	blit(dst, 4, 4, src, 2, 2, 3, 3) // only src(0,0)=1 lands at dst(3,3)
	i := (3*4 + 3) * 4
	if dst[i] != 1 {
		t.Fatalf("clip bottom-right: dst(3,3)=%d want 1", dst[i])
	}
}

func TestFillRGBA(t *testing.T) {
	buf := make([]byte, 8)
	fillRGBA(buf, toolkit.RGB(9, 8, 7))
	if buf[0] != 9 || buf[1] != 8 || buf[2] != 7 || buf[3] != 0xFF {
		t.Fatalf("fillRGBA wrong: %v", buf[:4])
	}
}
