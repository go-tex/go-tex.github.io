// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// TestIconPackNames lists the packs in picker order.
func TestIconPackNames(t *testing.T) {
	names := iconPackNames()
	if len(names) != len(iconPacks) || names[0] != "Seti UI" {
		t.Fatalf("iconPackNames = %v", names)
	}
}

// TestPackDrawersRender: every pack's file and folder drawers rasterise a real
// glyph (non-empty paint) through the toolkit SVG renderer.
func TestPackDrawersRender(t *testing.T) {
	const w, h = 24, 24
	box := toolkit.Rect{X: 3, Y: 3, W: 16, H: 16}
	paints := func(d iconDrawer) bool {
		buf := make([]byte, w*h*4)
		d(painter.NewPixelPainter(buf, w, h), box, toolkit.RGB(0x30, 0x30, 0x30))
		for _, b := range buf {
			if b != 0 {
				return true
			}
		}
		return false
	}
	for _, p := range iconPacks {
		if !paints(p.fileIconDrawer("paper.tex")) {
			t.Errorf("%s: .tex file drawer painted nothing", p.name)
		}
		if !paints(p.fileIconDrawer("pkg.sty")) {
			t.Errorf("%s: .sty file drawer painted nothing", p.name)
		}
		if !paints(p.folderIconDrawer()) {
			t.Errorf("%s: folder drawer painted nothing", p.name)
		}
	}
}

// TestTeXStyDistinctGlyphs: the packs that carry a config/settings glyph give a
// LaTeX package (.sty) a different icon than a LaTeX document (.tex), so the tree
// reads a .sty apart from the document it supports. Devicon has no config glyph
// and is exempt.
func TestTeXStyDistinctGlyphs(t *testing.T) {
	distinguishes := map[string]bool{"Seti UI": true, "Material": true, "VSCode Icons": true}
	for _, p := range iconPacks {
		if !distinguishes[p.name] {
			continue
		}
		if tex, sty := p.file("paper.tex"), p.file("pkg.sty"); tex == "" || sty == "" || tex == sty {
			t.Errorf("%s: .tex and .sty should be distinct glyphs", p.name)
		}
	}
}

// TestFileTreeIconCallbackCoversDirsAndFiles: the Explorer asks the host for one
// drawer per row, so the question is no longer what a built node carries but what
// that callback answers — a nil for either kind is a row drawn with no glyph.
//
// ⛔ It asks for every pack and for a file whose extension nothing special
// handles, because the per-name lookup is where a pack can return nothing.
func TestFileTreeIconCallbackCoversDirsAndFiles(t *testing.T) {
	s := newTestState(t, false)
	s.sidebar.ensureWidgets()
	icon := s.sidebar.tree.Icon
	if icon == nil {
		t.Fatal("the Explorer was given no icon callback, so every row draws the default")
	}
	for i := range iconPacks {
		s.iconPackPicker.Selected().Set(i)
		for _, c := range []struct {
			name string
			dir  bool
		}{
			{"sec", true},
			{"main.tex", false},
			{"refs.bib", false},
			{"notes.qqq", false},
		} {
			if icon(c.name, c.dir, false) == nil {
				t.Errorf("pack %d: %q (dir=%v) got no drawer", i, c.name, c.dir)
			}
		}
		// An open folder is a separate ask, and a pack that answered only the shut
		// one would lose its glyph the moment a folder is expanded.
		if icon("sec", true, true) == nil {
			t.Errorf("pack %d: an open folder got no drawer", i)
		}
	}
}

// TestIconPackIdxClamps: an out-of-range picker value clamps to the first pack.
func TestIconPackIdxClamps(t *testing.T) {
	s := newTestState(t, false)
	if s.iconPackIdx() != 0 {
		t.Fatalf("default icon pack should be 0, got %d", s.iconPackIdx())
	}
	s.iconPackPicker.Selected().Set(len(iconPacks) + 5) // out of range
	if s.iconPackIdx() != 0 {
		t.Errorf("out-of-range pack index should clamp to 0, got %d", s.iconPackIdx())
	}
}
