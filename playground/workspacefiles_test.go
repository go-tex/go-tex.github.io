// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// openWorkspace puts a State in front of a fake repository holding files, with
// p active, so the file verbs have something to act on.
func openWorkspace(t *testing.T, files map[string]string, active string) (*State, *fakeGitBackend) {
	t.Helper()
	s, f, _ := withGit(t)
	f.hasRepo = true
	for p, c := range files {
		f.fileData[p] = c
		f.files = append(f.files, p)
	}
	slices.Sort(f.files)
	s.git.setFiles(slices.Clone(f.files))
	if active != "" {
		s.git.openFile(active)
	}
	return s, f
}

// ⛔ THE DEFECT A DELETE WOULD HAVE, STATED FIRST. Removing the path from the
// working tree is the easy half. The file also has an edit buffer, and
// flushDirty writes every dirty buffer back before a stage or a commit — so a
// deleted file whose buffer survived is RECREATED by the next commit. The delete
// would look like it worked and then undo itself.
// ⛔ TWO TABS, NOT ONE, and that is the whole test. Closing the LAST tab clears
// the editor and returns without stashing anything, so a single-tab version of
// this passes whichever order forgetLocal uses and pins nothing. The buffer only
// comes back when there is a neighbour to activate, because activating it stashes
// the outgoing file's live text on the way past. Measured: with the order
// reversed, the single-tab version stayed green and this one goes red.
func TestADeletedFileIsNotWrittenBackByTheNextFlush(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A", "notes.tex": "B"}, "main.tex")
	s.GitOpenFile("notes.tex")
	if len(s.GitOpenTabs()) != 2 {
		t.Fatalf("this test needs a neighbour tab, got %v", s.GitOpenTabs())
	}
	// Edit it, so its buffer is dirty and would certainly be flushed.
	s.SetSource("B edited")

	var gotErr error
	s.GitDeleteFile("notes.tex", func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("delete: %v", gotErr)
	}
	if _, held := s.git.buffers["notes.tex"]; held {
		t.Error("the deleted file still has an edit buffer; the next commit would write it back")
	}
	if dirty := s.git.dirtyBuffers(); dirty["notes.tex"] != "" {
		t.Errorf("the deleted file is still in the flush set: %v", dirty)
	}
	// And the flush is the thing that would do it, so run it.
	var flushErr error
	s.git.flushDirty(func(err error) { flushErr = err })
	if flushErr != nil {
		t.Fatalf("flush: %v", flushErr)
	}
	if _, back := f.gotWrites["notes.tex"]; back {
		t.Error("the flush wrote the deleted file back into the tree")
	}
}

// And the tab goes with it: a tab for a file that no longer exists is a row the
// person can click to open nothing.
func TestADeleteClosesTheFilesTab(t *testing.T) {
	s, _ := openWorkspace(t, map[string]string{"main.tex": "A", "notes.tex": "B"}, "main.tex")
	s.GitOpenFile("notes.tex")
	if got := s.GitOpenTabs(); len(got) != 2 {
		t.Fatalf("expected two tabs, got %v", got)
	}
	s.GitDeleteFile("notes.tex", nil)
	if got := s.GitOpenTabs(); slices.Contains(got, "notes.tex") {
		t.Errorf("the deleted file keeps a tab: %v", got)
	}
	// The neighbour is active and the editor shows it, rather than the deleted
	// file's text sitting in an editor with no file behind it.
	if active := s.git.loaded.Get(); active != "main.tex" {
		t.Errorf("active file after deleting the active one = %q, want main.tex", active)
	}
	if src := s.Source(); src != "A" {
		t.Errorf("editor shows %q after the delete, want the neighbour's content", src)
	}
}

// A file that exists only as a buffer — created or imported, never written back
// — is dropped locally. Asking the backend would answer "not-exist" and turn a
// correct delete into an error.
func TestDeletingAnUnwrittenFileNeedsNoBackend(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	name := s.GitCreateFile("fresh.tex")
	if name != "fresh.tex" {
		t.Fatalf("create gave %q", name)
	}
	var gotErr error
	s.GitDeleteFile("fresh.tex", func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("deleting an unwritten file: %v", gotErr)
	}
	if f.removeCalls != 0 {
		t.Errorf("the backend was asked to remove a path it never had (%d calls)", f.removeCalls)
	}
	if _, held := s.git.buffers["fresh.tex"]; held {
		t.Error("the buffer survived")
	}
}

// A path the workspace does not hold at all is refused, not sent on.
func TestDeletingAPathTheWorkspaceDoesNotHoldIsRefused(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	var gotErr error
	s.GitDeleteFile("ghost.tex", func(err error) { gotErr = err })
	if !errors.Is(gotErr, errNoGitPath) {
		t.Fatalf("error = %v, want errNoGitPath", gotErr)
	}
	if f.removeCalls != 0 {
		t.Errorf("the backend was called for a path nothing holds")
	}
}

// A backend failure leaves the workspace exactly as it was: the row, the buffer
// and the tab all stay, because the file is still there.
func TestAFailedDeleteKeepsTheFile(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A", "notes.tex": "B"}, "notes.tex")
	f.removeErr = errors.New("boom")
	var gotErr error
	s.GitDeleteFile("notes.tex", func(err error) { gotErr = err })
	if gotErr == nil {
		t.Fatal("a failing remove reported success")
	}
	if _, held := s.git.buffers["notes.tex"]; !held {
		t.Error("the buffer was dropped although the file is still in the tree")
	}
	if !s.git.treeLists("notes.tex") {
		t.Error("the file list lost a path the backend still holds")
	}
}

// --- create ------------------------------------------------------------------

// ⛔ A create never overwrites. The workspace is the only copy until a commit, so
// creating "main.tex" beside an existing one suffixes rather than destroying it.
func TestCreateNeverOverwritesAnExistingName(t *testing.T) {
	s, _ := openWorkspace(t, map[string]string{"main.tex": "precious"}, "main.tex")
	got := s.GitCreateFile("main.tex")
	if got != "main (2).tex" {
		t.Fatalf("create over an existing name gave %q, want main (2).tex", got)
	}
	if c, _ := s.git.bufferContent("main.tex"); c != "precious" {
		t.Errorf("the existing file's content became %q", c)
	}
}

// It also must not land on a repository file nobody has opened: that name is
// taken even with no buffer for it.
func TestCreateAvoidsAnUnopenedRepositoryFile(t *testing.T) {
	s, _ := openWorkspace(t, map[string]string{"main.tex": "A", "notes.tex": "B"}, "main.tex")
	if got := s.GitCreateFile("notes.tex"); got != "notes (2).tex" {
		t.Fatalf("create gave %q, want notes (2).tex", got)
	}
}

func TestCreateOpensWhatItMade(t *testing.T) {
	s, _ := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	name := s.GitCreateFile("sections/intro.tex")
	if name != "sections/intro.tex" {
		t.Fatalf("create gave %q", name)
	}
	if s.git.loaded.Get() != name {
		t.Errorf("the created file is not active: %q", s.git.loaded.Get())
	}
	if !slices.Contains(s.GitOpenTabs(), name) {
		t.Errorf("the created file has no tab: %v", s.GitOpenTabs())
	}
	if s.Source() != "" {
		t.Errorf("a created file opened with %q in the editor, want empty", s.Source())
	}
}

// ⛔ The host refuses a bad name even though the widget validates one first: the
// verbs are reached headlessly too, and a host that trusted its own UI to be the
// only caller would be one refactor away from writing outside the workspace.
func TestNamesThatLeaveTheWorkspaceAreRefused(t *testing.T) {
	bad := []string{"", "/etc/passwd", "../outside.tex", "a/../../b.tex", "./a.tex", "a//b.tex", "dir/", "a\\b.tex", "x\x01.tex", "..", "."}
	for _, name := range bad {
		if why := workspaceNameRefusal(name); why == "" {
			t.Errorf("%q was accepted as a workspace name", name)
		}
	}
	for _, name := range []string{"main.tex", "sections/intro.tex", "a.b.c.tex", "Chapitre 1.tex", "日本語.tex"} {
		if why := workspaceNameRefusal(name); why != "" {
			t.Errorf("%q was refused: %s", name, why)
		}
	}
}

func TestCreateRefusesABadNameWithoutTouchingTheWorkspace(t *testing.T) {
	s, _ := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	if got := s.GitCreateFile("../escape.tex"); got != "" {
		t.Fatalf("create gave %q for a path that leaves the workspace", got)
	}
	if n := len(s.GitOpenTabs()); n != 1 {
		t.Errorf("a refused create changed the tabs: %v", s.GitOpenTabs())
	}
	if msg := s.git.errMsg.Get(); !strings.Contains(msg, "leaves the workspace") {
		t.Errorf("the refusal does not say why: %q", msg)
	}
}

// --- rename ------------------------------------------------------------------

// ⛔ WRITE THE NEW ONE FIRST. A rename is two operations over a protocol that can
// fail between them. Write-then-remove leaves two files on a failure; the other
// order leaves none, and a lost file in a workspace that is the only copy until a
// commit is not recoverable. This pins the order.
func TestARenameWritesTheNewPathBeforeRemovingTheOld(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"old.tex": "content"}, "old.tex")
	f.removeErr = errors.New("boom")
	var gotErr error
	s.GitRenameFile("old.tex", "new.tex", func(err error) { gotErr = err })
	if gotErr == nil {
		t.Fatal("a failing remove reported a successful rename")
	}
	// The new path got written even though the remove failed: two files, not none.
	if f.gotWrites["new.tex"] != "content" {
		t.Errorf("the new path was not written first: %v", f.gotWrites)
	}
	if _, gone := f.fileData["old.tex"]; !gone {
		t.Error("the old path vanished although its removal failed")
	}
	// And the message names both halves, so the person is not sent looking for
	// one file when the workspace holds two.
	msg := s.git.errMsg.Get()
	if !strings.Contains(msg, "new.tex") || !strings.Contains(msg, "old.tex") {
		t.Errorf("the error names only part of what happened: %q", msg)
	}
}

func TestARenameMovesTheBufferTheTabAndTheEditor(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"old.tex": "content", "main.tex": "A"}, "old.tex")
	s.SetSource("edited")
	var gotErr error
	s.GitRenameFile("old.tex", "sections/new.tex", func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("rename: %v", gotErr)
	}
	if _, held := s.git.buffers["old.tex"]; held {
		t.Error("the old name kept a buffer")
	}
	if c, _ := s.git.bufferContent("sections/new.tex"); c != "edited" {
		t.Errorf("the new name holds %q, want the unsaved edits", c)
	}
	if got := s.GitOpenTabs(); slices.Contains(got, "old.tex") || !slices.Contains(got, "sections/new.tex") {
		t.Errorf("tabs after rename = %v", got)
	}
	if s.git.loaded.Get() != "sections/new.tex" {
		t.Errorf("the renamed file is not active: %q", s.git.loaded.Get())
	}
	// A new name holding a slash IS the move: the content went to the new path.
	if f.gotWrites["sections/new.tex"] != "edited" {
		t.Errorf("the move did not carry the content: %v", f.gotWrites)
	}
}

// ⛔ A rename does NOT suffix the way an import does. An import has no opinion
// about its name; a rename is nothing but an opinion about one, so answering
// "main.tex" with "main (2).tex" would be ignoring what was just typed.
func TestARenameOntoAnExistingNameIsRefusedNotSuffixed(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"old.tex": "A", "taken.tex": "B"}, "old.tex")
	var gotErr error
	s.GitRenameFile("old.tex", "taken.tex", func(err error) { gotErr = err })
	if gotErr == nil {
		t.Fatal("renaming onto an existing name succeeded")
	}
	if f.writeCalls != 0 || f.removeCalls != 0 {
		t.Errorf("the backend was touched for a refused rename (%d writes, %d removes)", f.writeCalls, f.removeCalls)
	}
	if _, held := s.git.buffers["old.tex"]; !held {
		t.Error("the refused rename dropped the source file's buffer")
	}
	if msg := s.git.errMsg.Get(); !strings.Contains(msg, "already exists") {
		t.Errorf("the refusal does not say why: %q", msg)
	}
}

// Renaming a file that exists only as a buffer is renaming a map key: no backend
// round-trip, because there is nothing in the tree to move.
func TestRenamingAnUnwrittenFileNeedsNoBackend(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	s.GitCreateFile("draft.tex")
	s.SetSource("typed")
	var gotErr error
	s.GitRenameFile("draft.tex", "final.tex", func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("rename: %v", gotErr)
	}
	if f.writeCalls != 0 || f.removeCalls != 0 {
		t.Errorf("an unwritten rename went to the backend (%d writes, %d removes)", f.writeCalls, f.removeCalls)
	}
	if c, _ := s.git.bufferContent("final.tex"); c != "typed" {
		t.Errorf("the new name holds %q, want the typed text", c)
	}
	if _, held := s.git.buffers["draft.tex"]; held {
		t.Error("the old name kept a buffer")
	}
}

// Renaming a file nobody has opened has no buffer to carry, so the content comes
// from the committed file. This is the ordinary case for a repository of many
// files and only one open, and it is the path that reads the backend.
func TestRenamingAnUnopenedFileCarriesItsCommittedContent(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A", "appendix.tex": "APPENDIX"}, "main.tex")
	if _, held := s.git.buffers["appendix.tex"]; held {
		t.Fatal("this test needs a file with no buffer")
	}
	var gotErr error
	s.GitRenameFile("appendix.tex", "back/matter.tex", func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("rename: %v", gotErr)
	}
	if f.gotWrites["back/matter.tex"] != "APPENDIX" {
		t.Errorf("the new path got %q, want the committed content", f.gotWrites["back/matter.tex"])
	}
	// The active file is untouched: renaming something else must not move the
	// editor off what the person is working on.
	if s.git.loaded.Get() != "main.tex" {
		t.Errorf("renaming an unopened file moved the editor to %q", s.git.loaded.Get())
	}
	if s.Source() != "A" {
		t.Errorf("the editor's content changed to %q", s.Source())
	}
}

// A source that cannot be read is refused before anything is written, so a
// rename never invents an empty file in place of one it could not open.
func TestRenamingAnUnreadableFileWritesNothing(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	// Listed by the tree but absent from the content map: the shape of a file the
	// worker reports and then cannot read.
	s.git.setFiles(append(slices.Clone(s.git.files), "phantom.tex"))
	var gotErr error
	s.GitRenameFile("phantom.tex", "real.tex", func(err error) { gotErr = err })
	if !errors.Is(gotErr, errNoGitPath) {
		t.Fatalf("error = %v, want errNoGitPath", gotErr)
	}
	if f.writeCalls != 0 {
		t.Errorf("an unreadable source was written anyway (%d writes)", f.writeCalls)
	}
}

func TestRenamingToTheSameNameDoesNothing(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	var called bool
	s.GitRenameFile("main.tex", "main.tex", func(err error) {
		called = true
		if err != nil {
			t.Errorf("renaming to the same name reported %v", err)
		}
	})
	if !called {
		t.Error("the callback was not run")
	}
	if f.writeCalls != 0 || f.removeCalls != 0 {
		t.Error("renaming to the same name touched the backend")
	}
}

// --- the guards every verb shares ---------------------------------------------

func TestTheFileVerbsRefuseWithoutARepository(t *testing.T) {
	s, f, _ := withGit(t)
	f.hasRepo = false
	if got := s.GitCreateFile("a.tex"); got != "" {
		t.Errorf("create with no repository gave %q", got)
	}
	var delErr, renErr error
	s.GitDeleteFile("a.tex", func(err error) { delErr = err })
	s.GitRenameFile("a.tex", "b.tex", func(err error) { renErr = err })
	if !errors.Is(delErr, errNoGitRepo) || !errors.Is(renErr, errNoGitRepo) {
		t.Errorf("delete = %v, rename = %v, want errNoGitRepo for both", delErr, renErr)
	}
}

// A verb that ran while a network op was in flight would race the op's own
// file-list reply, so each is a no-op meanwhile.
func TestTheFileVerbsWaitForAnOpInFlight(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"main.tex": "A"}, "main.tex")
	s.git.beginOp("Pulling")
	defer s.git.endOp()
	if got := s.GitCreateFile("a.tex"); got != "" {
		t.Errorf("create during an op gave %q", got)
	}
	s.GitDeleteFile("main.tex", nil)
	s.GitRenameFile("main.tex", "b.tex", nil)
	if f.removeCalls != 0 || f.writeCalls != 0 {
		t.Errorf("a verb reached the backend during an op (%d removes, %d writes)", f.removeCalls, f.writeCalls)
	}
}
