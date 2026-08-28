// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// tabsJoined is the open editor tabs as a "|"-joined string, for terse assertions.
func tabsJoined(s *State) string { return strings.Join(s.GitOpenTabs(), "|") }

// TestFileTabsOpenAndActivate: cloning opens the primary as the first tab;
// opening more files appends tabs and activates them; re-opening an open file
// does not duplicate its tab and just re-activates it.
func TestFileTabsOpenAndActivate(t *testing.T) {
	s, _ := withClonedSidebar(t) // main.tex is the primary, loaded

	if got := tabsJoined(s); got != "main.tex" {
		t.Fatalf("after clone, tabs = %q, want main.tex", got)
	}
	if s.GitActiveTabIndex() != 0 {
		t.Fatalf("active tab index = %d, want 0", s.GitActiveTabIndex())
	}

	// Open two more files: each appends a tab and becomes active.
	if !s.GitOpenFile("chapters/intro.tex") {
		t.Fatal("open intro.tex")
	}
	if !s.GitOpenFile("chapters/ch1.tex") {
		t.Fatal("open ch1.tex")
	}
	if got := tabsJoined(s); got != "main.tex|chapters/intro.tex|chapters/ch1.tex" {
		t.Fatalf("tabs = %q", got)
	}
	if s.GitActiveTabIndex() != 2 {
		t.Fatalf("active tab index = %d, want 2 (ch1.tex)", s.GitActiveTabIndex())
	}

	// Re-open an already-open file: no duplicate tab, just re-activation.
	if !s.GitOpenFile("main.tex") {
		t.Fatal("re-open main.tex")
	}
	if got := tabsJoined(s); got != "main.tex|chapters/intro.tex|chapters/ch1.tex" {
		t.Fatalf("re-open duplicated a tab: %q", got)
	}
	if s.GitActiveTabIndex() != 0 {
		t.Fatalf("re-open active index = %d, want 0", s.GitActiveTabIndex())
	}
}

// TestFileTabsCloseInactive: closing a non-active tab removes it and leaves the
// active file unchanged.
func TestFileTabsCloseInactive(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex") // active = intro.tex; tabs = main|intro

	s.GitCloseTab("main.tex") // close the inactive tab
	if got := tabsJoined(s); got != "chapters/intro.tex" {
		t.Fatalf("after closing inactive, tabs = %q", got)
	}
	if s.GitLoadedPath() != "chapters/intro.tex" {
		t.Fatalf("active file changed to %q after closing an inactive tab", s.GitLoadedPath())
	}
}

// TestFileTabsCloseActiveActivatesNeighbour: closing the active tab activates the
// tab to its left (or the new first tab).
func TestFileTabsCloseActiveActivatesNeighbour(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex")
	s.GitOpenFile("chapters/ch1.tex") // tabs = main|intro|ch1, active = ch1

	s.GitCloseTab("chapters/ch1.tex") // close active → neighbour (intro) activates
	if got := tabsJoined(s); got != "main.tex|chapters/intro.tex" {
		t.Fatalf("tabs = %q after closing active", got)
	}
	if s.GitLoadedPath() != "chapters/intro.tex" {
		t.Fatalf("neighbour not activated: active = %q, want intro.tex", s.GitLoadedPath())
	}

	// Close the first tab while it is NOT active (active is intro at idx 1): the
	// first tab (main) goes, intro stays active and shifts to index 0.
	s.GitOpenFile("main.tex") // active = main (idx 0)
	s.GitCloseTab("main.tex") // close active first tab → new first (intro) activates
	if got := tabsJoined(s); got != "chapters/intro.tex" {
		t.Fatalf("tabs = %q after closing the first active tab", got)
	}
	if s.GitLoadedPath() != "chapters/intro.tex" {
		t.Fatalf("after closing first tab, active = %q, want intro.tex", s.GitLoadedPath())
	}
}

// TestFileTabsCloseLast: closing the only tab clears the editor to an empty,
// path-less buffer.
func TestFileTabsCloseLast(t *testing.T) {
	s, _ := withClonedSidebar(t) // one tab: main.tex
	s.GitCloseTab("main.tex")
	if got := tabsJoined(s); got != "" {
		t.Fatalf("tabs = %q after closing the last tab, want empty", got)
	}
	if s.GitLoadedPath() != "" {
		t.Fatalf("loaded = %q after closing the last tab, want empty", s.GitLoadedPath())
	}
	if s.GitActiveTabIndex() != -1 {
		t.Fatalf("active index = %d with no tabs, want -1", s.GitActiveTabIndex())
	}
	if s.Source() != "" {
		t.Fatalf("editor not cleared after closing the last tab: %q", s.Source())
	}
}

// TestFileTabsCloseKeepsBufferEdits: closing a tab keeps the file's edit buffer,
// so re-opening it from the tree restores the unsaved edits.
func TestFileTabsCloseKeepsBufferEdits(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex")
	typeInto(s, "EDIT") // intro.tex -> "EDITINTRO"

	s.GitOpenFile("main.tex")           // switch away (active = main), tabs = main|intro
	s.GitCloseTab("chapters/intro.tex") // close intro's tab (inactive)
	if got := tabsJoined(s); got != "main.tex" {
		t.Fatalf("tabs = %q after closing intro", got)
	}

	// Re-open intro.tex: its edits come back from the retained buffer.
	s.GitOpenFile("chapters/intro.tex")
	if s.Source() != "EDITINTRO" {
		t.Fatalf("re-opened intro.tex = %q, want the retained edits EDITINTRO", s.Source())
	}
}

// TestFileTabsNoRepo: with no repo there are no tabs and the active index is -1;
// closing a tab or closing "" is a safe no-op.
func TestFileTabsNoRepo(t *testing.T) {
	s := newTestState(t, false)
	if len(s.GitOpenTabs()) != 0 || s.GitActiveTabIndex() != -1 {
		t.Fatalf("no repo: tabs=%v active=%d", s.GitOpenTabs(), s.GitActiveTabIndex())
	}
	s.GitCloseTab("")          // empty path: no-op
	s.GitCloseTab("ghost.tex") // unknown path: no-op
	s.git.addTab("")           // empty path never becomes a tab
	if len(s.GitOpenTabs()) != 0 {
		t.Fatalf("no-op operations changed tabs: %v", s.GitOpenTabs())
	}
}
