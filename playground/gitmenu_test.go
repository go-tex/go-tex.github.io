// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// ⛔ THE POINT OF THE CHANGE, as a test: the command band is ONE row.
//
// It was eight buttons wrapped three to a row — three rows of fixed height, in
// the one column whose scarce resource is vertical space, for actions used
// occasionally. Each row costs btnH+gap, so two rows came back; at default
// density that is about three more files visible, permanently.
//
// The assertion is on rows rather than on pixels because the row is the design
// decision and the pixels are its consequence: a future change to btnH should
// not fail this, and a fourth button quietly reappearing should.
func TestTheCommandBandIsOneRow(t *testing.T) {
	s, _ := withClonedSidebar(t)
	if n := len(s.sidebar.buttons); n != 1 {
		var labels []string
		for _, b := range s.sidebar.buttons {
			labels = append(labels, b.label)
		}
		t.Fatalf("the command band holds %d buttons (%v), want 1", n, labels)
	}
	band := s.sidebar.buttons[0]
	if band.role != sbRoleGitMenu {
		t.Fatalf("the one button is role %d, want the command menu", band.role)
	}
	if band.rect.W < s.sidebar.bounds.W/2 {
		t.Errorf("the command button is %d wide in a %d column — it should span it", band.rect.W, s.sidebar.bounds.W)
	}
}

// Every command the buttons offered is still offered, so the change moved them
// rather than quietly dropping any.
func TestTheMenuOffersEveryCommandTheButtonsDid(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.openGitMenu()
	var got []string
	for _, it := range s.sidebar.gitMenu.Menu.Items {
		if !it.Separator {
			got = append(got, it.Label)
		}
	}
	joined := strings.Join(got, "|")
	for _, want := range []string{"Refresh", "Stage", "Commit…", "Pull", "Push", "Forget this repository"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the command menu lost %q; it offers %v", want, got)
		}
	}
	// And Import/Export are NOT here: they talk to the computer, not the remote,
	// and now live in the file tree's own menu beside New File / Rename / Delete.
	for _, gone := range []string{"Import", "Export"} {
		if strings.Contains(joined, gone) {
			t.Errorf("%q is still in the git command menu: %v", gone, got)
		}
	}
}

// ⛔ A command that cannot run right now is a DISABLED ROW, not a missing one. A
// menu whose contents change with the state teaches nothing and leaves the
// reader wondering whether they misremembered.
func TestBusyGreysTheCommandsRatherThanHidingThem(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.git.beginOp("Pulling")
	defer s.git.endOp()
	s.sidebar.openGitMenu()
	items := s.sidebar.gitMenu.Menu.Items
	var rows int
	for _, it := range items {
		if it.Separator {
			continue
		}
		rows++
		if it.Action != nil {
			t.Errorf("%q is live while a git op is in flight", it.Label)
		}
	}
	if rows < 6 {
		t.Errorf("a busy column offers only %d rows; the commands should be greyed, not removed", rows)
	}
}

// ⛔ And the button that OPENS the menu stays enabled while busy. It opens a
// menu; the menu greys the commands. A disabled opener would hide the whole
// list — including Refresh, which is how someone finds out what the column is
// busy with.
func TestTheMenuOpenerStaysEnabledWhileBusy(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.git.beginOp("Pushing")
	defer s.git.endOp()
	s.Draw(make([]byte, testW*testH*4))
	bt := s.sidebar.btn(sbRoleGitMenu, "Git ▾")
	if bt.Disabled().Get() {
		t.Fatal("the command menu's button is disabled while busy, so the commands cannot even be seen")
	}
}

// The menu is clamped to the column, so it can never spill into the editor —
// which matters because the column is drawn BEFORE the body, and anything
// overflowing it would be painted over by the editor.
func TestTheCommandMenuStaysInsideTheColumn(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.openGitMenu()
	mb := s.sidebar.gitMenu.MenuBounds()
	col := s.sidebar.bounds
	if mb.X < col.X || mb.Y < col.Y || mb.X+mb.W > col.X+col.W || mb.Y+mb.H > col.Y+col.H {
		t.Fatalf("the menu %v escapes the column %v and would be painted over by the editor", mb, col)
	}
}

// A click elsewhere closes it without also pressing what is underneath.
func TestAClickOutsideClosesTheCommandMenu(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.openGitMenu()
	if !s.sidebar.menuOpen() {
		t.Fatal("the menu did not open")
	}
	before := s.Source()
	er := s.editor.Bounds()
	s.HandleClick(er.X+er.W/2, er.Y+er.H/2)
	if s.sidebar.menuOpen() {
		t.Error("the command menu survived a click in the editor")
	}
	if s.Source() != before {
		t.Error("the dismissing click also reached the editor")
	}
}

// Forget sits last, behind a separator of its own: it throws away this
// browser's saved copy, and grouping it with Pull and Push would put a discard
// one slip away from the two commands people use most.
func TestForgetIsSeparatedFromTheEverydayCommands(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.sidebar.openGitMenu()
	items := s.sidebar.gitMenu.Menu.Items
	last := items[len(items)-1]
	if !strings.HasPrefix(last.Label, "Forget") {
		t.Fatalf("the last row is %q, want Forget", last.Label)
	}
	if len(items) < 2 || !items[len(items)-2].Separator {
		t.Error("Forget is not behind a separator of its own")
	}
}

// With no repository the column keeps its empty state — Clone, and Import,
// which must stay a BUTTON there: with no repository there is no tree, so the
// tree's context menu cannot offer it, and bringing a file in is exactly what
// someone with no repository may want to do first.
func TestTheEmptyStateKeepsCloneAndImport(t *testing.T) {
	s := newTestState(t, false)
	s.git.io.Import = func(hostFileDeliver) {}
	s.layout()
	s.Draw(make([]byte, testW*testH*4))
	var roles []sidebarRole
	for _, b := range s.sidebar.buttons {
		roles = append(roles, b.role)
	}
	var hasClone, hasImport bool
	for _, r := range roles {
		switch r {
		case sbRoleClone:
			hasClone = true
		case sbRoleImport:
			hasImport = true
		}
	}
	if !hasClone || !hasImport {
		t.Fatalf("the empty state offers roles %v; it needs Clone and Import", roles)
	}
	if s.sidebar.treeRect != (toolkit.Rect{}) {
		t.Error("the empty state laid out a file tree")
	}
}
