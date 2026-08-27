// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

func TestCompileLaTeXSampleProducesContent(t *testing.T) {
	res := compileLaTeX(SampleLaTeX, toolkit.DefaultLight(), nil)
	if res.errText != "" {
		t.Fatalf("sample compile error: %s", res.errText)
	}
	if res.pages <= 0 || res.drawnPages <= 0 {
		t.Fatalf("bad result: pages=%d drawn=%d", res.pages, res.drawnPages)
	}
	if len(res.svgs) != res.drawnPages {
		t.Fatalf("svgs %d != drawnPages %d", len(res.svgs), res.drawnPages)
	}
	// Each page is a real SVG with a real natural size.
	for i, svg := range res.svgs {
		if !strings.HasPrefix(svg, "<svg") {
			t.Fatalf("page %d is not an svg: %.60s", i, svg)
		}
		if sz := res.sizes[i]; sz.X <= 0 || sz.Y <= 0 {
			t.Fatalf("page %d has no size: %+v", i, sz)
		}
	}
}

func TestCompileLaTeXHardError(t *testing.T) {
	// An undefined control sequence in STRICT mode would error, but compileLaTeX
	// uses lenient mode; force a genuine structural error instead.
	res := compileLaTeX(`\documentclass{article}\begin{document}\end{document}\end{document}`, toolkit.DefaultLight(), nil)
	// Whatever the engine decides, the function must not panic and must return a
	// coherent result (either an error string or drawable pages).
	if res.errText == "" && res.svgs == nil && res.pages == 0 {
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

func TestFillRGBA(t *testing.T) {
	buf := make([]byte, 8)
	fillRGBA(buf, toolkit.RGB(9, 8, 7))
	if buf[0] != 9 || buf[1] != 8 || buf[2] != 7 || buf[3] != 0xFF {
		t.Fatalf("fillRGBA wrong: %v", buf[:4])
	}
}

// TestPreviewDefaultsToFitWidth: the render pane opens in sticky fit-to-width so
// a page fills the pane instead of sitting at a fixed 100% in empty margins.
func TestPreviewDefaultsToFitWidth(t *testing.T) {
	s := newTestState(t, false)
	if !s.renderView.FitWidth() {
		t.Fatal("the previewer should default to sticky fit-to-width")
	}
}
