// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"sync"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// This file is the main app's HALF of the off-thread git split: the [gitBackend]
// that drives a separate git-worker.wasm over the [gitrpc] message protocol,
// WITHOUT importing internal/browsergit (so go-git is dead-code-eliminated out of
// playground.wasm — the whole point of the split). It is tagless and browser-free
// so the RPC translation, the reply cache and the error mapping are all covered
// by a native `go test` behind a fake [workerTransport]; the real syscall/js
// transport (a Web Worker + postMessage) lives in git_js.go behind this seam.
//
// # Why the reads are cached, not round-tripped
//
// The worker holds the cloned repository in ITS memory; the main app keeps only a
// small cache — the .tex file contents plus the last status/log — refreshed from
// every mutating op's reply. So [gitBackend]'s synchronous reads (ReadFile,
// Status, Log, HasRepo) never cross the Worker boundary and can run on any
// goroutine, including the UI event-loop goroutine (the file picker), with no risk
// of a postMessage deadlock. Only Clone/Pull/Commit/Push round-trip, and they
// each run on their own goroutine where blocking on the worker reply is safe.

// git error sentinels for the MAIN app. They replace the browsergit.Err* the app
// used to import: the worker classifies every failure into a stable [gitrpc] code
// and the main app maps that code back onto one of these, so playground.wasm needs
// no browsergit dependency to render a clear panel line.
var (
	errGitAuth           = errors.New("git: authentication failed")
	errGitNonFastForward = errors.New("git: non-fast-forward push rejected")
	errGitRepoNotFound   = errors.New("git: repository not found")
	errGitTransport      = errors.New("git: transport error")
	errGitNotExist       = errors.New("git: file does not exist")
)

// codeToError maps a gitrpc error code (plus the worker's human detail) back onto
// a main-app sentinel. An unknown code keeps the worker's detail verbatim so a
// novel failure still reads clearly on the panel.
func codeToError(code, detail string) error {
	switch code {
	case gitrpc.CodeAuth:
		return errGitAuth
	case gitrpc.CodeNonFastForward:
		return errGitNonFastForward
	case gitrpc.CodeRepoNotFound:
		return errGitRepoNotFound
	case gitrpc.CodeTransport:
		return errGitTransport
	case gitrpc.CodeNotExist:
		return errGitNotExist
	case gitrpc.CodeNoRepo:
		return errNoGitRepo
	default:
		if detail == "" {
			detail = "git: worker error"
		}
		return errors.New(detail)
	}
}

// argsFromConfig maps the panel config onto the RPC clone arguments, trimming the
// surrounding whitespace the old browsergit.Options mapping used to. The whole
// remote URL rides in URL (both documented forms reach the origin verbatim); the
// worker authenticates every remote the same way (PAT-as-password).
func argsFromConfig(c gitConfig) gitrpc.Args {
	return gitrpc.Args{
		URL:    strings.TrimSpace(c.URL),
		Branch: strings.TrimSpace(c.Branch),
		Token:  strings.TrimSpace(c.Token),
		Author: strings.TrimSpace(c.Author),
		Email:  strings.TrimSpace(c.Email),
	}
}

// workerTransport is the seam over the Web Worker. Spawn starts the worker + its
// wasm load (non-blocking, fired when the panel opens so the download overlaps the
// user filling the form). Call posts one request and blocks until the matching
// reply arrives; it is only ever invoked from an op-goroutine, so the block yields
// to the page event loop instead of freezing the UI.
type workerTransport interface {
	Spawn()
	Call(req gitrpc.Request) gitrpc.Reply
}

// workerGitBackend is the [gitBackend] that drives the git worker. It caches the
// small read-model (contents/status/log/hasRepo) the panel renders so the
// interface's synchronous reads never cross the worker boundary.
type workerGitBackend struct {
	t workerTransport

	mu       sync.Mutex
	hasRepo  bool
	contents map[string]string
	status   gitStatus
	statusOK bool
	log      []GitCommitInfo
}

// newWorkerGitBackend builds the backend over t with an empty cache.
func newWorkerGitBackend(t workerTransport) *workerGitBackend {
	return &workerGitBackend{t: t, contents: map[string]string{}}
}

// Prewarm spawns the worker (and starts its wasm download) ahead of the first
// operation. The panel calls it when it opens.
func (b *workerGitBackend) Prewarm() { b.t.Spawn() }

// apply folds a mutating op's reply into the cached read-model under the lock.
func (b *workerGitBackend) apply(reply gitrpc.Reply) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hasRepo = true
	for p, c := range reply.Contents {
		b.contents[p] = c
	}
	if reply.Status != nil {
		b.status = gitStatus{
			Branch:    reply.Status.Branch,
			Ahead:     reply.Status.Ahead,
			Behind:    reply.Status.Behind,
			Clean:     reply.Status.Clean,
			DirtyFile: reply.Status.DirtyFile,
			Changes:   changesFrom(reply.Status.Changes),
		}
		b.statusOK = true
	}
	b.log = commitsFrom(reply.Log)
}

// changesFrom converts the RPC per-file dirty entries to the app's gitFileChange
// the sidebar badges rows from.
func changesFrom(in []gitrpc.Change) []gitFileChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]gitFileChange, 0, len(in))
	for _, c := range in {
		out = append(out, gitFileChange{Path: c.Path, Status: c.Status})
	}
	return out
}

// commitsFrom converts the RPC log lines to the panel's GitCommitInfo.
func commitsFrom(in []gitrpc.Commit) []GitCommitInfo {
	if len(in) == 0 {
		return nil
	}
	out := make([]GitCommitInfo, 0, len(in))
	for _, c := range in {
		out = append(out, GitCommitInfo{Hash: c.Hash, Subject: c.Subject, Author: c.Author})
	}
	return out
}

// Clone clones cfg's remote in the worker and hands back the working-tree file
// list; the reply also seeds the contents/status/log cache.
func (b *workerGitBackend) Clone(cfg gitConfig, done func([]string, error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpClone, Args: argsFromConfig(cfg)})
		if !reply.OK {
			done(nil, codeToError(reply.Code, reply.Error))
			return
		}
		b.apply(reply)
		done(reply.Files, nil)
	}()
}

// Restore asks the worker to reopen a saved repository. Nothing saved is an
// ordinary answer — found is false with no error, and the caller clones.
func (b *workerGitBackend) Restore(cfg gitConfig, done func([]string, bool, error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpRestore, Args: argsFromConfig(cfg)})
		if !reply.OK {
			done(nil, false, codeToError(reply.Code, reply.Error))
			return
		}
		if !reply.Restored {
			done(nil, false, nil)
			return
		}
		b.apply(reply)
		done(reply.Files, true, nil)
	}()
}

// Pull fast-forwards the open repo in the worker and refreshes the cache.
func (b *workerGitBackend) Pull(done func(error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpPull})
		if !reply.OK {
			done(codeToError(reply.Code, reply.Error))
			return
		}
		b.apply(reply)
		done(nil)
	}()
}

// Commit writes content to path in the worker's working tree and commits it,
// then folds the fresh status/log into the cache (and the new content, so a later
// re-pick of the same file shows what was committed).
func (b *workerGitBackend) Commit(path, content, message string, done func(error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpCommit, Args: gitrpc.Args{Path: path, Content: content, Message: message}})
		if !reply.OK {
			done(codeToError(reply.Code, reply.Error))
			return
		}
		b.mu.Lock()
		b.contents[path] = content
		b.mu.Unlock()
		b.apply(reply)
		done(nil)
	}()
}

// Stage writes content to path in the worker's working tree and stages it (git
// add) without committing, then folds the fresh status/log into the cache. The
// new content is cached too (like Commit), so the sidebar's active-file dirty
// overlay clears and the file's "staged" badge shows through.
func (b *workerGitBackend) Stage(path, content string, done func(error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpStage, Args: gitrpc.Args{Path: path, Content: content}})
		if !reply.OK {
			done(codeToError(reply.Code, reply.Error))
			return
		}
		b.mu.Lock()
		b.contents[path] = content
		b.mu.Unlock()
		b.apply(reply)
		done(nil)
	}()
}

// WriteFiles writes each path→content to the worker's working tree (OpWriteFile,
// no commit/stage) and caches each written content, so the sidebar's per-file
// dirty overlay clears the moment the flush lands. It is the multi-file write-back
// a stage/commit runs first; the first write error aborts and is reported. An
// empty set writes nothing and reports success.
func (b *workerGitBackend) WriteFiles(files map[string]string, done func(error)) {
	go func() {
		for p, c := range files {
			reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpWriteFile, Args: gitrpc.Args{Path: p, Content: c}})
			if !reply.OK {
				done(codeToError(reply.Code, reply.Error))
				return
			}
			b.mu.Lock()
			b.contents[p] = c
			b.mu.Unlock()
		}
		done(nil)
	}()
}

// Push pushes the open branch and refreshes the status/log cache.
func (b *workerGitBackend) Push(done func(error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpPush})
		if !reply.OK {
			done(codeToError(reply.Code, reply.Error))
			return
		}
		b.apply(reply)
		done(nil)
	}()
}

// ReadFile serves a working-tree file from the cache the last clone/pull filled.
// The worker also implements a discrete readFile op, but the live panel reads from
// the cache so a file-pick (which runs on the UI goroutine) never blocks on the
// worker.
func (b *workerGitBackend) ReadFile(path string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.contents[path]; ok {
		return []byte(c), nil
	}
	return nil, errGitNotExist
}

// Status returns the cached branch/divergence snapshot.
func (b *workerGitBackend) Status() (gitStatus, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, b.statusOK
}

// Log returns the cached recent commits (the limit is applied worker-side).
func (b *workerGitBackend) Log(int) []GitCommitInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.log
}

// HasRepo reports whether a clone has succeeded (from the cache, no round-trip).
func (b *workerGitBackend) HasRepo() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasRepo
}
