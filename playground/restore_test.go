// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"fmt"
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
	f.pullErr = fmt.Errorf("dial tcp: no route to host: %w", errGitTransport)

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

func TestARejectedTokenIsNotCalledOffline(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = true
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}restored\end{document}`
	f.pullErr = fmt.Errorf("refused: %w", errGitAuth)

	var gotErr error
	s.BootClone(func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("the boot still succeeded on its own terms: %v", gotErr)
	}
	// A problem the reader has to fix keeps the error line it earned. Calling it
	// "offline" would send them hunting a network fault that is not there.
	if e := s.git.errMsg.Get(); e == "" {
		t.Fatal("a rejected token left no error line at all")
	}
	if n := s.git.notice.Get(); strings.Contains(strings.ToLower(n), "offline") {
		t.Fatalf("notice = %q — a rejected token is not a network failure", n)
	}
}

// Forgetting is about the NEXT visit. A reader asking to drop a stored copy is
// not asking to lose the document they are editing, so this must never behave
// like a destructive action wearing a harmless label.
func TestForgetLeavesTheOpenWorkspaceAlone(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = true
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}restored\end{document}`
	s.BootClone(nil)

	before := s.Source()
	if !strings.Contains(before, "restored") {
		t.Fatalf("setup: the workspace never opened (%q)", before)
	}

	forgot := false
	s.GitForget(func() { forgot = true })
	if !forgot {
		t.Fatal("GitForget never called back")
	}
	if f.forgetCalls != 1 {
		t.Fatalf("the backend was asked to forget %d times, want once", f.forgetCalls)
	}
	if !s.SidebarOpen() {
		t.Error("forgetting closed the workspace")
	}
	if got := s.Source(); got != before {
		t.Errorf("forgetting changed the open document:\n got %q\nwant %q", got, before)
	}
	if !f.hasRepo {
		t.Error("forgetting closed the repository")
	}
	if n := s.git.notice.Get(); !strings.Contains(strings.ToLower(n), "saved copy") {
		t.Errorf("notice = %q, want it to say the saved copy was dropped", n)
	}
	// It is not a git operation against the remote.
	if f.cloneCalls != 0 {
		t.Errorf("forgetting re-cloned %d times; it must only affect the next visit", f.cloneCalls)
	}
}

// A native build has no browser storage, so there is nothing saved to drop —
// but the call must still complete rather than leaving a caller waiting.
func TestForgetOnABuildWithoutBrowserGit(t *testing.T) {
	called := false
	nopGitBackend{}.Forget(gitConfig{}, func() { called = true })
	if !called {
		t.Fatal("the no-op backend never reported back")
	}
}
