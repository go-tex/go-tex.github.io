// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
)

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

// TestIsMarkdownDoc: .md/.markdown/.mkd are Markdown; other extensions are not.
func TestIsMarkdownDoc(t *testing.T) {
	for _, p := range []string{"README.md", "notes.MARKDOWN", "docs/x.mkd"} {
		if !isMarkdownDoc(p) {
			t.Errorf("%q should be Markdown", p)
		}
	}
	for _, p := range []string{"main.tex", "refs.bib", "pkg.sty", "notes.txt"} {
		if isMarkdownDoc(p) {
			t.Errorf("%q should NOT be Markdown", p)
		}
	}
}

// TestMarkdownLaTeX: a Markdown source converts to LaTeX (heading -> \section,
// emphasis -> \textbf) so the engine can typeset it.
func TestMarkdownLaTeX(t *testing.T) {
	tex := markdownLaTeX("# Title\n\nSome **bold** text.")
	if !strings.Contains(tex, `\section{Title}`) || !strings.Contains(tex, `\textbf{`) {
		t.Fatalf("markdownLaTeX did not convert heading/emphasis: %q", tex)
	}
}

// TestMarkdownLaTeXErrors: a Markdown source that fails to parse, or a document
// that fails to write back as LaTeX, yields "" so the render pane blanks rather
// than erroring.
func TestMarkdownLaTeXErrors(t *testing.T) {
	// Parse failure → "".
	oldP := parseMarkdown
	parseMarkdown = func([]byte) (*richdoc.Document, error) { return nil, errors.New("bad md") }
	if got := markdownLaTeX("x"); got != "" {
		t.Fatalf("parse-failure markdownLaTeX = %q, want empty", got)
	}
	parseMarkdown = oldP

	// Write failure (parse succeeds, LaTeX write fails) → "".
	oldW := writeLaTeX
	writeLaTeX = func(*richdoc.Document) ([]byte, error) { return nil, errors.New("bad write") }
	if got := markdownLaTeX("# ok"); got != "" {
		t.Fatalf("write-failure markdownLaTeX = %q, want empty", got)
	}
	writeLaTeX = oldW
}

// TestMarkdownTabRenders: opening a Markdown README renders it as a document — the
// render pane shows pages (not blank), because compileSource converts it to LaTeX.
func TestMarkdownTabRenders(t *testing.T) {
	s, f := withClonedSidebar(t)
	f.fileData["README.md"] = "# go-tex\n\nA **pure-Go** LaTeX engine.\n\n- fast\n- offline\n"

	if !s.GitOpenFile("README.md") {
		t.Fatal("open README.md")
	}
	// compileSource yields converted LaTeX (a section from the heading).
	if src := s.git.compileSource(); !strings.Contains(src, `\section{go-tex}`) {
		t.Fatalf("README.md not converted to LaTeX: %q", src)
	}
	// Opening it recompiled (SetSource -> Compile); the README renders to pages.
	if s.drawnPages == 0 || s.errText != "" {
		t.Fatalf("README.md should render: drawn=%d err=%q", s.drawnPages, s.errText)
	}
	if s.RenderVisiblePages() == 0 {
		t.Fatal("render pane should show the README's pages, got 0")
	}
}
