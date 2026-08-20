// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground

import (
	"context"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
)

// This is the browser half of the remote-git feature — the glue behind the
// [gitBackend] seam in git.go. It runs a [browsergit.Client] against a CORS
// origin over the WHATWG Fetch RoundTripper (the browser's net/http transport),
// holding the cloned repository entirely in memory. It is excluded from the
// native `go test` coverage gate (browsergit itself is proven natively + under a
// Node-Fetch wasm e2e); its in-app proof is the headless git-panel run driven by
// git_e2e_test.go.
//
// # Threading
//
// WebAssembly runs Go on the page's single thread and yields to JavaScript only
// at blocking points, so the network goroutines here, the done callbacks and the
// UI event handlers never touch the panel's observables at the same instant —
// the single-goroutine contract mvvm.Observable asks for. Each network op runs
// in its own goroutine (a Fetch round-trip blocks the goroutine, not the page)
// and reports back through done, exactly as collab_js.go does for WebRTC.

// EnableGit installs the real browsergit-backed backend on the playground's Git
// affordance. The wasm driver calls it once, after building the State, passing
// its repaint function.
func (s *State) EnableGit(repaint func()) {
	s.git.attach(&browserGitBackend{}, repaint)
}

// browserGitBackend is the live [gitBackend]: one browsergit session holding the
// cloned in-memory repo between calls.
type browserGitBackend struct {
	repo *browsergit.Repo
}

// Clone opens cfg's remote into memory and reports the working-tree file list.
func (b *browserGitBackend) Clone(cfg gitConfig, done func([]string, error)) {
	go func() {
		client := browsergit.New(cfg.options())
		// The whole remote URL rides in BaseURL, so repoPath is empty; Depth 1 is
		// forced by browsergit (the browser client only ever wants the tip).
		repo, err := client.Clone(context.Background(), "", cfg.Branch, 1)
		if err != nil {
			done(nil, err)
			return
		}
		b.repo = repo
		files, err := repo.List()
		done(files, err)
	}()
}

// Pull fast-forwards the open repo against origin.
func (b *browserGitBackend) Pull(done func(error)) {
	go func() {
		if b.repo == nil {
			done(errNoBrowserGit)
			return
		}
		done(b.repo.Pull(context.Background()))
	}()
}

// Commit writes content to path in the working tree, then commits it.
func (b *browserGitBackend) Commit(path, content, message string, done func(error)) {
	go func() {
		if b.repo == nil {
			done(errNoBrowserGit)
			return
		}
		if err := b.repo.WriteFile(path, []byte(content)); err != nil {
			done(err)
			return
		}
		_, err := b.repo.Commit(message)
		done(err)
	}()
}

// Push pushes the open branch to origin.
func (b *browserGitBackend) Push(done func(error)) {
	go func() {
		if b.repo == nil {
			done(errNoBrowserGit)
			return
		}
		done(b.repo.Push(context.Background()))
	}()
}

// ReadFile reads a working-tree file from the in-memory tree.
func (b *browserGitBackend) ReadFile(path string) ([]byte, error) {
	if b.repo == nil {
		return nil, errNoBrowserGit
	}
	return b.repo.ReadFile(path)
}

// Status snapshots the open repo's branch + divergence.
func (b *browserGitBackend) Status() (gitStatus, bool) {
	if b.repo == nil {
		return gitStatus{}, false
	}
	st, err := b.repo.Status()
	if err != nil {
		return gitStatus{}, false
	}
	return gitStatus{
		Branch:    st.Branch,
		Ahead:     st.Ahead,
		Behind:    st.Behind,
		Clean:     st.Clean,
		DirtyFile: len(st.Changes),
	}, true
}

// Log returns up to limit recent commits, newest first.
func (b *browserGitBackend) Log(limit int) []GitCommitInfo {
	if b.repo == nil {
		return nil
	}
	commits, err := b.repo.Log(limit)
	if err != nil {
		return nil
	}
	out := make([]GitCommitInfo, 0, len(commits))
	for _, c := range commits {
		out = append(out, GitCommitInfo{Hash: c.Hash, Subject: c.Subject, Author: c.Author})
	}
	return out
}

// HasRepo reports whether a repository is currently open.
func (b *browserGitBackend) HasRepo() bool { return b.repo != nil }
