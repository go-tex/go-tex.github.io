// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package main

import (
	"context"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// browsergitSession is the live [gitSession]: one in-memory browsergit repository
// held across RPC calls, talking smart-HTTP to the CORS origin over the worker's
// own Fetch RoundTripper. It is a thin adapter over the already-100%-covered
// browsergit package, excluded from the native coverage gate; its proof is the
// headless two-wasm clone→commit→push run.
type browsergitSession struct {
	repo *browsergit.Repo
	// client and key belong to the repository currently open: the client can
	// reopen it and the key says where it is saved. Both are set by whichever of
	// Clone or Restore opened it, so Persist knows where to write.
	client *browsergit.Client
	key    string
	// progress, when set by the handler around a clone/pull, receives the remote's
	// parsed sideband updates. It is applied to every client this session mints
	// (so a Clone's brand-new client forwards too) via applyProgress.
	progress func(gitrpc.Progress)
}

// SetProgress records the sink the handler wants remote progress forwarded to
// and, when a repository is already open (a Pull reuses its client), attaches it
// at once. A fresh Clone's client picks it up in applyProgress.
func (s *browsergitSession) SetProgress(fn func(gitrpc.Progress)) {
	s.progress = fn
	if s.client != nil {
		s.applyProgress(s.client)
	}
}

// emitPhase pushes a synthetic phase (no fraction) to the session's progress
// sink, if one is attached. Restore has no remote sideband to parse — its wait is
// reading local storage and rebuilding the repo — so it names its own steps this
// way, reusing the panel's clone/pull progress display.
func (s *browsergitSession) emitPhase(phase string) {
	if s.progress != nil {
		// The main app forwards Progress.Line to the panel (git_js.go), so the phase
		// rides in Line; Fraction -1 keeps the bar indeterminate (there is no count).
		s.progress(gitrpc.Progress{Line: phase, Phase: phase, Fraction: -1})
	}
}

// applyProgress points client's sideband callback at the session's current sink,
// translating each raw git line into a parsed [gitrpc.Progress]. A nil sink
// detaches forwarding on that client.
func (s *browsergitSession) applyProgress(client *browsergit.Client) {
	if s.progress == nil {
		client.SetProgress(nil)
		return
	}
	client.SetProgress(func(line string) {
		if s.progress != nil {
			s.progress(gitrpc.ParseProgress(line))
		}
	})
}

// Clone opens url@branch with the given identity, replacing any open repo. The
// whole remote URL rides in BaseURL (empty repoPath), matching the panel's two
// documented URL forms; every remote authenticates the same way (PAT-as-password).
func (s *browsergitSession) Clone(url, branch, token, author, email string) error {
	// Strip a credential out of the remote before it is used for anything —
	// browsergit.New would do it for the client, but the cache key is derived
	// here too, and the same repository opened with and without a credential in
	// its URL must be ONE saved workspace, not two.
	url, token = browsergit.SplitCredential(url, token)
	client := browsergit.New(browsergit.Options{
		BaseURL:  url,
		Token:    token,
		Author:   author,
		Email:    email,
		Provider: "generic",
	})
	// Forward this clone's own progress: the handler set the sink before calling
	// Clone, and this client is brand new, so it must be wired up here.
	s.applyProgress(client)
	repo, err := client.Clone(context.Background(), "", branch, 1)
	if err != nil {
		return err
	}
	s.repo, s.client, s.key = repo, client, workspaceKey(url, branch)
	return nil
}

// Restore reopens the repository this browser saved for url@branch on an earlier
// visit, without contacting the remote.
//
// Finding nothing is an ordinary answer: a first visit, a cleared browser, a
// private window, a context with no storage at all. Only one case is an error —
// an entry that exists and cannot be reopened — because that is the reader's own
// uncommitted work going away, and they should be told rather than left to
// notice. The unreadable entry is dropped so the next visit starts clean.
func (s *browsergitSession) Restore(url, branch, token, author, email string) (bool, error) {
	url, token = browsergit.SplitCredential(url, token)
	client := browsergit.New(browsergit.Options{
		BaseURL:  url,
		Token:    token,
		Author:   author,
		Email:    email,
		Provider: "generic",
	})
	s.applyProgress(client)
	key := workspaceKey(url, branch)
	s.emitPhase("Reading saved copy")
	snap, existed := loadSnapshot(key)
	if !existed {
		return false, nil
	}
	s.emitPhase("Reopening the repository")
	repo, err := client.Open(snap, branch)
	if err != nil {
		dropSnapshot(key)
		return false, errSavedWorkspaceUnreadable
	}
	s.repo, s.client, s.key = repo, client, key
	return true, nil
}

// Forget drops the saved copy for url@branch without touching the open
// repository — see the interface comment for why that separation matters.
func (s *browsergitSession) Forget(url, branch string) {
	url, _ = browsergit.SplitCredential(url, "")
	dropSnapshot(workspaceKey(url, branch))
}

// Persist writes the open repository down for the next visit. Best-effort by
// contract: a browser can refuse the write — quota, private mode, eviction —
// and that must not fail the git operation that just succeeded.
func (s *browsergitSession) Persist() {
	if s.repo == nil || s.key == "" {
		return
	}
	snap, err := s.repo.Snapshot()
	if err != nil {
		return
	}
	saveSnapshot(s.key, snap)
}

func (s *browsergitSession) List() ([]string, error) { return s.repo.List() }

func (s *browsergitSession) ReadFile(p string) (string, error) {
	data, err := s.repo.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *browsergitSession) WriteFile(p, content string) error {
	return s.repo.WriteFile(p, []byte(content))
}

func (s *browsergitSession) Commit(message string) error {
	_, err := s.repo.Commit(message)
	return err
}

func (s *browsergitSession) Stage() error { return s.repo.Stage() }

func (s *browsergitSession) Pull() error { return s.repo.Pull(context.Background()) }

func (s *browsergitSession) Push() error { return s.repo.Push(context.Background()) }

func (s *browsergitSession) Status() (gitrpc.Status, error) {
	st, err := s.repo.Status()
	if err != nil {
		return gitrpc.Status{}, err
	}
	changes := make([]gitrpc.Change, 0, len(st.Changes))
	for _, c := range st.Changes {
		changes = append(changes, gitrpc.Change{Path: c.Path, Status: c.Status})
	}
	return gitrpc.Status{
		Branch:    st.Branch,
		Ahead:     st.Ahead,
		Behind:    st.Behind,
		Clean:     st.Clean,
		DirtyFile: len(st.Changes),
		Changes:   changes,
	}, nil
}

func (s *browsergitSession) Log(limit int) ([]gitrpc.Commit, error) {
	commits, err := s.repo.Log(limit)
	if err != nil {
		return nil, err
	}
	out := make([]gitrpc.Commit, 0, len(commits))
	for _, c := range commits {
		out = append(out, gitrpc.Commit{Hash: c.Hash, Subject: c.Subject, Author: c.Author})
	}
	return out, nil
}

func (s *browsergitSession) HasRepo() bool { return s.repo != nil }
