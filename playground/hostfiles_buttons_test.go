package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// findButton returns the laid-out button for a role.
func findButton(t *testing.T, s *State, role sidebarRole) sidebarButton {
	t.Helper()
	for _, b := range s.sidebar.buttons {
		if b.role == role {
			return b
		}
	}
	t.Fatalf("no button laid out for role %v; roles present: %v", role, laidOutRoles(s))
	return sidebarButton{}
}

func laidOutRoles(s *State) []sidebarRole {
	var out []sidebarRole
	for _, b := range s.sidebar.buttons {
		out = append(out, b.role)
	}
	return out
}

// ⛔ Import has to be reachable with NO repository. Cloning was the only way to
// get a document into the workspace, which made "bring your own .tex" depend on
// having a remote to bring it to — the reverse of the order a visitor arrives in.
func TestImportIsOfferedInTheEmptyState(t *testing.T) {
	s := newTestState(t, false)
	s.SetSidebarOpen(true)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	if s.sidebar.hasRepo() {
		t.Fatal("this test needs the empty state")
	}
	imp := findButton(t, s, sbRoleImport)
	if !imp.disabled {
		t.Error("with no host file IO installed the Import button should be disabled")
	}
	// Both buttons fit inside the column and do not overlap.
	clone := findButton(t, s, sbRoleClone)
	if clone.rect.X+clone.rect.W > imp.rect.X {
		t.Errorf("Clone %v overlaps Import %v", clone.rect, imp.rect)
	}
	if imp.rect.X+imp.rect.W > s.sidebar.bounds.X+s.sidebar.bounds.W {
		t.Errorf("Import %v sticks out of the column %v", imp.rect, s.sidebar.bounds)
	}

	// With a host installed it becomes live, and a click reaches the picker.
	asked := false
	// The host's IO is installed before the first layout in the real app (it rides
	// on EnableGit); here it arrives after one, so ask for the layout the app would
	// already have done.
	s.git.io.Import = func(hostFileDeliver) { asked = true }
	s.layout()
	s.Draw(buf) // drawButtons is what pushes `disabled` onto the widget itself
	if findButton(t, s, sbRoleImport).disabled {
		t.Fatal("with a host installed the Import button should be live")
	}
	r := findButton(t, s, sbRoleImport).rect
	s.HandleClick(r.X+r.W/2, r.Y+r.H/2)
	if !asked {
		t.Error("clicking Import did not reach the host's picker")
	}
}

// A disabled button must not act. The paint is not the guard: the click path runs
// through the widget, and a check that lived only in how the button is DRAWN
// would be one refactor away from calling a nil picker.
func TestClickingADisabledImportDoesNothing(t *testing.T) {
	s := newTestState(t, false)
	s.SetSidebarOpen(true)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	r := findButton(t, s, sbRoleImport).rect
	s.HandleClick(r.X+r.W/2, r.Y+r.H/2)
	if s.GitActive() {
		t.Error("a disabled Import opened the Remote-Git panel")
	}
	// ⛔ And the same with the paint's guard BYPASSED. The line above proves only
	// that a disabled widget swallows its click, which it would do with no nil
	// check in dispatch at all — so it is no evidence about dispatch. Calling
	// dispatch directly, with io.Import still nil, is the control: without the
	// guard this panics.
	s.sidebar.dispatch(sbRoleImport)
	s.sidebar.dispatch(sbRoleExport)
}

// With a repository open, both commands live in the FILE TREE's context menu
// beside New File / Rename / Delete — they act on files, not on the remote — and
// each is live only when there is a host to talk to.
//
// ⛔ This replaces TestImportAndExportSitInTheCommandRows, whose name described
// where they used to be. The assertion that matters did not change: a command
// with no host behind it must be present and DISABLED, not missing.
func TestImportAndExportLiveInTheTreeMenu(t *testing.T) {
	s, _ := withClonedSidebar(t)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// On the active file's row, with no host IO installed.
	items := s.sidebar.treeMenuExtra("main.tex", false)
	imp := findMenuItem(t, items, "Import from this computer…")
	if imp.Action != nil {
		t.Error("with no host file IO the Import row should be disabled")
	}
	exp := findMenuItem(t, items, "Export “main.tex”…")
	if exp.Action != nil {
		t.Error("with no host file IO the Export row should be disabled")
	}

	var gotName string
	s.git.io.Export = func(name string, _ []byte) { gotName = name }
	s.git.io.Import = func(hostFileDeliver) {}
	items = s.sidebar.treeMenuExtra("main.tex", false)
	if findMenuItem(t, items, "Import from this computer…").Action == nil {
		t.Fatal("with a host installed the Import row should be live")
	}
	live := findMenuItem(t, items, "Export “main.tex”…")
	if live.Action == nil {
		t.Fatal("with a host installed the Export row should be live")
	}
	live.Action()
	if gotName != "main.tex" {
		t.Errorf("the Export row sent %q to the host, want main.tex", gotName)
	}
}

// ⛔ Export acts on ONE file, so it is offered on a file row and not on a folder
// or on the root: a menu that offered it everywhere would have to decide what
// "export this folder" meant, and there is no answer. Import is offered
// everywhere, because a file can be brought in anywhere.
func TestExportIsOfferedOnAFileRowOnly(t *testing.T) {
	s, _ := withClonedSidebar(t)
	s.git.io.Export = func(string, []byte) {}
	for _, c := range []struct {
		path string
		dir  bool
	}{
		{"chapters", true},
		{"", true}, // the root
	} {
		for _, it := range s.sidebar.treeMenuExtra(c.path, c.dir) {
			if strings.HasPrefix(it.Label, "Export") {
				t.Errorf("Export offered on %q (dir=%v)", c.path, c.dir)
			}
		}
	}
	if len(s.sidebar.treeMenuExtra("chapters", true)) == 0 {
		t.Error("a folder row lost Import as well")
	}
}

// findMenuItem returns the row with label, failing when it is absent — a
// disabled row is still present, which is the distinction these tests turn on.
func findMenuItem(t *testing.T, items []toolkit.MenuItem, label string) toolkit.MenuItem {
	t.Helper()
	var have []string
	for _, it := range items {
		if it.Label == label {
			return it
		}
		if !it.Separator {
			have = append(have, it.Label)
		}
	}
	t.Fatalf("no menu row %q; rows present: %v", label, have)
	return toolkit.MenuItem{}
}

// ⛔ A read that failed is not an empty file. Reported as empty bytes it would
// SUCCEED — a tab named after the user's file, holding nothing — which is the
// shape of a lost file, so the two travel on separate channels.
func TestAFailedReadIsNotAnEmptyImport(t *testing.T) {
	s := newTestState(t, false)
	v := s.git
	before := len(v.buffers)

	v.importDelivered("paper.tex", nil, true)
	if len(v.buffers) != before {
		t.Errorf("a failed read created a buffer: %d → %d", before, len(v.buffers))
	}
	if e := v.errMsg.Get(); !strings.Contains(e, "could not be read") {
		t.Errorf("errMsg = %q, want it to say the file could not be read", e)
	}

	// An actually empty file is a real file the user picked, and is taken.
	v.importDelivered("empty.tex", []byte{}, false)
	if _, ok := v.buffers["empty.tex"]; !ok {
		t.Errorf("an empty file was refused; buffers = %v", v.buffers)
	}
}
