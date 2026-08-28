// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// TestIsRenderableDoc: only a .tex/.ltx document is renderable; packages, classes,
// databases and prose are not.
func TestIsRenderableDoc(t *testing.T) {
	for _, p := range []string{"main.tex", "paper.TEX", "chapters/intro.tex", "doc.ltx"} {
		if !isRenderableDoc(p) {
			t.Errorf("%q should be a renderable document", p)
		}
	}
	for _, p := range []string{"refs.bib", "pkg.sty", "book.cls", "README.md", "notes.txt", "Makefile", "noext"} {
		if isRenderableDoc(p) {
			t.Errorf("%q should NOT be a renderable document", p)
		}
	}
}

// TestNonDocumentTabBlanksRender: opening a non-document file (a .sty/.bib/.md)
// blanks the render pane — no pages, no svgs, no error — rather than compiling its
// non-LaTeX content or falling back to another file.
func TestNonDocumentTabBlanksRender(t *testing.T) {
	s, _ := withClonedSidebar(t) // main.tex active

	// Opening a .bib routes through SetSource -> Compile; compileSource is "" for a
	// non-document, so Compile blanks the render pane.
	if !s.GitOpenFile("refs.bib") {
		t.Fatal("open refs.bib")
	}
	if s.pages != 0 || s.drawnPages != 0 || len(s.svgs) != 0 || s.errText != "" {
		t.Fatalf("non-document tab should blank the render: pages=%d drawn=%d svgs=%d err=%q",
			s.pages, s.drawnPages, len(s.svgs), s.errText)
	}
	if s.RenderVisiblePages() != 0 {
		t.Fatalf("render pane should show 0 pages for a non-document, got %d", s.RenderVisiblePages())
	}
	// The status bar must not read "compile error" — it is simply not a document.
	if s.status.Segments[2] == "compile error" {
		t.Fatalf("a non-document tab should not read a compile error, got %q", s.status.Segments[2])
	}

	// Switching back to a .tex renders again (non-empty source recompiles).
	if !s.GitOpenFile("main.tex") {
		t.Fatal("re-open main.tex")
	}
	if s.git.compileSource() == "" {
		t.Fatal("main.tex should compile a non-empty source again")
	}
}
