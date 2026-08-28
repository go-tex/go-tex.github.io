// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// TestFileTypeInk covers every extension class and the extensionless / unknown
// fallbacks.
func TestFileTypeInk(t *testing.T) {
	for _, tc := range []struct {
		name string
		want toolkit.RGBA
	}{
		{"paper.tex", inkTeX},
		{"beamer.sty", inkTeX},
		{"logo.png", inkImage},
		{"fig.pdf", inkImage},
		{"main.go", inkCode},
		{"run.sh", inkCode},
		{"data.json", inkData},
		{"conf.yaml", inkData},
		{"README", inkDoc},    // no extension
		{"notes.txt", inkDoc}, // unknown extension
	} {
		if got := fileTypeInk(tc.name); got != tc.want {
			t.Errorf("fileTypeInk(%q) = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestBuildFileTreeSetsIcons: directories get the folder icon and files get a
// (non-nil) type icon, so the tree renders a leading glyph per row.
func TestBuildFileTreeSetsIcons(t *testing.T) {
	roots, _ := buildFileTree([]string{"main.tex", "sections/intro.tex"}, func(string) (string, toolkit.RGBA) { return "", toolkit.RGBA{} })
	var sawDir, sawFile bool
	var walk func(n *toolkit.TreeTableNode)
	walk = func(n *toolkit.TreeTableNode) {
		if len(n.Children) > 0 {
			if n.Icon == nil {
				t.Errorf("directory %q has no icon", n.Cells[0])
			}
			sawDir = true
			for _, c := range n.Children {
				walk(c)
			}
			return
		}
		if n.Icon == nil {
			t.Errorf("file %q has no icon", n.Cells[0])
		}
		sawFile = true
	}
	for _, r := range roots {
		walk(r)
	}
	if !sawDir || !sawFile {
		t.Fatalf("expected both a dir and a file node (dir=%v file=%v)", sawDir, sawFile)
	}
}
