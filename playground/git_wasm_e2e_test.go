// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground_test

// The in-browser half of the Git-panel e2e proof. Compiled to GOOS=js
// GOARCH=wasm and run under Node with the argv0 spoof (see git_e2e_test.go),
// this drives the REAL Git panel — [playground.State] with the real browsergit
// backend installed by EnableGit — through the whole user flow over the WHATWG
// Fetch RoundTripper (the browser transport):
//
//	configure URL/branch → Clone → assert the seeded .tex loaded into the
//	SOURCE editor → edit it → Commit → Push
//
// against a live CORS-enabled git origin. The native orchestrator then witnesses
// the pushed commit by re-cloning the bare repo. This file never builds on the
// native lane, so it contributes nothing to native line coverage; its evidence
// is the console output + the witness repo.

import (
	"os"
	"strings"
	"testing"
	"time"

	playground "github.com/go-tex/go-tex.github.io/playground"
)

// GitPanelWasmMarker is the distinctive text the wasm client appends to the
// loaded .tex and commits, asserted by the native witness to have reached the
// bare origin. Exported-style constant kept beside its user.
const GitPanelWasmMarker = "% edited by the Git panel over the browser Fetch RoundTripper"

func TestGitPanelWasmCycle(t *testing.T) {
	base := os.Getenv("GITPANEL_REPO_URL")
	if base == "" {
		t.Skip("GITPANEL_REPO_URL unset — run via the git-panel e2e orchestrator")
	}

	playground.SetupText(1)
	s := playground.NewState(1000, 720, false)
	s.CompilePending()
	s.EnableGit(func() {}) // install the real browsergit backend; repaint is a no-op

	s.SetGitURL(base)
	s.SetGitBranch("main")
	s.SetGitAuthor("wasm client")
	s.SetGitEmail("wasm@browser.local")
	s.SetGitCommitMessage("Git panel commit over Fetch")

	// Clone: the panel loads the primary .tex into the source editor.
	if err := drive(t, s.GitClone); err != nil {
		t.Fatalf("CLONE_FAIL: %v", err)
	}
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("LOADED_FAIL: loaded=%q, want main.tex", s.GitLoadedPath())
	}
	if !strings.Contains(s.Source(), "SEED-TEX") {
		t.Fatalf("SOURCE_FAIL: editor did not get the seeded .tex: %.60q", s.Source())
	}
	t.Logf("CLONE_OK (Fetch RoundTripper): loaded %s into the source editor", s.GitLoadedPath())

	// Edit the loaded document (a real buffer change the commit must capture).
	s.SetSource(s.Source() + "\n" + GitPanelWasmMarker + "\n")

	// Commit + Push.
	if err := drive(t, s.GitCommit); err != nil {
		t.Fatalf("COMMIT_FAIL: %v", err)
	}
	t.Log("COMMIT_OK")
	if err := drive(t, s.GitPush); err != nil {
		t.Fatalf("PUSH_FAIL: %v", err)
	}
	// This exact line is what the orchestrator greps for.
	t.Log("GIT_PANEL_WASM_CYCLE_OK")
}

// drive runs one async panel action (GitClone/GitCommit/GitPush) and blocks
// until its done callback fires or a deadline elapses. Blocking on the channel
// yields to the page event loop, letting the backend goroutine + its Fetch run.
func drive(t *testing.T, action func(done func(error))) error {
	t.Helper()
	ch := make(chan error, 1)
	action(func(err error) { ch <- err })
	select {
	case err := <-ch:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the git action")
		return nil
	}
}
