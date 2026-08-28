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
		if !paints(p.folderIconDrawer()) {
			t.Errorf("%s: folder drawer painted nothing", p.name)
		}
	}
}

// TestBuildFileTreeSetsPackIcons: dirs get the folder drawer, files a per-name
// drawer, both non-nil, so every row renders a glyph.
func TestBuildFileTreeSetsPackIcons(t *testing.T) {
	pack := iconPacks[0]
	roots, _ := buildFileTree([]string{"main.tex", "sec/intro.tex"},
		func(string) (string, toolkit.RGBA) { return "", toolkit.RGBA{} },
		pack.fileIconDrawer, pack.folderIconDrawer())
	var sawDir, sawFile bool
	var walk func(n *toolkit.TreeTableNode)
	walk = func(n *toolkit.TreeTableNode) {
		if n.Icon == nil {
			t.Errorf("node %q has no icon", n.Cells[0])
		}
		if len(n.Children) > 0 {
			sawDir = true
			for _, c := range n.Children {
				walk(c)
			}
			return
		}
		sawFile = true
	}
	for _, r := range roots {
		walk(r)
	}
	if !sawDir || !sawFile {
		t.Fatalf("want both a dir and a file node (dir=%v file=%v)", sawDir, sawFile)
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
