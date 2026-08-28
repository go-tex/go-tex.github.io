// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// TestHighlightLangFollowsActiveFile: the editor's syntax language is chosen from
// the active file's extension — LaTeX for the .tex family and the sample, Markdown
// for .md/.markdown/.mkd (case-insensitively), plaintext for anything else.
func TestHighlightLangFollowsActiveFile(t *testing.T) {
	s := newTestState(t, false)
	cases := map[string]string{
		"":               "latex", // the sample document / empty editor
		"README.md":      "markdown",
		"notes.MARKDOWN": "markdown",
		"docs/x.mkd":     "markdown",
		"main.tex":       "latex",
		"sty/pkg.sty":    "latex",
		"book.cls":       "latex",
		"refs.bib":       "latex",
		"data.json":      "plaintext",
		"Makefile":       "plaintext",
	}
	for p, want := range cases {
		s.git.loaded.Set(p)
		if got := s.highlightLang(); got != want {
			t.Errorf("highlightLang(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestLoadSetsEditorLanguage: loading a file through SetSource / SetSourceCursor
// sets the editor's lexer language to match, so a Markdown README highlights as
// Markdown and a switch back to a .tex restores LaTeX.
func TestLoadSetsEditorLanguage(t *testing.T) {
	s := newTestState(t, false)

	s.git.loaded.Set("README.md")
	s.SetSource("# Title\n\nSome **bold** text.")
	if s.editor.Language != "markdown" {
		t.Fatalf("after SetSource(.md) editor.Language = %q, want markdown", s.editor.Language)
	}

	s.git.loaded.Set("main.tex")
	s.SetSourceCursor("\\documentclass{article}", 0, 0, 0)
	if s.editor.Language != "latex" {
		t.Fatalf("after SetSourceCursor(.tex) editor.Language = %q, want latex", s.editor.Language)
	}
}
