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
}

// Clone opens url@branch with the given identity, replacing any open repo. The
// whole remote URL rides in BaseURL (empty repoPath), matching the panel's two
// documented URL forms; every remote authenticates the same way (PAT-as-password).
func (s *browsergitSession) Clone(url, branch, token, author, email string) error {
	client := browsergit.New(browsergit.Options{
		BaseURL:  url,
		Token:    token,
		Author:   author,
		Email:    email,
		Provider: "generic",
	})
	repo, err := client.Clone(context.Background(), "", branch, 1)
	if err != nil {
		return err
	}
	s.repo = repo
	return nil
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
