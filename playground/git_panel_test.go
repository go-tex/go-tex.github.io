// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

func TestGitLauncherThroughHandleClick(t *testing.T) {
	s := newTestState(t, false)
	s.git.layout()
	lb := s.git.launcher
	// A click on the launcher, routed through the app's own HandleClick, opens the
	// panel (the app.go hook's true branch).
	if !s.HandleClick(lb.X+lb.W/2, lb.Y+lb.H/2) {
		t.Fatal("HandleClick did not consume the Git launcher click")
	}
	if !s.GitActive() {
		t.Fatal("launcher click via HandleClick did not open the panel")
	}
}

func TestGitLayoutEdges(t *testing.T) {
	s := newTestState(t, false)
	v := s.git
	// Before the host's first layout the toolbar height is zero: the launcher
	// falls back to a default height rather than collapsing.
	s.toolbarH = 0
	v.layout()
	if v.launcher.H <= 0 {
		t.Fatalf("launcher height with toolbarH==0 = %d", v.launcher.H)
	}
	// A surface narrower than the panel clamps the panel to fit.
	s.w = 120
	v.open = true
	v.layout()
	if v.panel.W > s.w {
		t.Fatalf("panel width %d exceeds surface %d", v.panel.W, s.w)
	}
}

func TestGitClickRouting(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex", "chapters/a.tex", "chapters/b.tex", "d.tex"}
	f.fileData["main.tex"] = "x"
	f.fileData["chapters/a.tex"] = "A"
	s.GitClone(nil) // loads main.tex, and sets up a >1 tex picker
	v.open = true
	v.layout()

	// Closed-launcher branch: a click off the launcher while closed is ignored.
	v.open = false
	v.layout()
	if v.handleClick(-100, -100) {
		t.Fatal("off-launcher click consumed while closed")
	}
	lb := v.launcher
	if !v.handleClick(lb.X+lb.W/2, lb.Y+lb.H/2) || !v.open {
		t.Fatal("launcher click did not open the panel")
	}

	// Focus a text field.
	v.layout()
	var urlBox = v.boxes[0]
	if !v.handleClick(urlBox.rect.X+2, urlBox.rect.Y+2) || v.focus != urlBox.field {
		t.Fatalf("field click did not focus it (focus=%d)", v.focus)
	}

	// Click the file-pick button for chapters/a.tex (index 1 in texFiles) → loads
	// that file (defocuses fields).
	v.layout()
	loaded := false
	for _, b := range v.buttons {
		if b.role == gitRolePickFile && b.arg == 1 {
			loaded = v.handleClick(b.rect.X+1, b.rect.Y+1)
		}
	}
	if !loaded {
		t.Fatal("file-pick button not clicked")
	}
	if v.focus != gitFieldNone {
		t.Fatal("clicking a button should defocus the field")
	}
	if s.GitLoadedPath() != "chapters/a.tex" {
		t.Fatalf("pick did not load chapters/a.tex: %q", s.GitLoadedPath())
	}

	// Click Close.
	v.layout()
	for _, b := range v.buttons {
		if b.role == gitRoleClose {
			v.handleClick(b.rect.X+1, b.rect.Y+1)
		}
	}
	if v.open {
		t.Fatal("Close did not close the panel")
	}

	// A click on empty panel space is still swallowed (modal).
	v.open = true
	v.layout()
	if !v.handleClick(v.panel.X+1, v.panel.Y+v.panel.H-2) {
		t.Fatal("modal panel should swallow a background click")
	}
}

func TestGitPickFileBusyAndBounds(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex", "a.tex"}
	f.fileData["main.tex"] = "x"
	f.fileData["a.tex"] = "A"
	s.GitClone(nil)
	// Out-of-range index is a no-op (defensive).
	v.dispatch(gitRolePickFile, 99)
	// Busy blocks a pick.
	v.busy.Set(true)
	v.dispatch(gitRolePickFile, 1)
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("busy pick changed the file to %q", s.GitLoadedPath())
	}
	v.busy.Set(false)
	// roleNone is a no-op branch.
	v.dispatch(gitRoleNone, 0)
}

func TestGitDispatchActionRoles(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = "x"
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Clean: true}

	// Each action role routes to the matching State method.
	v.dispatch(gitRoleClone, 0)
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("dispatch Clone did not load: %q", s.GitLoadedPath())
	}
	v.dispatch(gitRolePull, 0)
	v.dispatch(gitRoleCommit, 0)
	if f.gotCommitPath != "main.tex" {
		t.Fatalf("dispatch Commit did not reach the backend: %q", f.gotCommitPath)
	}
	v.dispatch(gitRolePush, 0)
	if s.GitNotice() == "" {
		t.Fatal("dispatch Push produced no notice")
	}
}

func TestGitCharEditsFocusedField(t *testing.T) {
	s, _, _ := withGit(t)
	v := s.git
	// Unfocused / closed: not consumed.
	if v.handleChar("x") {
		t.Fatal("closed panel consumed a char")
	}
	v.open = true
	if v.handleChar("x") {
		t.Fatal("unfocused field consumed a char")
	}
	// Focused URL field takes characters.
	v.focus = gitFieldURL
	v.url.Set("")
	if !s.HandleChar("h") || !s.HandleChar("i") {
		t.Fatal("focused field did not consume chars via HandleChar")
	}
	if v.url.Get() != "hi" {
		t.Fatalf("url = %q, want hi", v.url.Get())
	}
	// A multi-rune key name is swallowed but not inserted.
	before := v.url.Get()
	if !v.handleChar("Shift") || v.url.Get() != before {
		t.Fatal("a key-name should be swallowed but not inserted")
	}
	// Focus with no observable is impossible via fieldObs, but guard the branch:
	v.focus = gitField(999)
	if v.handleChar("z") {
		t.Fatal("an unknown focus field should not consume a char")
	}
}

func TestGitKeyEditing(t *testing.T) {
	s, _, _ := withGit(t)
	v := s.git
	// Closed: not consumed.
	if v.handleKey("Backspace") {
		t.Fatal("closed panel consumed a key")
	}
	v.open = true
	// Escape while open-but-unfocused closes the panel.
	if !s.HandleKeyDown("Escape") || v.open {
		t.Fatal("Escape should close an unfocused panel")
	}

	// Backspace on a focused field.
	v.open = true
	v.focus = gitFieldBranch
	v.branch.Set("main")
	if !v.handleKey("Backspace") || v.branch.Get() != "mai" {
		t.Fatalf("backspace: branch=%q", v.branch.Get())
	}
	// Backspace on an empty field is a no-op.
	v.branch.Set("")
	v.handleKey("Backspace")
	if v.branch.Get() != "" {
		t.Fatalf("backspace on empty = %q", v.branch.Get())
	}
	// Tab advances focus.
	v.focus = gitFieldURL
	if !v.handleKey("Tab") || v.focus != gitFieldBranch {
		t.Fatalf("Tab did not advance focus (focus=%d)", v.focus)
	}
	// Enter also advances (Return synonym too).
	v.handleKey("Enter")
	v.handleKey("Return")
	// Escape while focused defocuses but keeps the panel open.
	v.focus = gitFieldURL
	if !v.handleKey("Escape") || v.focus != gitFieldNone || !v.open {
		t.Fatal("Escape while focused should defocus but keep the panel open")
	}
	// A non-editing key while focused is still swallowed by the modal field.
	v.focus = gitFieldURL
	if !v.handleKey("ArrowLeft") {
		t.Fatal("a focused field should swallow other keys")
	}
	// Open + unfocused + a non-Escape key: not consumed by the field.
	v.focus = gitFieldNone
	if v.handleKey("Backspace") {
		t.Fatal("unfocused field should not consume an editing key")
	}
}

func TestGitDrawEveryPhase(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	buf := make([]byte, testW*testH*4)

	f.files = []string{"main.tex", "a.tex", "b.tex"}
	f.fileData["main.tex"] = "x"
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Ahead: 1, Clean: false, DirtyFile: 1}
	f.log = []GitCommitInfo{{Hash: "abcdef1234", Subject: "seed", Author: "seed"}}
	s.GitClone(nil) // repo open, main.tex loaded, >1 tex → picker, log present

	phases := []struct {
		name  string
		setup func()
	}{
		{"launcher-closed", func() { v.open = false }},
		{"open-with-picker-log", func() { v.open = true; v.focus = gitFieldNone }},
		{"token-focused", func() { v.focus = gitFieldToken; v.token.Set("secret") }},
		{"busy", func() { v.focus = gitFieldNone; v.busy.Set(true) }},
		{"error", func() { v.busy.Set(false); v.errMsg.Set("boom") }},
		{"notice", func() { v.errMsg.Set(""); v.notice.Set("Pushed to origin.") }},
	}
	for _, ph := range phases {
		ph.setup()
		s.Draw(buf) // must not panic; draws the launcher + (when open) the panel
	}
}

// TestGitDrawNarrowClamped exercises the panel-clamp draw path on a small window.
func TestGitDrawNarrowClamped(t *testing.T) {
	s, _, _ := withGit(t)
	s.Resize(360, 400)
	s.git.open = true
	buf := make([]byte, 360*400*4)
	s.Draw(buf)
}
