// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// ⛔ THE RULE THIS FILE EXISTS FOR, first: a Delete pressed while writing must
// never reach a file.
//
// The Explorer's keys act on the SELECTED entry — Delete asks to delete it, F2
// renames it — so routing them without a focus gate would turn an ordinary
// keystroke in the editor into a deletion. The gate is that the column must hold
// the keyboard, which it takes by being clicked and loses on any click
// elsewhere.
func TestDeleteInTheEditorNeverReachesAFile(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// Give the tree focus and a selection, the way clicking a row does.
	s.sidebar.focusTree()
	s.sidebar.tree.Selected().Set("refs.bib")

	// Now click into the editor, as a person does to write.
	er := s.editor.Bounds()
	s.HandleClick(er.X+er.W/2, er.Y+er.H/2)
	if s.sidebar.tree.Focused() {
		t.Fatal("the file tree kept the keyboard after a click in the editor")
	}
	if !s.HandleKeyDown("Delete") {
		t.Fatal("the editor did not take the Delete")
	}
	if s.sidebar.tree.Confirming() != "" {
		t.Fatalf("Delete in the editor asked to delete %q", s.sidebar.tree.Confirming())
	}
	// And the file is still listed.
	if !strings.Contains(strings.Join(s.SidebarFileRows(), "|"), "refs.bib") {
		t.Fatal("refs.bib left the workspace")
	}
}

// With the column focused, the same key asks — and only asks. A delete that went
// through on the keypress would be a file lost to a mistyped keystroke, so the
// widget's confirmation is part of the contract being checked here.
func TestDeleteInTheFocusedTreeAsksFirst(t *testing.T) {
	s, f := withClonedSidebar(t)
	s.sidebar.focusTree()
	s.sidebar.tree.Selected().Set("refs.bib")

	if !s.HandleKeyDown("Delete") {
		t.Fatal("the focused tree did not take the Delete")
	}
	if got := s.sidebar.tree.Confirming(); got != "refs.bib" {
		t.Fatalf("Confirming() = %q, want refs.bib", got)
	}
	if f.removeCalls != 0 {
		t.Errorf("the file was removed before anyone confirmed (%d calls)", f.removeCalls)
	}
	// Enter confirms, and only then does the backend hear about it.
	if !s.HandleKeyDown("Enter") {
		t.Fatal("the confirmation did not take the Enter")
	}
	if f.removeCalls != 1 {
		t.Fatalf("after confirming, removeCalls = %d, want 1", f.removeCalls)
	}
	if got := strings.Join(f.removed, ","); got != "refs.bib" {
		t.Errorf("removed %q", got)
	}
}

// F2 opens the in-place rename on the selected entry, which is what makes the
// name editable at all.
func TestF2OpensTheRename(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.focusTree()
	s.sidebar.tree.Selected().Set("refs.bib")
	if !s.HandleKeyDown("F2") {
		t.Fatal("the focused tree did not take the F2")
	}
	if !s.sidebar.tree.Editing() {
		t.Fatal("F2 did not open an in-place rename")
	}
	// Escape abandons it, leaving the name alone.
	s.HandleKeyDown("Escape")
	if s.sidebar.tree.Editing() {
		t.Fatal("Escape did not close the rename")
	}
}

// ⛔ Shift+F10 and the Menu key are the KEYBOARD route to the context menu, and
// on this app they are the ONLY route: the wasm host drops every mouse event
// whose button is not 0 and listens for no contextmenu, so New File, New Folder,
// Rename and Delete are reachable through these two keys or not at all.
func TestTheContextMenuIsReachableFromTheKeyboard(t *testing.T) {
	for _, key := range []string{"Shift+F10", "ContextMenu"} {
		s, _ := withClonedSidebar(t)
		s.sidebar.focusTree()
		s.sidebar.tree.Selected().Set("main.tex")
		if !s.HandleKeyDown(key) {
			t.Fatalf("%s was not taken", key)
		}
		if !s.sidebar.tree.MenuOpen() {
			t.Fatalf("%s did not open the context menu", key)
		}
	}
}

// Type-ahead moves the selection, which is the APG treeview behaviour and the
// reason characters are routed at all.
func TestTypeAheadMovesTheSelection(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.focusTree()
	s.sidebar.tree.Selected().Set("main.tex")
	if !s.HandleChar("r") {
		t.Fatal("the focused tree did not take the character")
	}
	if got := s.sidebar.tree.Selected().Get(); got != "refs.bib" {
		t.Fatalf("type-ahead selected %q, want refs.bib", got)
	}
}

// And with the column unfocused, the same character types into the editor
// instead — the other half of the gate.
func TestTypingGoesToTheEditorWhenTheTreeIsNotFocused(t *testing.T) {
	s, _ := withClonedSidebar(t)
	before := s.Source()
	er := s.editor.Bounds()
	s.HandleClick(er.X+er.W/2, er.Y+er.H/2)
	if !s.HandleChar("r") {
		t.Fatal("the editor did not take the character")
	}
	if s.Source() == before {
		t.Fatal("the character reached neither the editor nor the tree")
	}
}

// Focus is dropped with any name being typed or deletion awaiting an answer. An
// in-place rename left open while the person works in the editor would catch the
// next Enter they press there.
func TestLeavingTheColumnAbandonsAnOpenRename(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.focusTree()
	s.sidebar.tree.Selected().Set("refs.bib")
	s.HandleKeyDown("F2")
	if !s.sidebar.tree.Editing() {
		t.Fatal("F2 did not open a rename")
	}
	er := s.editor.Bounds()
	s.HandleClick(er.X+er.W/2, er.Y+er.H/2)
	if s.sidebar.tree.Editing() {
		t.Fatal("the rename survived a click in the editor")
	}
}

// A key offered while the column has no repository, or is closed, is not taken —
// so the chain below it still runs.
func TestKeysAreNotTakenWithoutARepository(t *testing.T) {
	s := newTestState(t, false)
	s.sidebar.ensureWidgets()
	s.sidebar.tree.SetFocused(true) // even focused, there is nothing to act on
	if s.sidebar.handleKey("Delete") {
		t.Error("a Delete was consumed with no repository open")
	}
	if s.sidebar.handleChar("r") {
		t.Error("a character was consumed with no repository open")
	}
}
