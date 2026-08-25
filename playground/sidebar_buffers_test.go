// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// This file proves the independent per-file edit-buffer model (git.go): opening
// another file no longer discards the current file's unsaved edits, several files
// can be dirty at once (their tree badges reflect it), stage/commit flush every
// dirty buffer to the working tree, and the render compiles the primary .tex's
// live buffer.

// The typeInto helper (real HandleChar input, caret-advancing) lives in
// latexcomplete_test.go and is reused here.

// TestPerFileBuffersRoundTrip is the core proof: open A, edit it, open B, edit it,
// switch back to A — A's edits AND caret are restored, and B stays dirty in the
// tree. Both files badge amber M at once.
func TestPerFileBuffersRoundTrip(t *testing.T) {
	s, _ := withClonedSidebar(t) // main.tex active ("MAIN BODY"); chapters/intro.tex = "INTRO"

	// Edit main.tex (caret starts at 0,0 after the clone load).
	typeInto(s, "XY") // -> "XYMAIN BODY", caret col 2
	if !s.GitBufferDirty("main.tex") {
		t.Fatal("main.tex should be dirty after editing")
	}
	wantCol := s.CursorCol()
	if wantCol != 2 {
		t.Fatalf("caret col after typing = %d, want 2", wantCol)
	}

	// Open intro.tex: main.tex's edits are preserved in its buffer, not discarded.
	if !s.GitOpenFile("chapters/intro.tex") {
		t.Fatal("opening intro.tex should succeed")
	}
	if s.Source() != "INTRO" {
		t.Fatalf("intro.tex source = %q, want INTRO", s.Source())
	}
	if !s.GitBufferDirty("main.tex") {
		t.Fatal("main.tex should STILL be dirty after switching away")
	}

	// Edit intro.tex → now two files are dirty at once.
	typeInto(s, "Z") // -> "ZINTRO"
	if !s.GitBufferDirty("chapters/intro.tex") {
		t.Fatal("intro.tex should be dirty after editing")
	}

	// The tree badges BOTH as modified (amber M).
	s.Draw(make([]byte, testW*testH*4))
	joined := strings.Join(s.SidebarFileRows(), "|")
	if !strings.Contains(joined, "main.tex M") {
		t.Fatalf("main.tex not badged M in the tree: %v", s.SidebarFileRows())
	}
	if !strings.Contains(joined, "intro.tex M") {
		t.Fatalf("intro.tex not badged M in the tree: %v", s.SidebarFileRows())
	}

	// Switch back to main.tex → its edits AND caret come back.
	if !s.GitOpenFile("main.tex") {
		t.Fatal("re-opening main.tex should succeed")
	}
	if s.Source() != "XYMAIN BODY" {
		t.Fatalf("main.tex restore = %q, want XYMAIN BODY", s.Source())
	}
	if s.CursorCol() != wantCol {
		t.Fatalf("caret not restored: col = %d, want %d", s.CursorCol(), wantCol)
	}
	// intro.tex stays dirty in the tree even though it is no longer active.
	if !s.GitBufferDirty("chapters/intro.tex") {
		t.Fatal("intro.tex should stay dirty after switching away from it")
	}
}

// TestOpenActiveFileIsNoop covers openFile's early return: opening the already
// active file changes nothing (and reports the file active).
func TestOpenActiveFileIsNoop(t *testing.T) {
	s, _ := withClonedSidebar(t) // main.tex active
	typeInto(s, "Q")
	before := s.Source()
	if !s.GitOpenFile("main.tex") {
		t.Fatal("opening the active file should report it active")
	}
	if s.Source() != before {
		t.Fatalf("opening the active file changed the buffer: %q", s.Source())
	}
}

// TestOpenFileReadError covers openFile's read-miss branch for a fresh (unbuffered)
// file: the error surfaces and the active file is unchanged.
func TestOpenFileReadError(t *testing.T) {
	s, f := withClonedSidebar(t) // main.tex active
	f.readErr = errGitTransport
	if s.GitOpenFile("refs.bib") {
		t.Fatal("opening a file that fails to read should not become active")
	}
	if s.GitError() == "" {
		t.Fatal("a read error on open should surface on the panel")
	}
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("active file should be unchanged, got %q", s.GitLoadedPath())
	}
}

// TestStashActiveEdgeCases covers stashActive's no-active-file early return and its
// buffer-create branch.
func TestStashActiveEdgeCases(t *testing.T) {
	// No active file → a safe no-op (no buffer created).
	s := newTestState(t, false)
	s.git.stashActive()
	if len(s.git.buffers) != 0 {
		t.Fatalf("stashActive with no active file created buffers: %v", s.git.buffers)
	}

	// Active file but no buffer for it → stashActive creates one from the live editor.
	s2, _ := withClonedSidebar(t)
	delete(s2.git.buffers, "main.tex")
	s2.git.stashActive()
	if _, ok := s2.git.buffers["main.tex"]; !ok {
		t.Fatal("stashActive should create the active file's buffer when absent")
	}
}

// TestGitStageFlushesAllDirtyBuffers proves Stage writes EVERY dirty buffer back to
// the working tree (not just the active file), and clears their dirtiness after.
func TestGitStageFlushesAllDirtyBuffers(t *testing.T) {
	s, f := withClonedSidebar(t)
	typeInto(s, "A") // main.tex -> "AMAIN BODY"
	if !s.GitOpenFile("chapters/intro.tex") {
		t.Fatal("open intro.tex")
	}
	typeInto(s, "B") // intro.tex -> "BINTRO"

	var gotErr error
	s.GitStage(func(e error) { gotErr = e })
	if gotErr != nil {
		t.Fatalf("stage err = %v", gotErr)
	}
	// Both dirty buffers reached the working tree.
	if f.gotWrites["main.tex"] != "AMAIN BODY" {
		t.Fatalf("stage did not flush main.tex: %q", f.gotWrites["main.tex"])
	}
	if f.gotWrites["chapters/intro.tex"] != "BINTRO" {
		t.Fatalf("stage did not flush intro.tex: %q", f.gotWrites["chapters/intro.tex"])
	}
	// After the flush both buffers match the tree → neither is dirty.
	if s.GitBufferDirty("main.tex") || s.GitBufferDirty("chapters/intro.tex") {
		t.Fatal("buffers should be clean after a stage flush")
	}
	if s.GitNotice() == "" {
		t.Fatal("a successful stage should set a notice")
	}
}

// TestGitCommitFlushesAllDirtyBuffers is the commit peer of the stage flush proof.
func TestGitCommitFlushesAllDirtyBuffers(t *testing.T) {
	s, f := withClonedSidebar(t)
	typeInto(s, "A")
	if !s.GitOpenFile("chapters/intro.tex") {
		t.Fatal("open intro.tex")
	}
	typeInto(s, "B")

	var gotErr error
	s.GitCommit(func(e error) { gotErr = e })
	if gotErr != nil {
		t.Fatalf("commit err = %v", gotErr)
	}
	if f.gotWrites["main.tex"] != "AMAIN BODY" || f.gotWrites["chapters/intro.tex"] != "BINTRO" {
		t.Fatalf("commit did not flush both buffers: %v", f.gotWrites)
	}
	// The active file's own content is committed through the backend Commit call.
	if f.gotCommitPath != "chapters/intro.tex" || f.gotCommitContent != "BINTRO" {
		t.Fatalf("commit forwarded path=%q content=%q", f.gotCommitPath, f.gotCommitContent)
	}
	if s.GitBufferDirty("main.tex") || s.GitBufferDirty("chapters/intro.tex") {
		t.Fatal("buffers should be clean after a commit flush")
	}
}

// TestGitStageFlushError covers the flush-failure branch of GitStage (and finishOp):
// a write-back error aborts before the stage, surfaces on the panel and clears busy.
func TestGitStageFlushError(t *testing.T) {
	s, f := withClonedSidebar(t)
	typeInto(s, "A") // make main.tex dirty so the flush actually round-trips
	f.writeErr = errGitTransport
	var gotErr error
	s.GitStage(func(e error) { gotErr = e })
	if gotErr == nil || s.GitError() == "" || s.GitBusy() {
		t.Fatalf("flush error not surfaced: err=%v panelErr=%q busy=%v", gotErr, s.GitError(), s.GitBusy())
	}
	// The stage step never ran.
	if f.gotStagePath != "" {
		t.Fatalf("stage should not run after a flush error, got %q", f.gotStagePath)
	}
}

// TestGitCommitFlushError is the commit peer of the flush-failure proof.
func TestGitCommitFlushError(t *testing.T) {
	s, f := withClonedSidebar(t)
	typeInto(s, "A")
	f.writeErr = errGitAuth
	var gotErr error
	s.GitCommit(func(e error) { gotErr = e })
	if gotErr == nil || s.GitError() == "" || s.GitBusy() {
		t.Fatalf("commit flush error not surfaced: err=%v panelErr=%q busy=%v", gotErr, s.GitError(), s.GitBusy())
	}
	if f.gotCommitPath != "" {
		t.Fatalf("commit should not run after a flush error, got %q", f.gotCommitPath)
	}
}

// TestPrimaryTeXCompiles proves item 4: the render compiles the primary .tex's LIVE
// buffer, tracking its edits even while another file (a .sty/.bib) is being edited.
func TestPrimaryTeXCompiles(t *testing.T) {
	// No repo: the editor's own buffer (the sample document) compiles.
	s0 := newTestState(t, false)
	if got := s0.git.compileSource(); got != s0.Source() {
		t.Fatalf("no-repo compileSource = %q, want the editor buffer", got)
	}

	s, _ := withClonedSidebar(t) // main.tex is the primary, and active
	if s.GitPrimaryPath() != "main.tex" {
		t.Fatalf("primary = %q, want main.tex", s.GitPrimaryPath())
	}
	typeInto(s, "Q") // main.tex -> "QMAIN BODY"
	// Primary active → its live editor content compiles.
	if got := s.git.compileSource(); got != "QMAIN BODY" {
		t.Fatalf("compileSource (primary active) = %q, want QMAIN BODY", got)
	}
	// Switch to a non-primary file: the render still compiles the primary's stashed
	// (edited) buffer.
	if !s.GitOpenFile("refs.bib") {
		t.Fatal("open refs.bib")
	}
	if got := s.git.compileSource(); got != "QMAIN BODY" {
		t.Fatalf("compileSource (editing refs.bib) = %q, want the primary's edits", got)
	}
	// A primary path with no buffer (and not active) falls back to the editor buffer.
	s.git.primaryPath = "ghost.tex"
	delete(s.git.buffers, "ghost.tex")
	if got := s.git.compileSource(); got != s.editor.Text().Get() {
		t.Fatalf("compileSource (missing primary buffer) = %q, want the editor buffer", got)
	}
}

// TestGitPickFileSetsPrimary covers the panel file-picker dispatch: picking a .tex
// makes it the compiled primary and opens it; an out-of-range pick is a no-op.
func TestGitPickFileSetsPrimary(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"main.tex", "other.tex"}
	f.fileData = map[string]string{"main.tex": "M", "other.tex": "O"}
	s.GitClone(nil)

	v := s.git
	idx := -1
	for i, p := range v.texFiles {
		if p == "other.tex" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("other.tex missing from the picker")
	}
	v.dispatch(gitRolePickFile, idx)
	if v.primaryPath != "other.tex" || s.GitLoadedPath() != "other.tex" || s.Source() != "O" {
		t.Fatalf("pick did not set primary/open: primary=%q loaded=%q source=%q", v.primaryPath, s.GitLoadedPath(), s.Source())
	}
	// An out-of-range pick is guarded (no panic, no change).
	v.dispatch(gitRolePickFile, -1)
	if v.primaryPath != "other.tex" {
		t.Fatalf("an out-of-range pick changed the primary: %q", v.primaryPath)
	}
}

// TestGitBufferPathsAndIntrospection covers the multi-buffer introspection helpers.
func TestGitBufferPathsAndIntrospection(t *testing.T) {
	s, _ := withClonedSidebar(t)
	if got := s.GitBufferPaths(); len(got) != 1 || got[0] != "main.tex" {
		t.Fatalf("buffer paths after clone = %v, want [main.tex]", got)
	}
	if !s.GitOpenFile("refs.bib") {
		t.Fatal("open refs.bib")
	}
	// Two buffers now exist, returned sorted.
	got := s.GitBufferPaths()
	if strings.Join(got, ",") != "main.tex,refs.bib" {
		t.Fatalf("buffer paths = %v, want [main.tex refs.bib]", got)
	}
}

// TestFreshCloneClearsBuffers proves a new clone drops every prior edit buffer so no
// stale dirtiness leaks across repositories.
func TestFreshCloneClearsBuffers(t *testing.T) {
	s, f := withClonedSidebar(t)
	typeInto(s, "A")
	if !s.GitOpenFile("refs.bib") {
		t.Fatal("open refs.bib")
	}
	if len(s.GitBufferPaths()) != 2 {
		t.Fatalf("expected two buffers before re-clone, got %v", s.GitBufferPaths())
	}
	// Re-clone a different repo.
	f.files = []string{"doc.tex"}
	f.fileData = map[string]string{"doc.tex": "DOC"}
	f.status = gitStatus{Branch: "main", Clean: true}
	s.GitClone(nil)
	if got := s.GitBufferPaths(); len(got) != 1 || got[0] != "doc.tex" {
		t.Fatalf("re-clone should reset buffers to the new repo, got %v", got)
	}
	if s.GitLoadedPath() != "doc.tex" || s.GitPrimaryPath() != "doc.tex" {
		t.Fatalf("re-clone active/primary = %q/%q", s.GitLoadedPath(), s.GitPrimaryPath())
	}
}
