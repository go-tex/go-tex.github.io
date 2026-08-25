// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// The overlay keeps the structure the text is positioned by and drops everything
// the canvas already draws.
func TestTextOverlaySVGKeepsTextAndStructure(t *testing.T) {
	page := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 200">` +
		`<rect width="100%" height="100%" fill="white"/><g fill="black">` +
		`<g data-l="3"><text x="1" y="2" fill-opacity="0"><tspan x="1">ab</tspan> <tspan x="9">cd</tspan></text>` +
		`<path d="M0 0"/></g>` +
		`<image x="0" y="0" width="4" height="4" href="data:,"/>` +
		`</g></svg>`

	got := textOverlaySVG(page)
	for _, want := range []string{`viewBox="0 0 100 200"`, `<g data-l="3">`, `<tspan x="9">cd</tspan>`, `</svg>`} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay lost %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"<path", "<rect", "<image"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("overlay kept a drawing element %q:\n%s", unwanted, got)
		}
	}
	if len(got) >= len(page) {
		t.Errorf("overlay (%d B) must be smaller than the page (%d B)", len(got), len(page))
	}
}

// A page with nothing to say gets no element, and input that is not the engine's
// output is refused rather than half-copied.
func TestTextOverlaySVGRefusals(t *testing.T) {
	cases := map[string]string{
		"no text":           `<svg viewBox="0 0 1 1"><path d="M0 0"/></svg>`,
		"not an svg":        `<html><text>hello</text></html>`,
		"empty":             ``,
		"unterminated tag":  `<svg viewBox="0 0 1 1"><text x="1"`,
		"unterminated text": `<svg viewBox="0 0 1 1"><text x="1">a`,
	}
	for name, in := range cases {
		if got := textOverlaySVG(in); got != "" {
			t.Errorf("%s: expected no overlay, got %q", name, got)
		}
	}
}

func TestElementName(t *testing.T) {
	cases := map[string]string{
		`<svg xmlns="x">`:  "svg",
		`</g>`:             "/g",
		`<path d="M0 0"/>`: "path",
		`<text>`:           "text",
		`<>`:               "",
	}
	for in, want := range cases {
		if got := elementName(in); got != want {
			t.Errorf("elementName(%q) = %q, want %q", in, got, want)
		}
	}
}

// End-to-end over the real engine: a compiled document yields an overlay per
// page whose text is the document's words.
func TestCompileProducesSearchableText(t *testing.T) {
	res := compileLaTeX(`\documentclass{article}\begin{document}Findable words here.\end{document}`, toolkit.DefaultLight())
	if len(res.bitmaps) == 0 {
		t.Fatalf("no page rendered: %q", res.errText)
	}
	if len(res.textLayers) != len(res.bitmaps) {
		t.Fatalf("%d text layers for %d bitmaps — they must stay parallel",
			len(res.textLayers), len(res.bitmaps))
	}
	if !strings.Contains(stripTags(res.textLayers[0]), "Findable words here.") {
		t.Errorf("the page's words are not in its overlay:\n%s", res.textLayers[0])
	}
}

// stripTags leaves the character data of an SVG fragment, the way a browser's
// text content reads it.
func stripTags(s string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			return out.String()
		}
		s = s[i+j+1:]
	}
}

// The root <svg> is re-fitted so the host can size it: the pt width/height are
// gone and the viewBox is pinned to fill the box exactly.
func TestOverlayRootIsRefitted(t *testing.T) {
	page := `<svg xmlns="http://www.w3.org/2000/svg" width="489pt" height="787.2pt" viewBox="0 0 489 787.2">` +
		`<text x="1" y="2">a</text></svg>`
	got := textOverlaySVG(page)
	if strings.Contains(got, `width="489pt"`) || strings.Contains(got, `height="787.2pt"`) {
		t.Errorf("the point size must go, the host sizes the overlay: %s", got)
	}
	if !strings.Contains(got, `viewBox="0 0 489 787.2"`) {
		t.Errorf("the viewBox must stay, it is what maps runs to glyphs: %s", got)
	}
	if !strings.Contains(got, `preserveAspectRatio="none"`) {
		t.Errorf("without it a rounding difference letterboxes every run off its glyphs: %s", got)
	}
}

func TestDropAttr(t *testing.T) {
	if got := dropAttr(`<svg width="4" height="5">`, "width"); got != `<svg height="5">` {
		t.Errorf("dropAttr = %q", got)
	}
	if got := dropAttr(`<svg height="5">`, "width"); got != `<svg height="5">` {
		t.Errorf("a missing attribute must leave the tag alone: %q", got)
	}
	if got := dropAttr(`<svg width="4`, "width"); got != `<svg width="4` {
		t.Errorf("an unterminated value must leave the tag alone: %q", got)
	}
}
