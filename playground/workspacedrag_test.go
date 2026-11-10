// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"slices"
	"strings"
	"testing"
)

// Dragging a file row onto a folder moves the file — the gesture a file manager
// is expected to have, and the last of the Explorer's verbs that had no route
// into this app.
//
// ⛔ An inert drag is NOT the same as an absent one. The tree outlines the
// folder under the pointer as a drop target while the row is held, so a column
// that drew that invitation and then did nothing on release would be worse than
// one that never offered it.
func TestDraggingAFileOntoAFolderMovesIt(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{
		"main.tex":           "BODY",
		"chapters/intro.tex": "INTRO",
	}, "main.tex")
	s.layout()
	s.Draw(make([]byte, testW*testH*4))

	rows := s.SidebarFileRows()
	want := []string{"chapters/", "intro.tex", "main.tex"}
	if strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	tr := s.sidebar.treeRect
	from := treeRowY(tr, 2) // main.tex
	onto := treeRowY(tr, 0) // the chapters/ folder
	x := tr.X + tr.W/2

	// Press on the file, drag onto the folder, release there — the real sequence.
	s.HandleClick(x, from)
	s.HandleMove(x, (from+onto)/2)
	s.HandleMove(x, onto)
	s.HandleRelease(x, onto)

	if f.gotWrites["chapters/main.tex"] != "BODY" {
		t.Fatalf("the move did not write the new path: %v", f.gotWrites)
	}
	if !slices.Contains(f.removed, "main.tex") {
		t.Errorf("the old path was not removed: %v", f.removed)
	}
	if got := s.git.loaded.Get(); got != "chapters/main.tex" {
		t.Errorf("the moved file is not the active one: %q", got)
	}
}

// A press that goes nowhere is a plain click and must not move anything: a file
// opened by clicking it would otherwise be at the mercy of a trembling hand.
func TestAPressWithoutATravelDoesNotMoveAnything(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{
		"main.tex":           "BODY",
		"chapters/intro.tex": "INTRO",
	}, "chapters/intro.tex")
	s.layout()
	s.Draw(make([]byte, testW*testH*4))

	tr := s.sidebar.treeRect
	x, y := tr.X+tr.W/2, treeRowY(tr, 2) // main.tex
	s.HandleClick(x, y)
	s.HandleRelease(x, y)

	if f.removeCalls != 0 || len(f.gotWrites) != 0 {
		t.Fatalf("a click moved something (%d removes, writes %v)", f.removeCalls, f.gotWrites)
	}
	// It opened the file, which is what a click on a row is for.
	if got := s.git.loaded.Get(); got != "main.tex" {
		t.Errorf("the click did not open the row: %q", got)
	}
}
