// Copyright (c) the go-tex/go-tex.github.io authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// A dead key is how a French keyboard writes ^: the browser reports it as a
// COMPOSITION, never as a keydown carrying the character. Nothing here listened
// for one, so the circumflex — which is how a superscript is written — could not
// be typed at all, and the space that usually forces a bare ^ arrived as a space.
func TestACompositionCommitsItsText(t *testing.T) {
	s := newTestState(t, false)
	s.editor.Text().Set("")
	before := s.editor.Text().Get()
	if !s.HandleCompositionCommit("^") {
		t.Fatal("the commit reached nothing")
	}
	if got := s.editor.Text().Get(); got != before+"^" {
		t.Errorf("editor holds %q, want %q", got, before+"^")
	}
	// A commit can carry several characters: a CJK candidate, or the ^ of a dead
	// key followed by a digit it does not combine with.
	if !s.HandleCompositionCommit("î2") {
		t.Fatal("the multi-character commit reached nothing")
	}
	if got, want := s.editor.Text().Get(), "^î2"; got != want {
		t.Errorf("editor holds %q, want %q", got, want)
	}
}

// The pending text is shown, but it is NOT in the document — and it must not
// start a recompile either: an accent half typed is not an edit.
func TestAPreviewIsNotAnEdit(t *testing.T) {
	s := newTestState(t, false)
	s.editor.Text().Set("x")
	s.TakePendingCompile() // drain the boot latch
	var scheduled int
	s.OnCompileNeeded = func() { scheduled++ }
	if !s.HandleCompositionPreview("^") {
		t.Fatal("the preview reached nothing")
	}
	if got := s.editor.Text().Get(); got != "x" {
		t.Errorf("the preview reached the document: %q", got)
	}
	if scheduled != 0 {
		t.Errorf("a preview scheduled %d compiles, want none", scheduled)
	}
	if s.TakePendingCompile() {
		t.Error("a preview latched a pending compile")
	}
	// Abandoning it leaves nothing behind.
	if !s.HandleCompositionCancel() {
		t.Fatal("the cancel reached nothing")
	}
	if got := s.editor.Text().Get(); got != "x" {
		t.Errorf("editor holds %q after a cancelled composition, want %q", got, "x")
	}
}

// A composition goes where typing goes. The find bar takes the keyboard while it
// is open, and the render pane takes it when it holds focus — in which case
// there is nothing to preview into and the preview must not fall back to the
// editor sitting behind it.
func TestACompositionFollowsTheKeyboard(t *testing.T) {
	s := newTestState(t, false)
	s.editor.Text().Set("")
	if !s.ToggleFindReplace() {
		t.Fatal("the find bar did not open")
	}
	if !s.HandleCompositionCommit("é") {
		t.Fatal("the commit reached nothing")
	}
	if got := s.fr.query.Text().Get(); !strings.Contains(got, "é") {
		t.Errorf("the find field holds %q, want it to carry é", got)
	}
	if got := s.editor.Text().Get(); got != "" {
		t.Errorf("the editor took %q while the find bar was open", got)
	}
	s.ToggleFindReplace()
	// The git panel takes the characters but cannot show them coming.
	s.git.open = true
	if _, ok := s.compositionTarget(); ok {
		t.Error("a composition previews into the editor while the git panel has the keyboard")
	}
	s.git.open = false
}
