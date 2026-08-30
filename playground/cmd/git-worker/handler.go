// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Command git-worker is the playground's OFF-THREAD remote-git client. It runs as
// a Web Worker executing its own git-worker.wasm and is the ONLY binary that
// imports internal/browsergit (go-git) — so the main playground.wasm stays free of
// go-git and streams the base app without it. The worker holds one long-lived
// browsergit session in memory across calls and answers the main app's [gitrpc]
// requests over postMessage.
//
// This file is the tagless core: the op dispatch and the browsergit-error→[gitrpc]
// code mapping, both exercised by a native `go test` behind the [gitSession] seam
// (handler_test.go). The syscall/js glue and the real browsergit session are in
// the js/wasm-only files (main.go, session_js.go), proven by the headless
// two-wasm e2e.
package main

import (
	"errors"
	"path"
	"strings"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// gitSession is the remote-git surface the handler drives. The live implementation
// (session_js.go) wraps a [browsergit.Client]/Repo; tests inject a fake so the
// dispatch + error mapping are covered natively without a browser or a network.
type gitSession interface {
	// Clone opens url@branch with the given identity into memory (a no-op path
	// prefix; the whole URL rides in url). It replaces any previously open repo.
	Clone(url, branch, token, author, email string) error
	// Restore reopens a repository this browser saved on an earlier visit,
	// without contacting the remote, and reports whether one was found. A false
	// with no error means nothing was stored for this url@branch.
	Restore(url, branch, token, author, email string) (bool, error)
	// Persist writes the open repository down so a later visit can Restore it.
	// It is best-effort by contract: a browser can refuse the write (quota,
	// private mode, eviction) and that must not fail the git operation that
	// just succeeded.
	Persist()
	// List returns the working-tree file paths (slash-relative), .git pruned.
	List() ([]string, error)
	// ReadFile returns one working-tree file's contents.
	ReadFile(p string) (string, error)
	// WriteFile writes one working-tree file (no commit).
	WriteFile(p, content string) error
	// Commit stages every change and commits with the configured identity.
	Commit(message string) error
	// Stage stages every change (git add) without committing.
	Stage() error
	// Pull fast-forwards the working tree against origin.
	Pull() error
	// Push pushes the tracked branch to origin.
	Push() error
	// Status snapshots the branch/divergence/dirty state.
	Status() (gitrpc.Status, error)
	// Log returns up to limit commits, newest first (limit <= 0 → the client cap).
	Log(limit int) ([]gitrpc.Commit, error)
	// HasRepo reports whether a repository is currently open.
	HasRepo() bool
}

// handler dispatches [gitrpc] requests onto a single [gitSession]. It serialises
// calls (browsergit worktrees are not safe for concurrent use); the worker's
// message pump also hands it one request at a time.
type handler struct {
	s gitSession
}

// newHandler builds a handler over s.
func newHandler(s gitSession) *handler { return &handler{s: s} }

// handle decodes a request wire string, dispatches it and encodes the reply. A
// malformed request maps to a bad-request reply (id 0, since the id could not be
// read).
func (h *handler) handle(reqJSON string) string {
	req, err := gitrpc.DecodeRequest(reqJSON)
	if err != nil {
		return gitrpc.EncodeReply(gitrpc.ErrorReply(0, gitrpc.CodeBadRequest, err.Error()))
	}
	return gitrpc.EncodeReply(h.dispatch(req))
}

// dispatch runs one request's op against the session.
func (h *handler) dispatch(req gitrpc.Request) gitrpc.Reply {
	switch req.Op {
	case gitrpc.OpClone:
		return h.clone(req)
	case gitrpc.OpRestore:
		return h.restore(req)
	case gitrpc.OpList:
		return h.list(req)
	case gitrpc.OpReadFile:
		return h.readFile(req)
	case gitrpc.OpWriteFile:
		return h.writeFile(req)
	case gitrpc.OpStatus:
		return h.status(req)
	case gitrpc.OpCommit:
		return h.commit(req)
	case gitrpc.OpStage:
		return h.stage(req)
	case gitrpc.OpPull:
		return h.pull(req)
	case gitrpc.OpPush:
		return h.push(req)
	case gitrpc.OpLog:
		return h.log(req)
	default:
		return gitrpc.ErrorReply(req.ID, gitrpc.CodeBadRequest, "unknown op: "+req.Op)
	}
}

// requireRepo returns a no-repo error reply (and false) when nothing is cloned.
func (h *handler) requireRepo(id int) (gitrpc.Reply, bool) {
	if !h.s.HasRepo() {
		return gitrpc.ErrorReply(id, gitrpc.CodeNoRepo, "no repository cloned"), false
	}
	return gitrpc.Reply{}, true
}

// fail wraps a session error into a coded error reply.
func (h *handler) fail(id int, err error) gitrpc.Reply {
	return gitrpc.ErrorReply(id, codeForErr(err), err.Error())
}

func (h *handler) clone(req gitrpc.Request) gitrpc.Reply {
	a := req.Args
	if err := h.s.Clone(a.URL, a.Branch, a.Token, a.Author, a.Email); err != nil {
		return h.fail(req.ID, err)
	}
	return h.mutatingReply(req.ID, true)
}

// restore reopens a saved repository. Finding none is an ordinary answer, not a
// failure: a first visit has nothing stored, and the caller clones instead.
func (h *handler) restore(req gitrpc.Request) gitrpc.Reply {
	a := req.Args
	found, err := h.s.Restore(a.URL, a.Branch, a.Token, a.Author, a.Email)
	if err != nil {
		return h.fail(req.ID, err)
	}
	if !found {
		reply := gitrpc.OKReply(req.ID)
		return reply
	}
	reply := h.mutatingReply(req.ID, true)
	reply.Restored = reply.OK
	return reply
}

func (h *handler) list(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	files, err := h.s.List()
	if err != nil {
		return h.fail(req.ID, err)
	}
	reply := gitrpc.OKReply(req.ID)
	reply.HasRepo = true
	reply.Files = files
	return reply
}

func (h *handler) readFile(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	data, err := h.s.ReadFile(req.Args.Path)
	if err != nil {
		return h.fail(req.ID, err)
	}
	reply := gitrpc.OKReply(req.ID)
	reply.HasRepo = true
	reply.Content = data
	return reply
}

func (h *handler) writeFile(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	if err := h.s.WriteFile(req.Args.Path, req.Args.Content); err != nil {
		return h.fail(req.ID, err)
	}
	// An uncommitted edit is exactly the work a reload must not throw away, so
	// a plain write is saved like any other change. This reply deliberately
	// carries no status or log — nothing about the history moved — so it does
	// not go through mutatingReply and saves for itself.
	h.s.Persist()
	reply := gitrpc.OKReply(req.ID)
	reply.HasRepo = true
	return reply
}

func (h *handler) status(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	st, err := h.s.Status()
	if err != nil {
		return h.fail(req.ID, err)
	}
	reply := gitrpc.OKReply(req.ID)
	reply.HasRepo = true
	reply.Status = &st
	return reply
}

func (h *handler) commit(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	a := req.Args
	if err := h.s.WriteFile(a.Path, a.Content); err != nil {
		return h.fail(req.ID, err)
	}
	if err := h.s.Commit(a.Message); err != nil {
		return h.fail(req.ID, err)
	}
	return h.mutatingReply(req.ID, false)
}

// stage writes the file then stages it (git add) without committing, so the
// fused status reply reports the file as staged. It mirrors commit's write step
// but stops short of the commit.
func (h *handler) stage(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	a := req.Args
	if err := h.s.WriteFile(a.Path, a.Content); err != nil {
		return h.fail(req.ID, err)
	}
	if err := h.s.Stage(); err != nil {
		return h.fail(req.ID, err)
	}
	return h.mutatingReply(req.ID, false)
}

func (h *handler) pull(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	if err := h.s.Pull(); err != nil {
		return h.fail(req.ID, err)
	}
	return h.mutatingReply(req.ID, true)
}

func (h *handler) push(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	if err := h.s.Push(); err != nil {
		return h.fail(req.ID, err)
	}
	return h.mutatingReply(req.ID, false)
}

func (h *handler) log(req gitrpc.Request) gitrpc.Reply {
	if r, ok := h.requireRepo(req.ID); !ok {
		return r
	}
	commits, err := h.s.Log(req.Args.Limit)
	if err != nil {
		return h.fail(req.ID, err)
	}
	reply := gitrpc.OKReply(req.ID)
	reply.HasRepo = true
	reply.Log = commits
	return reply
}

// mutatingReply builds the fused reply the main app folds into its read-model
// after a clone/pull/commit/push: always the status + log, and (for clone/pull)
// the working-tree file list plus the .tex file contents, so the panel loads the
// source without a second round-trip.
func (h *handler) mutatingReply(id int, withFiles bool) gitrpc.Reply {
	// Every caller of this has just changed the repository, so this is the one
	// place the saved copy is refreshed. Best-effort by contract: a browser that
	// refuses the write must not turn a successful commit into a failed one.
	h.s.Persist()

	reply := gitrpc.OKReply(id)
	reply.HasRepo = true

	st, err := h.s.Status()
	if err != nil {
		return h.fail(id, err)
	}
	reply.Status = &st

	commits, err := h.s.Log(0)
	if err != nil {
		return h.fail(id, err)
	}
	reply.Log = commits

	if !withFiles {
		return reply
	}

	files, err := h.s.List()
	if err != nil {
		return h.fail(id, err)
	}
	reply.Files = files

	contents := map[string]string{}
	for _, f := range files {
		if !isTextSource(f) {
			continue
		}
		data, err := h.s.ReadFile(f)
		if err != nil {
			return h.fail(id, err)
		}
		contents[f] = data
	}
	reply.Contents = contents
	return reply
}

// isTeX reports whether f is a .tex file (case-insensitive extension).
func isTeX(f string) bool { return strings.EqualFold(path.Ext(f), ".tex") }

// textSourceExts are the working-tree extensions whose contents the clone/pull
// reply pre-caches, so the workspace sidebar can open any of them into the editor
// without a second round-trip: the LaTeX source family plus plain text/markdown.
var textSourceExts = map[string]bool{
	".tex": true, ".sty": true, ".cls": true, ".bib": true, ".bst": true,
	".def": true, ".ltx": true, ".tikz": true, ".txt": true, ".md": true,
}

// isTextSource reports whether f is a text file the sidebar can open into the
// editor (case-insensitive extension), so its contents ride along in the reply.
func isTextSource(f string) bool { return textSourceExts[strings.ToLower(path.Ext(f))] }

// codeForErr maps a browsergit error onto a stable [gitrpc] code. Everything the
// browsergit client can return is classified into one of its sentinels, so the
// default (transport) is the catch-all for a novel failure.
func codeForErr(err error) string {
	switch {
	case errors.Is(err, browsergit.ErrAuth):
		return gitrpc.CodeAuth
	case errors.Is(err, browsergit.ErrNonFastForward):
		return gitrpc.CodeNonFastForward
	case errors.Is(err, browsergit.ErrRepoNotFound):
		return gitrpc.CodeRepoNotFound
	case errors.Is(err, browsergit.ErrNotExist):
		return gitrpc.CodeNotExist
	default:
		return gitrpc.CodeTransport
	}
}
