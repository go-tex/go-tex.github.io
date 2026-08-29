// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// TestWysiwygLaTeXOnly: WYSIWYG is offered only for a LaTeX document. On a
// Markdown tab it is unavailable and toggling it is a no-op — the Markdown source
// is NOT parsed as LaTeX and written back (the reported corruption bug).
func TestWysiwygLaTeXOnly(t *testing.T) {
	s, f := withClonedSidebar(t)
	f.fileData["README.md"] = "# Title\n\nSome **bold** text.\n"

	// The sample / a .tex document offers WYSIWYG.
	if !s.wysiwyg().available() {
		t.Fatal("WYSIWYG should be available on main.tex")
	}

	// Open the Markdown README: WYSIWYG is withheld.
	if !s.GitOpenFile("README.md") {
		t.Fatal("open README.md")
	}
	if s.wysiwyg().available() {
		t.Fatal("WYSIWYG must be unavailable on a Markdown tab")
	}

	// Toggling does nothing — crucially, the Markdown source is unchanged (not
	// converted to LaTeX).
	before := s.Source()
	s.ToggleWysiwyg()
	if s.WysiwygActive() {
		t.Fatal("WYSIWYG must not activate on a Markdown tab")
	}
	if s.Source() != before {
		t.Fatalf("Markdown source was altered by the toggle:\n%q\n->\n%q", before, s.Source())
	}

	// The toolbar toggle button is disabled on the Markdown tab.
	s.Draw(make([]byte, testW*testH*4))
	if !s.wysiwygBtn.Disabled().Get() {
		t.Fatal("the view toggle button should be disabled on a Markdown tab")
	}

	// Back on a .tex, it is available and enabled again.
	if !s.GitOpenFile("main.tex") {
		t.Fatal("re-open main.tex")
	}
	if !s.wysiwyg().available() {
		t.Fatal("WYSIWYG should be available again on main.tex")
	}
	s.Draw(make([]byte, testW*testH*4))
	if s.wysiwygBtn.Disabled().Get() {
		t.Fatal("the view toggle button should be enabled on a .tex tab")
	}
}

// TestWysiwygDropsToSourceOnFileSwitch: switching files while in WYSIWYG drops back
// to Source (writing the current file's edits back first), so a switch never leaves
// a stale RichEditor over the new file.
func TestWysiwygDropsToSourceOnFileSwitch(t *testing.T) {
	s, _ := withClonedSidebar(t) // main.tex active
	s.SetSource("\\section{Intro}\n\nA paragraph.")
	s.ToggleWysiwyg()
	if !s.WysiwygActive() {
		t.Fatal("should be in WYSIWYG on main.tex")
	}

	// Switch to another .tex: WYSIWYG drops to Source automatically.
	if !s.GitOpenFile("chapters/ch1.tex") {
		t.Fatal("open chapters/ch1.tex")
	}
	if s.WysiwygActive() {
		t.Fatal("switching files should drop WYSIWYG back to Source")
	}
	if s.Source() != "CH1" {
		t.Fatalf("new file did not load in Source: %q", s.Source())
	}
}
