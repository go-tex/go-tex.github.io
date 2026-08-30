// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"testing"
)

// A browser that already holds the repository should not fetch it again, and a
// remote it cannot reach should not empty a workspace it already has. These pin
// the four ways a boot can go.

func TestBootRestoresInsteadOfCloning(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = true
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}restored\end{document}`

	var gotErr error
	s.BootClone(func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("boot: %v", gotErr)
	}
	if f.restoreCalls != 1 {
		t.Fatalf("restore was tried %d times, want once", f.restoreCalls)
	}
	if f.cloneCalls != 0 {
		t.Fatalf("the repository was cloned %d times although this browser already held it", f.cloneCalls)
	}
	if !s.SidebarOpen() {
		t.Error("the workspace must open once the saved repository is reopened")
	}
	if got := s.Source(); !strings.Contains(got, "restored") {
		t.Errorf("the restored .tex was not loaded: %q", got)
	}
}

func TestBootClonesWhenNothingIsSaved(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}cloned\end{document}`

	s.BootClone(nil)
	if f.restoreCalls != 1 {
		t.Fatalf("restore was tried %d times, want once before falling back", f.restoreCalls)
	}
	if f.cloneCalls != 1 {
		t.Fatalf("clone ran %d times, want once — a first visit has nothing saved", f.cloneCalls)
	}
	if got := s.Source(); !strings.Contains(got, "cloned") {
		t.Errorf("the cloned .tex was not loaded: %q", got)
	}
}

func TestAnUnreachableRemoteDoesNotEmptyASavedWorkspace(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = true
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}restored\end{document}`
	f.pullErr = errors.New("dial tcp: no route to host")

	var gotErr error
	s.BootClone(func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("a failed refresh must not fail the boot: %v", gotErr)
	}
	if !s.SidebarOpen() {
		t.Fatal("the workspace closed although its files were already in hand")
	}
	if got := s.Source(); !strings.Contains(got, "restored") {
		t.Errorf("the restored .tex was dropped: %q", got)
	}
	if n := s.git.notice.Get(); !strings.Contains(strings.ToLower(n), "saved copy") {
		t.Errorf("notice = %q, want it to say the saved copy is what is on screen", n)
	}
	// The red error line must be CLEARED: the workspace opened and is usable,
	// and a failure shown ahead of everything else says the opposite.
	if e := s.git.errMsg.Get(); e != "" {
		t.Errorf("error line = %q, want it cleared — only the update failed, not the boot", e)
	}
	// The notice shares the 264 px column with the file tree and does not wrap.
	if n := s.git.notice.Get(); len(n) > 40 {
		t.Errorf("notice is %d characters (%q); anything longer is clipped mid-word", len(n), n)
	}
	if s.BootNotice() != "" {
		t.Errorf("boot notice = %q, want none — the samples DID arrive", s.BootNotice())
	}
}

func TestAnUnreadableSavedWorkspaceIsSaidOutLoud(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreErr = errors.New("the saved workspace could not be reopened")
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}cloned\end{document}`

	s.BootClone(nil)
	if f.cloneCalls != 1 {
		t.Fatalf("clone ran %d times, want once — an unreadable copy still has to be replaced", f.cloneCalls)
	}
	if got := s.Source(); !strings.Contains(got, "cloned") {
		t.Errorf("the fresh clone was not loaded: %q", got)
	}
}

func TestRestoringIsNotCalledFetching(t *testing.T) {
	s := newTestState(t, false)
	s.git.op.Set("Restoring")
	msg, caption := s.sidebar.loadingText()
	if msg != "Restoring…" {
		t.Fatalf("message = %q, want %q", msg, "Restoring…")
	}
	if strings.Contains(strings.ToLower(caption), "fetch") {
		t.Fatalf("caption = %q — restoring reads this browser's own copy and contacts no remote", caption)
	}
}

func TestBootIsIdempotent(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = true
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}restored\end{document}`
	s.BootClone(nil)

	// A second boot with a repository already open must report success without
	// touching either the store or the remote — the host fires it on every
	// engine-ready, and re-opening would discard whatever is being edited.
	before := f.restoreCalls
	var gotErr error
	called := false
	s.BootClone(func(err error) { gotErr, called = err, true })
	if !called {
		t.Fatal("the second boot never reported back")
	}
	if gotErr != nil {
		t.Fatalf("the second boot reported %v", gotErr)
	}
	if f.restoreCalls != before || f.cloneCalls != 0 {
		t.Fatalf("the second boot went to work anyway: restore=%d clone=%d", f.restoreCalls, f.cloneCalls)
	}
}
