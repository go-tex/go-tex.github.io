// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// A secondary click on a file row opens the Explorer's menu — the gesture that
// was discarded by the host until now, which is why New File, New Folder,
// Rename and Delete were keyboard-only.
func TestSecondaryClickOpensTheTreeMenu(t *testing.T) {
	s, _ := withClonedSidebar(t)
	tr := s.sidebar.treeRect
	if !s.HandleContextClick(tr.X+tr.W/2, treeRowY(tr, 3)) {
		t.Fatal("a secondary click on a file row was not taken")
	}
	if !s.sidebar.tree.MenuOpen() {
		t.Fatal("the context menu did not open")
	}
	// It also takes the keyboard, so Escape and the arrows reach the menu.
	if !s.sidebar.tree.Focused() {
		t.Error("the column did not take focus with the menu")
	}
}

// ⛔ Outside the regions that use one, the app must NOT take the gesture — the
// host suppresses the page's own menu only when this returns true, and a page
// that swallows every right-click stops being a page: that is how a reader
// copies text or opens a link in a new tab.
func TestSecondaryClickElsewhereIsLeftToTheBrowser(t *testing.T) {
	s, _ := withClonedSidebar(t)
	er := s.editor.Bounds()
	if s.HandleContextClick(er.X+er.W/2, er.Y+er.H/2) {
		t.Error("a secondary click in the editor was taken by the app")
	}
	// And below the tree, inside the column but not on it.
	b := s.sidebar.bounds
	if s.HandleContextClick(b.X+b.W/2, b.Y+2) {
		t.Error("a secondary click on the column's command band was taken")
	}
	if s.sidebar.menuOpen() {
		t.Error("a menu opened for a click that was not taken")
	}
}

// With no repository there is no tree, so nothing answers.
func TestSecondaryClickWithoutARepositoryIsNotTaken(t *testing.T) {
	s := newTestState(t, false)
	s.sidebar.ensureWidgets()
	b := s.sidebar.bounds
	if s.HandleContextClick(b.X+b.W/2, b.Y+b.H/2) {
		t.Error("a secondary click was taken with no repository open")
	}
}

// ⛔ A REPOSITORY APPEARING IS A GEOMETRY CHANGE. With no repo the column lays
// out an empty state and zeroes the tree, timeline and detail rects; with one it
// lays out a command band over two accordion bodies. Only BootClone re-laid out,
// so a clone started from the Remote-Git panel left the file tree with a ZERO
// rect — invisible and unclickable until a resize or a sidebar toggle happened
// to lay it out again.
//
// Found by a browser proof that right-clicked the tree and was handed
// treeRect [0,0,0,0] a moment after the clone succeeded.
func TestCloningThroughThePanelLaysTheTreeOut(t *testing.T) {
	s, f, _ := withGit(t)
	s.layout()
	if r := s.SidebarTreeRect(); r[3] != 0 {
		t.Fatalf("this test needs a column with no repository; tree rect = %v", r)
	}
	// Clone the way the panel's button does, not the way BootClone does.
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = "BODY"
	var gotErr error
	s.GitClone(func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("clone: %v", gotErr)
	}
	// A frame goes by: the app draws, which is all the real one does here.
	s.Draw(make([]byte, testW*testH*4))
	r := s.SidebarTreeRect()
	if r[2] <= 0 || r[3] <= 0 {
		t.Fatalf("after cloning through the panel the file tree has rect %v — invisible and unclickable", r)
	}
	// And a secondary click on it now reaches the Explorer.
	if !s.HandleContextClick(r[0]+r[2]/2, r[1]+4) {
		t.Error("a secondary click on the freshly laid-out tree was not taken")
	}
}

// The click that dismisses an open menu is swallowed: the gesture that closes a
// menu does not also act on what is underneath it.
func TestTheClickThatClosesTheMenuDoesNotAlsoEdit(t *testing.T) {
	s, _ := withClonedSidebar(t)
	tr := s.sidebar.treeRect
	s.HandleContextClick(tr.X+tr.W/2, treeRowY(tr, 3))
	if !s.sidebar.menuOpen() {
		t.Fatal("the menu did not open")
	}
	before := s.Source()
	er := s.editor.Bounds()
	if !s.HandleClick(er.X+er.W/2, er.Y+er.H/2) {
		t.Fatal("the dismissing click was not consumed")
	}
	if s.sidebar.menuOpen() {
		t.Error("the menu survived a click outside the column")
	}
	if s.Source() != before {
		t.Error("the dismissing click also reached the editor")
	}
}
