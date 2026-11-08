// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// workerBootDeadline bounds the wait for the git client's wasm to instantiate.
//
// ⛔⛔ THE WAIT USED TO BE UNBOUNDED, and that is a different defect from a slow
// download. The browser transport blocked on its ready channel until the worker
// said ready, and the one case where it never says so is the one measured in
// #212: the worker script loads, the main thread's wasm boots, the worker asks
// for git-worker.wasm — and then nothing, with no error event, so the error path
// never fires and the channel never closes. Every git call then waits for ever,
// the asset bar stays indeterminate (which is the honest rendering of "cannot
// tell"), and nothing on screen can say what happened.
//
// It was reported as "the git component is always very slow to appear without
// really knowing why". In that case it is not slow, it is hung — and the UI had
// no way to tell those apart either.
//
// 20 seconds because this is a multi-megabyte wasm over whatever connection the
// reader has, and the download reports its own progress: the deadline only has
// to outlast a slow transfer, not estimate one.
const workerBootDeadline = 20 * time.Second

// awaitSignal reports whether ready closed within d.
//
// It is here, in the portable file, rather than beside the Web Worker it serves:
// "how long to wait before calling it hung" is policy, and policy that lives in
// a js-only file can only be tested in a browser. The browser transport supplies
// the channel; this decides.
func awaitSignal(ready <-chan struct{}, d time.Duration) bool {
	select {
	case <-ready:
		return true
	case <-time.After(d):
		return false
	}
}

// workerCallIdleDeadline bounds the SILENCE after a request has been posted, not
// the operation's total time.
//
// ⛔⛔ A FIXED TIMEOUT HERE WOULD BE A WORSE DEFECT THAN THE HANG. Cloning a large
// repository over a slow link legitimately takes minutes, and a call killed at
// its deadline would turn a working clone into a failure the user cannot even
// retry into success. What is never legitimate is a worker that has gone quiet:
// go-git's sideband means clone and pull report progress continuously, so the
// distinction available to us is not fast/slow but SPEAKING/STOPPED.
//
// Sixty seconds of complete silence — no reply, no progress notification —
// therefore reads as stopped. #212's hang is exactly this shape measured from
// the outside: #223's boot deadline does NOT fire on that run, which proves the
// worker instantiated, and then the clone request is posted to a live worker and
// nothing ever comes back. The reply channel was waited on with no deadline at
// all, so the page could only sit there, and the CI driver could only report
// "90000ms exceeded" — a silence, not a diagnosis.
const workerCallIdleDeadline = 60 * time.Second

// awaitReply waits for a terminal reply, treating any progress notification as
// proof of life that restarts the clock. ok is false when nothing at all arrived
// for d.
//
// progress carries one token per notification; the caller may drop tokens when
// they pile up (a non-blocking send into a buffered channel), because the
// question asked here is whether ANY arrived, never how many.
func awaitReply(reply <-chan gitrpc.Reply, progress <-chan struct{}, d time.Duration) (gitrpc.Reply, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case r := <-reply:
			return r, true
		case <-progress:
			// Alive and working. Reset the clock rather than counting against a
			// total, so a long clone that keeps reporting is never cut off.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
		case <-timer.C:
			return gitrpc.Reply{}, false
		}
	}
}

// workerStalledReply is what a call gets when the worker accepted the request and
// then went silent.
//
// It NAMES the operation, and says plainly that it stopped responding rather than
// that it failed: "the clone was refused" and "the clone was never answered" send
// a reader to different places, and only one of them is worth retrying. The code
// is distinct from "worker" (the client never loaded) for the same reason.
func workerStalledReply(id int, op string) gitrpc.Reply {
	name := op
	if name == "" {
		name = "the git operation"
	}
	return gitrpc.Reply{
		ID:   id,
		OK:   false,
		Code: "stalled",
		Error: "the git client stopped responding during " + name +
			" (no progress for " + workerCallIdleDeadline.String() + ")",
	}
}

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
	// Call posts req and blocks for its reply. onProgress, when non-nil, receives
	// the op's server-side progress notifications (clone/pull sideband) ahead of
	// the terminal reply; the ops that do not stream progress pass nil.
	Call(req gitrpc.Request, onProgress func(gitrpc.Progress)) gitrpc.Reply
}

// workerGitBackend is the [gitBackend] that drives the git worker. It caches the
// small read-model (contents/status/log/hasRepo) the panel renders so the
// interface's synchronous reads never cross the worker boundary.
type workerGitBackend struct {
	t workerTransport

	// onProgress receives the remote's parsed sideband updates during a clone/pull.
	// Set once (SetProgress) by the wasm driver to forward into State; nil in the
	// native tests. It runs on the transport's event-loop goroutine.
	onProgress func(gitrpc.Progress)

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

// SetProgress installs the sink the clone/pull ops forward the remote's progress
// notifications to. nil (the default) means no forwarding.
func (b *workerGitBackend) SetProgress(fn func(gitrpc.Progress)) { b.onProgress = fn }

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
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpClone, Args: argsFromConfig(cfg)}, b.onProgress)
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
		// Restore streams two coarse phases of its own (reading the saved copy, then
		// reopening the repository) through the same sink clone/pull use, so the
		// panel names the step instead of only saying "Restoring…".
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpRestore, Args: argsFromConfig(cfg)}, b.onProgress)
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

// Forget asks the worker to drop the saved copy. It reports back whatever
// happened: there being nothing stored is the same outcome as having dropped it,
// so there is no error to surface.
func (b *workerGitBackend) Forget(cfg gitConfig, done func()) {
	go func() {
		b.t.Call(gitrpc.Request{Op: gitrpc.OpForget, Args: argsFromConfig(cfg)}, nil)
		done()
	}()
}

// Pull fast-forwards the open repo in the worker and refreshes the cache.
func (b *workerGitBackend) Pull(done func(error)) {
	go func() {
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpPull}, b.onProgress)
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
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpCommit, Args: gitrpc.Args{Path: path, Content: content, Message: message}}, nil)
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
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpStage, Args: gitrpc.Args{Path: path, Content: content}}, nil)
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
			reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpWriteFile, Args: gitrpc.Args{Path: p, Content: c}}, nil)
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
		reply := b.t.Call(gitrpc.Request{Op: gitrpc.OpPush}, nil)
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
