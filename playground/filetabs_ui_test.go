// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// drawnTabs draws the app (so the file-tab strip is laid out) and returns the
// wysiwyg FolderTabs for geometry.
func drawnTabs(s *State) *toolkit.FolderTabs {
	s.Draw(make([]byte, testW*testH*4))
	return s.wysiwyg().tabs
}

// TestFileTabStripReflectsOpenTabs: the editor-pane strip shows one tab per open
// file (base names), with the active file selected.
func TestFileTabStripReflectsOpenTabs(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex")
	s.GitOpenFile("chapters/ch1.tex") // tabs: main.tex | intro.tex | ch1.tex, active ch1

	ft := drawnTabs(s)
	if got := strings.Join(ft.Labels, "|"); got != "main.tex|intro.tex|ch1.tex" {
		t.Fatalf("file-tab labels = %q", got)
	}
	if ft.Selected().Get() != 2 {
		t.Fatalf("selected tab = %d, want 2 (ch1.tex)", ft.Selected().Get())
	}
}

// TestFileTabClickSwitchesFile: clicking a file tab makes that file active (and
// the render follows it, since compileSource tracks the active .tex).
func TestFileTabClickSwitchesFile(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex") // active intro; tabs: main | intro
	ft := drawnTabs(s)

	// Click the "main.tex" tab (index 0): its centre, in the label area (left of ×).
	tr := ft.TabRect(0)
	if !s.HandleClick(tr.X+toolkit.Scaled(toolkit.FolderTabsPadX), tr.Y+tr.H/2) {
		t.Fatal("file-tab click not consumed")
	}
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("clicking the main.tex tab did not switch: active = %q", s.GitLoadedPath())
	}
}

// TestFileTabCloseButtonClosesTab: clicking a tab's × closes it (and does not
// select it).
func TestFileTabCloseButtonClosesTab(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.GitOpenFile("chapters/intro.tex") // active intro; tabs: main | intro
	ft := drawnTabs(s)

	// Click the × of tab 0 (main.tex): the rightmost close-width of the tab.
	tr := ft.TabRect(0)
	cx := tr.X + tr.W - toolkit.Scaled(toolkit.FolderTabsCloseW)/2
	if !s.HandleClick(cx, tr.Y+tr.H/2) {
		t.Fatal("close-button click not consumed")
	}
	if got := strings.Join(s.GitOpenTabs(), "|"); got != "chapters/intro.tex" {
		t.Fatalf("after ×, tabs = %q, want intro only", got)
	}
	if s.GitLoadedPath() != "chapters/intro.tex" {
		t.Fatalf("closing an inactive tab changed the active file to %q", s.GitLoadedPath())
	}
}

// TestWysiwygToggleButton: the toolbar view toggle flips Source⇄WYSIWYG, its rect
// is laid out (non-empty), and EditorTabRect points at it.
func TestWysiwygToggleButton(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)
	s.Draw(make([]byte, testW*testH*4)) // lay out the toolbar

	r := s.EditorTabRect(tabWysiwyg)
	if r[2] <= 0 || r[3] <= 0 {
		t.Fatalf("view toggle rect not laid out: %v", r)
	}
	if s.WysiwygActive() {
		t.Fatal("mode should start on Source")
	}
	if !s.HandleClick(r[0]+r[2]/2, r[1]+r[3]/2) {
		t.Fatal("toggle click not consumed")
	}
	if !s.WysiwygActive() {
		t.Fatal("toggle did not switch to WYSIWYG")
	}
	// Clicking again returns to Source.
	if !s.HandleClick(r[0]+r[2]/2, r[1]+r[3]/2) {
		t.Fatal("second toggle click not consumed")
	}
	if s.WysiwygActive() {
		t.Fatal("toggle did not switch back to Source")
	}
}
