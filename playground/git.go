// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// This file is the tagless, browser-free half of the playground's remote-git
// feature: the panel state machine, the .tex file picker, the error→message
// mapping and the whole toolkit overlay panel — everything that can be built and
// exercised by a native `go test`. The actual git client (go-git, via
// internal/browsergit) is NOT linked into playground.wasm at all: it lives in a
// separate git-worker.wasm loaded on demand in a Web Worker, driven over the
// [gitrpc] message protocol by the [workerGitBackend] in gitworker.go, whose
// syscall/js transport (the Worker + postMessage plumbing) lives in git_js.go
// behind the [gitBackend] seam. So this file never imports syscall/js or go-git
// and stays 100%-coverable off a browser. It mirrors the Collaborate affordance
// in collab.go.
//
// # How a document round-trips
//
// The user configures a remote URL + branch + token, then:
//
//   - Clone → the backend shallow-clones the repo into memory and hands back the
//     working-tree file list; the primary .tex is loaded into the SOURCE editor
//     via [State.SetSource]. Editing is then ordinary. Opening other files keeps
//     an independent edit buffer per path (see [fileBuffer] / openFile), so
//     several files can carry unsaved edits at once.
//   - Commit/Stage → the app first flushes EVERY dirty edit buffer to the working
//     tree (flushDirty → [gitBackend.WriteFiles]), then commits/stages, so
//     browsergit's git-add-A captures edits to files other than the active one.
//   - Push → the backend pushes the branch to the origin.
//   - Pull → the backend fast-forwards, then the active file's buffer is reloaded.
//
// # BaseURL config forms (documented in the panel's help text)
//
// The panel takes ONE remote URL and does not care which form it is:
//
//	(a) a CORS-enabled Forgejo/Gitea base URL directly, e.g.
//	    https://sources.mesocentre.plateau-de-saclay.net/owner/repo.git
//	(b) a proxy-prefixed URL for GitHub etc. via the sovereign CORS proxy, e.g.
//	    https://gitproxy.<host>/github.com/owner/repo.git
//
// browsergit joins BaseURL + repoPath; the panel passes the whole URL as BaseURL
// and an empty repoPath, so either form reaches the origin verbatim.

// gitLogLimit is how many recent commits the status area shows.
const gitLogLimit = 5

// gitDefaultBranch is the branch the panel starts on. It matches the worker's own
// default (browsergit.DefaultBranch); the string is kept here so the main app
// stays free of the go-git-pulling browsergit import.
const gitDefaultBranch = "main"

// gitConfig is the remote + identity the panel drives a clone with. The worker's
// side maps it onto the browsergit client options; keeping the fields here
// (tagless) makes the panel unit-testable without a browser. See [argsFromConfig]
// for the RPC translation.
type gitConfig struct {
	URL    string
	Branch string
	Token  string
	Author string
	Email  string
}

// gitFileChange is one dirty working-tree entry the workspace sidebar badges a
// file row from: its slash-relative path and a classify() status label
// ("untracked" | "modified" | "deleted" | "staged").
type gitFileChange struct {
	Path   string
	Status string
}

// fileBuffer is one file's independent, in-memory edit buffer: its unsaved text
// plus the caret + scroll position to restore when the file is re-opened. The
// active file's live edits live in the editor and are captured into its buffer
// only when navigating away (stashActive); an inactive file's buffer holds its
// last edited text verbatim. A file is DIRTY when its buffer text differs from
// the committed/indexed content the git backend cache holds (bufferDirty).
type fileBuffer struct {
	text       string
	cursorLine int
	cursorCol  int
	scrollLine int
}

// gitStatus is the display snapshot of the open repo's branch + divergence, the
// subset of [browsergit.Status] the panel shows. Changes is the per-file dirty
// list the sidebar reads; DirtyFile is its count (kept for the compact line).
type gitStatus struct {
	Branch    string
	Ahead     int
	Behind    int
	Clean     bool
	DirtyFile int
	Changes   []gitFileChange
}

// GitCommitInfo is one log line surfaced to the host (and the headless proof):
// the short hash, the subject and the author.
type GitCommitInfo struct {
	Hash    string
	Subject string
	Author  string
}

// gitBackend is the browser-only seam this file drives. The real implementation
// ([workerGitBackend], wired in git_js.go) drives a separate git-worker.wasm over
// the [gitrpc] protocol — the worker holds the open in-memory repo; a native build
// gets [nopGitBackend], so the state machine and the panel are fully testable
// without a browser.
//
// Clone/Pull/Commit/Push are asynchronous (a network round-trip): each reports
// completion through done, which the implementation may call from another
// goroutine once the Fetch resolves. The synchronous reads (ReadFile, Status,
// Log, HasRepo) touch only the in-memory tree the last successful op left behind.
type gitBackend interface {
	// Clone opens cfg.URL@cfg.Branch with cfg's identity into memory; done
	// reports the working-tree file list (slash-relative) or an error.
	Clone(cfg gitConfig, done func(files []string, err error))
	// Restore reopens a repository this browser saved on an earlier visit,
	// without contacting the remote. done reports found=false when nothing was
	// saved — an ordinary first visit, not a failure — and an error only when a
	// saved workspace existed and could not be reopened.
	Restore(cfg gitConfig, done func(files []string, found bool, err error))
	// Forget drops this browser's saved copy of cfg's repository. The open
	// repository is left alone: forgetting is about the next visit.
	Forget(cfg gitConfig, done func())
	// Pull fast-forwards the open repo against origin; done reports the error.
	Pull(done func(err error))
	// Commit writes content to path in the working tree, then commits with
	// message and the configured identity; done reports the error.
	Commit(path, content, message string, done func(err error))
	// Stage writes content to path in the working tree, then stages it (git
	// add) WITHOUT committing; done reports the error.
	Stage(path, content string, done func(err error))
	// WriteFiles writes each path→content to the working tree WITHOUT committing
	// or staging (the multi-file write-back that flushes every dirty edit buffer
	// before a stage/commit so browsergit's git-add-A sees them all); done reports
	// the first error, or nil once every file is written.
	WriteFiles(files map[string]string, done func(err error))
	// Push pushes the open branch to origin; done reports the error.
	Push(done func(err error))
	// ReadFile returns a working-tree file (sync; the tree is in memory).
	ReadFile(path string) ([]byte, error)
	// Status returns the branch/divergence snapshot; ok is false with no repo.
	Status() (st gitStatus, ok bool)
	// Log returns up to limit recent commits, newest first.
	Log(limit int) []GitCommitInfo
	// HasRepo reports whether a repository is currently open.
	HasRepo() bool
}

// gitField names the panel's focusable text inputs; typing edits the focused
// field's observable.
type gitField int

const (
	gitFieldNone gitField = iota
	gitFieldURL
	gitFieldBranch
	gitFieldToken
	gitFieldAuthor
	gitFieldEmail
	gitFieldMessage
)

// gitView is the "Remote Git" affordance: a launcher pill and, when open, a
// modal overlay panel with the remote/identity form, the Clone/Pull/Commit/Push
// actions and a status area. All session + config state is held in
// [mvvm.Observable]s (the app's MVVM contract); geometry and derived display
// snapshots are recomputed by layout() each frame, exactly like collabView.
type gitView struct {
	s       *State
	backend gitBackend
	repaint func() // host repaint hook; nil in tests

	// bootBuffer is the document the editor held when a BOOT clone started. The
	// clone loads the repository's primary .tex into the editor, which would
	// throw away whatever the reader had typed while the network was busy — so
	// on a boot clone the load happens only if the buffer is still exactly this.
	// Empty outside a boot clone, where an explicit Clone always loads.
	bootBuffer string

	// cloneGen counts clone attempts so a superseded one cannot act on its
	// result, and bootInFlight says whether the running one is the speculative
	// boot clone.
	//
	// A boot clone must never keep the reader from opening the repository they
	// actually asked for, so an explicit clone supersedes it and a late result
	// from it is dropped. Two EXPLICIT clones are a different matter — a
	// double-click on Clone — and the second is still refused, which is the
	// guard that was there before any of this.
	cloneGen     int
	bootInFlight bool

	open  bool
	focus gitField

	// config + session state — observables (the single source of truth the view
	// renders and typing mutates; no per-frame read-back).
	url    *mvvm.Observable[string]
	branch *mvvm.Observable[string]
	token  *mvvm.Observable[string]
	author *mvvm.Observable[string]
	email  *mvvm.Observable[string]
	msg    *mvvm.Observable[string] // commit message

	busy   *mvvm.Observable[bool]
	errMsg *mvvm.Observable[string]
	notice *mvvm.Observable[string]
	// op names the git operation in flight ("Cloning", "Pulling", …), or is
	// empty. busy alone says only THAT something is happening; a workspace that
	// freezes its buttons without saying which of five operations is running
	// leaves the reader to guess, so the name is carried and shown.
	op     *mvvm.Observable[string]
	loaded *mvvm.Observable[string] // the ACTIVE working-tree path bound to the editor

	// Independent per-file edit buffers. Opening a file no longer discards the
	// previous file's unsaved edits: every opened path keeps its own [fileBuffer]
	// (text + caret + scroll) here, so several files can be dirty at once. The
	// ACTIVE file (loaded) is the one bound to the live editor — its buffer is a
	// stash refreshed on the way out (stashActive) rather than on every keystroke,
	// so the live editor stays the single source of truth for the active file and
	// bufferContent reads it directly. Cleared on a fresh clone (a new repo).
	buffers map[string]*fileBuffer
	// openTabs is the ordered list of files open as editor tabs (the file-tab
	// strip). Opening a file appends it (if not already open) and makes it active;
	// the active tab is the one in loaded. Closing a tab removes it and activates a
	// neighbour. Distinct from buffers, which retains an edited file even after its
	// tab is closed (so re-opening restores the edits). Cleared on a fresh clone.
	openTabs []string
	// primaryPath is the .tex the render pane compiles (item 4): the primary .tex
	// chosen on clone, or the last one picked in the panel's file picker. Its LIVE
	// buffer compiles, so the render tracks its edits even while another file (a
	// .sty/.bib) is the one being edited in the editor. Empty with no repo → the
	// editor's own buffer (the sample document) compiles.
	primaryPath string

	// derived display snapshots, refreshed on each successful op (not per frame).
	files    []string // the whole working-tree file list
	texFiles []string // just the .tex paths, for the picker
	status   gitStatus
	statusOK bool
	log      []GitCommitInfo

	// geometry, recomputed by layout() before every draw and hit-test.
	panel   toolkit.Rect
	buttons []gitItem
	labels  []gitLabel
	boxes   []gitFieldBox

	// Persistent toolkit widgets the panel is built from — created once and re-used
	// every frame so a Button keeps its pressed / hover face between the mousedown
	// that presses it and the mouseup that releases it, and an Entry keeps its
	// caret. The panel draws + routes events through these instead of hand-painting
	// rectangles and text. Buttons are keyed by role+arg (the file-picker has one
	// per .tex file); fields by their gitField.
	scrim     *toolkit.Backdrop
	card      *toolkit.Backdrop
	btns      map[string]*toolkit.Button
	entries   map[gitField]*toolkit.Entry
	labelPool []*toolkit.Label
}

// gitItem is one clickable button in the panel; role drives dispatch and arg
// carries the picked-file index for a file-pick button.
type gitItem struct {
	role  gitRole
	rect  toolkit.Rect
	label string
	arg   int
}

// gitLabel is one line of static text in the panel.
type gitLabel struct {
	rect toolkit.Rect
	text string
	ink  toolkit.RGBA // zero (A==0) inherits the theme
}

// gitFieldBox is one bordered text input in the panel.
type gitFieldBox struct {
	field  gitField
	rect   toolkit.Rect
	value  string
	masked bool // render as bullets (the token)
}

// gitRole names what a panel control does; dispatch switches on it.
type gitRole int

const (
	gitRoleNone gitRole = iota
	gitRoleClose
	gitRoleClone
	gitRolePull
	gitRoleCommit
	gitRolePush
	gitRolePickFile
)

// newGitView builds the affordance for s with a no-op backend (a native build
// stays here; the wasm driver swaps in the real backend via [State.EnableGit]).
func newGitView(s *State) *gitView {
	return &gitView{
		s:       s,
		backend: nopGitBackend{},
		url:     mvvm.NewObservable(DefaultRemoteURL),
		branch:  mvvm.NewObservable(gitDefaultBranch),
		token:   mvvm.NewObservable(""),
		author:  mvvm.NewObservable(""),
		email:   mvvm.NewObservable(""),
		msg:     mvvm.NewObservable("Update from the go-tex playground"),
		busy:    mvvm.NewObservable(false),
		errMsg:  mvvm.NewObservable(""),
		notice:  mvvm.NewObservable(""),
		op:      mvvm.NewObservable(""),
		loaded:  mvvm.NewObservable(""),
		buffers: map[string]*fileBuffer{},
	}
}

// attach swaps in a real backend and the host repaint hook. Called by the wasm
// driver via [State.EnableGit].
func (v *gitView) attach(b gitBackend, repaint func()) {
	v.backend = b
	v.repaint = repaint
}

// gitPrewarmer is the optional interface a backend implements to be spawned ahead
// of the first operation. The worker-RPC backend uses it to create the Web Worker
// (and start downloading git-worker.wasm) the moment the panel opens, so the
// extra wasm streams in while the user fills the remote form.
type gitPrewarmer interface{ Prewarm() }

// openPanel opens the panel and prewarms the backend if it supports it. Idempotent
// on the prewarm side (the transport spawns the worker at most once).
func (v *gitView) openPanel() {
	v.open = true
	if p, ok := v.backend.(gitPrewarmer); ok {
		p.Prewarm()
	}
}

// refresh marks the scene dirty and repaints if a host hook is installed.
func (v *gitView) refresh() {
	v.s.dirty = true
	if v.repaint != nil {
		v.repaint()
	}
}

// config snapshots the observables into a plain gitConfig for the backend.
func (v *gitView) config() gitConfig {
	return gitConfig{
		URL:    v.url.Get(),
		Branch: v.branch.Get(),
		Token:  v.token.Get(),
		Author: v.author.Get(),
		Email:  v.email.Get(),
	}
}

// fieldObs returns the observable a focusable field edits (nil for none).
func (v *gitView) fieldObs(f gitField) *mvvm.Observable[string] {
	switch f {
	case gitFieldURL:
		return v.url
	case gitFieldBranch:
		return v.branch
	case gitFieldToken:
		return v.token
	case gitFieldAuthor:
		return v.author
	case gitFieldEmail:
		return v.email
	case gitFieldMessage:
		return v.msg
	}
	return nil
}

// --- the network actions, driven by the panel and the headless proof --------

// errNoGitRepo / errNoGitFile guard the write actions before a clone / a load.
var (
	errNoGitRepo = errors.New("git: clone a repository first")
	errNoGitFile = errors.New("git: open a file before committing")
)

// GitClone clones the configured remote into memory and loads its primary .tex
// into the source editor. done (optional) receives the error; the panel passes
// nil, the headless proof passes a handler.
func (s *State) GitClone(done func(err error)) { s.gitClone(false, done) }

// gitClone runs a clone. boot marks the speculative one the application starts
// with (see [State.BootClone]): it stands aside for anything explicit, while an
// explicit clone SUPERSEDES whatever is in flight — a reader who asks for a
// repository must not be told to wait for one they never asked for.
func (s *State) gitClone(boot bool, done func(err error)) {
	v := s.git
	if v.busy.Get() && (boot || !v.bootInFlight) {
		return // a boot clone stands aside; an explicit one only supersedes a boot
	}
	v.cloneGen++
	gen := v.cloneGen
	v.bootInFlight = boot
	v.beginOp("Cloning")
	// The git client is a separate binary — 4.2 MB gzip of go-git — that only
	// downloads when a repository is opened. Name it while it is on its way: the
	// playground is already interactive and typesetting, and a reader watching a
	// workspace that has not filled yet deserves to know what is still coming
	// rather than to guess.
	if _, worker := v.backend.(gitPrewarmer); worker {
		s.SetAssetLoading("git-worker.wasm")
	}
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Clone(v.config(), func(files []string, err error) {
		if gen != v.cloneGen {
			// Superseded: another clone started while this one was in flight, and
			// acting now would overwrite what it opened.
			if done != nil {
				done(err)
			}
			return
		}
		v.endOp()
		s.SetAssetLoading("")
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.setFiles(files)
			// A fresh clone is a new repository: drop every prior edit buffer so no
			// stale dirtiness leaks across repos.
			v.buffers = map[string]*fileBuffer{}
			v.openTabs = nil
			// Any repository opening makes the boot notice stale: it says the
			// SAMPLES did not arrive, and once something is open that is no longer
			// the interesting truth. Cleared here rather than in BootClone's own
			// callback, which only ever sees its own attempt.
			s.SetBootNotice("")
			v.primaryPath = primaryTeX(files)
			v.log = v.backend.Log(gitLogLimit)
			v.refreshStatus()
			typedMeanwhile := v.bootBuffer != "" && s.Source() != v.bootBuffer
			v.bootBuffer = ""
			switch {
			case typedMeanwhile:
				// The reader started writing while the clone was in flight. Their
				// text wins; the workspace still fills with the repository.
				v.notice.Set("Cloned — kept what you were writing.")
			case v.primaryPath != "":
				v.loadFresh(v.primaryPath)
			default:
				v.loaded.Set("")
				v.notice.Set("Cloned — no .tex file to open.")
			}
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// GitPull fast-forwards the open repo, then reloads the file in the editor.
func (s *State) GitPull(done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	v.beginOp("Pulling")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Pull(func(err error) {
		v.endOp()
		s.SetAssetLoading("")
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.log = v.backend.Log(gitLogLimit)
			v.refreshStatus()
			if p := v.loaded.Get(); p != "" {
				// Fast-forward brought new committed content; reload the active file's
				// buffer from it (other files' buffers keep their unsaved edits).
				v.loadFresh(p)
			}
			v.notice.Set("Pulled.")
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// GitCommit flushes EVERY dirty edit buffer to the working tree, then commits.
// browsergit's Commit stages every change (git add -A) before committing, so the
// flush is what makes a commit capture edits to files other than the active one;
// the active file's own write is still carried by the backend Commit call. After
// a successful commit the flushed files' buffers match the committed content, so
// their dirty flags clear.
func (s *State) GitCommit(done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	if v.loaded.Get() == "" {
		v.fail(errNoGitFile, done)
		return
	}
	v.beginOp("Committing")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	path := v.loaded.Get()
	content := s.Source()
	v.flushDirty(func(err error) {
		if err != nil {
			v.finishOp(err, done)
			return
		}
		v.backend.Commit(path, content, v.msg.Get(), func(err error) {
			v.endOp()
			if err != nil {
				v.errMsg.Set(gitErrorMessage(err))
			} else {
				v.log = v.backend.Log(gitLogLimit)
				v.refreshStatus()
				v.notice.Set("Committed " + path + ".")
			}
			if done != nil {
				done(err)
			}
			v.refresh()
		})
	})
}

// GitStage flushes EVERY dirty edit buffer to the working tree, then stages them
// (git add) WITHOUT committing, so each edited file's sidebar badge flips to
// "staged". It mirrors GitCommit's guards + flush; the sidebar's Stage button
// drives it.
func (s *State) GitStage(done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	if v.loaded.Get() == "" {
		v.fail(errNoGitFile, done)
		return
	}
	v.beginOp("Staging")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	path := v.loaded.Get()
	content := s.Source()
	v.flushDirty(func(err error) {
		if err != nil {
			v.finishOp(err, done)
			return
		}
		v.backend.Stage(path, content, func(err error) {
			v.endOp()
			if err != nil {
				v.errMsg.Set(gitErrorMessage(err))
			} else {
				// Stage wrote the buffers to the tree and cached them, so the sidebar's
				// per-file dirty overlays clear and the "staged" badges show.
				v.refreshStatus()
				v.notice.Set("Staged " + path + ".")
			}
			if done != nil {
				done(err)
			}
			v.refresh()
		})
	})
}

// beginOp marks a git operation in flight and names it, so the workspace can say
// which one rather than only that it is busy. endOp is its counterpart: every
// path that clears busy clears the name with it, or a finished operation would
// keep announcing itself.
func (v *gitView) beginOp(name string) {
	v.busy.Set(true)
	v.op.Set(name)
}

func (v *gitView) endOp() {
	v.busy.Set(false)
	v.op.Set("")
}

// finishOp ends a git op that failed before its main step (a flush error):
// clears busy, surfaces the error and reports it.
func (v *gitView) finishOp(err error, done func(error)) {
	v.endOp()
	v.errMsg.Set(gitErrorMessage(err))
	if done != nil {
		done(err)
	}
	v.refresh()
}

// flushDirty writes every dirty edit buffer (the active file's live editor text
// included) back to the working tree so a following stage/commit sees them, then
// calls done. It captures the active file's live edits into its buffer first
// (stashActive). With nothing dirty it short-circuits to success without a backend
// round-trip.
func (v *gitView) flushDirty(done func(error)) {
	v.stashActive()
	dirty := v.dirtyBuffers()
	if len(dirty) == 0 {
		done(nil)
		return
	}
	v.backend.WriteFiles(dirty, done)
}

// dirtyBuffers is the path→content map of every buffer whose content differs
// from the committed/indexed content (the set flushDirty writes back).
func (v *gitView) dirtyBuffers() map[string]string {
	out := map[string]string{}
	for p := range v.buffers {
		if v.bufferDirty(p) {
			if c, ok := v.bufferContent(p); ok {
				out[p] = c
			}
		}
	}
	return out
}

// GitPush pushes the open branch to origin.
func (s *State) GitPush(done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	v.beginOp("Pushing")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Push(func(err error) {
		v.endOp()
		s.SetAssetLoading("")
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.refreshStatus()
			v.notice.Set("Pushed to origin.")
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// fail records a pre-flight guard failure on the panel and reports it.
func (v *gitView) fail(err error, done func(error)) {
	v.errMsg.Set(gitErrorMessage(err))
	v.refresh()
	if done != nil {
		done(err)
	}
}

// setFiles records the working-tree list and the .tex subset for the picker.
func (v *gitView) setFiles(files []string) {
	v.files = files
	v.texFiles = texFiles(files)
}

// loadFresh reads path from the working tree, makes it the ACTIVE file and
// (re)seeds its edit buffer from the freshly-read content — DISCARDING any prior
// buffer for it. It is the "load a committed file" path: clone (the primary .tex)
// and pull (reload the active file with the new committed content) use it. A read
// miss surfaces on the panel and leaves the active file unchanged.
func (v *gitView) loadFresh(p string) {
	data, err := v.backend.ReadFile(p)
	if err != nil {
		v.errMsg.Set(gitErrorMessage(err))
		return
	}
	v.loaded.Set(p)
	v.addTab(p)
	v.buffers[p] = &fileBuffer{text: string(data)}
	v.s.SetSource(string(data))
	v.notice.Set("Loaded " + p + ".")
}

// stashActive captures the active file's LIVE editor state (text + caret +
// scroll) into its edit buffer, so switching away preserves the unsaved edits. A
// no-op with no active file.
func (v *gitView) stashActive() {
	p := v.loaded.Get()
	if p == "" {
		return
	}
	buf := v.buffers[p]
	if buf == nil {
		buf = &fileBuffer{}
		v.buffers[p] = buf
	}
	buf.text = v.s.Source()
	buf.cursorLine = v.s.editor.CursorLine().Get()
	buf.cursorCol = v.s.editor.CursorCol().Get()
	buf.scrollLine = v.s.editor.ScrollLine().Get()
}

// openFile switches the editor to path, PRESERVING every buffer's unsaved edits.
// The active file's live edits are stashed first; then, if path already has an
// edit buffer, it is restored verbatim (text + caret + scroll); otherwise the
// committed content is read, a buffer is created for it and the caret parks at the
// top. Opening the already-active file is a no-op. A read miss surfaces on the
// panel and leaves the active file unchanged.
func (v *gitView) openFile(p string) {
	if p == "" || p == v.loaded.Get() {
		return
	}
	// Leaving a file drops WYSIWYG back to Source FIRST, writing the RichEditor's
	// edits back to the CURRENT (LaTeX) file, so the new file opens in Source and
	// one file's LaTeX is never written into another (WYSIWYG is LaTeX-only and
	// per-file). Runs before stashActive so those edits reach the current buffer.
	v.s.wysiwyg().exitWysiwyg()
	v.stashActive()
	// The active file's name shows in the sidebar's "Files" accordion header and on
	// its editor tab, so a switch clears the detail strip rather than restating the
	// file there — no per-open notice line to crowd the workspace column.
	if buf, ok := v.buffers[p]; ok {
		v.loaded.Set(p)
		v.addTab(p)
		v.s.SetSourceCursor(buf.text, buf.cursorLine, buf.cursorCol, buf.scrollLine)
		v.notice.Set("")
		return
	}
	data, err := v.backend.ReadFile(p)
	if err != nil {
		v.errMsg.Set(gitErrorMessage(err))
		return
	}
	v.buffers[p] = &fileBuffer{text: string(data)}
	v.loaded.Set(p)
	v.addTab(p)
	v.s.SetSource(string(data))
	v.notice.Set("")
}

// addTab appends p to the open editor tabs when it is not already open. The
// caller has already made p the active file (loaded); this only maintains the
// tab strip's ordered list.
func (v *gitView) addTab(p string) {
	if p == "" {
		return
	}
	for _, t := range v.openTabs {
		if t == p {
			return
		}
	}
	v.openTabs = append(v.openTabs, p)
}

// closeTab removes p from the open editor tabs. When p was the active file, the
// neighbour to its left (or the new first tab) becomes active and is opened; with
// no tab left the editor is cleared to an empty, path-less buffer. The file's
// edit buffer is retained, so re-opening it from the tree restores its edits.
func (v *gitView) closeTab(p string) {
	idx := -1
	for i, t := range v.openTabs {
		if t == p {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	v.openTabs = append(v.openTabs[:idx], v.openTabs[idx+1:]...)
	if v.loaded.Get() != p {
		return // closed an inactive tab; the active file is unchanged
	}
	// Closing the active tab: activate a neighbour (the one now at idx-1, or the new
	// first tab), or clear the editor when nothing is left open.
	if len(v.openTabs) == 0 {
		v.loaded.Set("")
		v.s.SetSource("")
		return
	}
	next := idx - 1
	if next < 0 {
		next = 0
	}
	target := v.openTabs[next]
	// openFile early-returns on the already-active path; the active file is p (being
	// closed), so target differs and the switch runs.
	v.openFile(target)
}

// bufferContent returns the current in-memory content of path — the LIVE editor
// for the active file, else the file's stashed edit buffer — and whether a buffer
// exists for it at all.
func (v *gitView) bufferContent(p string) (string, bool) {
	if p != "" && p == v.loaded.Get() {
		return v.s.Source(), true
	}
	if buf, ok := v.buffers[p]; ok {
		return buf.text, true
	}
	return "", false
}

// bufferDirty reports whether path's edit buffer differs from its committed /
// indexed content (the git backend cache the last clone/pull/commit/stage/flush
// filled). A path with no buffer, or an uncached file (nothing to compare), reads
// not-dirty.
func (v *gitView) bufferDirty(p string) bool {
	content, ok := v.bufferContent(p)
	if !ok {
		return false
	}
	data, err := v.backend.ReadFile(p)
	if err != nil {
		return false
	}
	return string(data) != content
}

// GitOpenFile opens working-tree path into the source editor (the sidebar's
// file-row click drives it), preserving the previously-open file's unsaved edits
// in its own buffer. It reads the cached content the last clone/pull filled — so
// any pre-cached text file (.tex/.sty/.bib/…), not just the primary .tex, opens.
// A no-op while a network op is in flight or before a clone; reports whether the
// file is the one now active.
func (s *State) GitOpenFile(path string) bool {
	v := s.git
	if v.busy.Get() || !v.backend.HasRepo() || path == "" {
		return false
	}
	v.openFile(path)
	v.refresh()
	return v.loaded.Get() == path
}

// GitOpenTabs is the ordered list of files open as editor tabs (the file-tab
// strip reads it). Empty with no repo / nothing opened.
func (s *State) GitOpenTabs() []string { return s.git.openTabs }

// GitActiveTabIndex is the index of the active file within GitOpenTabs, or -1
// when no open tab is active (no repo, or the editor was cleared).
func (s *State) GitActiveTabIndex() int {
	active := s.git.loaded.Get()
	for i, p := range s.git.openTabs {
		if p == active {
			return i
		}
	}
	return -1
}

// GitCloseTab closes the editor tab for path (the file-tab strip's × drives it),
// activating a neighbour when the active tab is closed. A no-op while a network
// op is in flight. It keeps the file's edit buffer, so re-opening restores edits.
func (s *State) GitCloseTab(path string) {
	v := s.git
	if v.busy.Get() || path == "" {
		return
	}
	v.closeTab(path)
	v.refresh()
}

// refreshStatus snapshots the backend's branch/divergence for the status area.
func (v *gitView) refreshStatus() {
	st, ok := v.backend.Status()
	v.status, v.statusOK = st, ok
}

// gitErrorMessage maps a browsergit sentinel (or any error) to a clear panel
// line. An error that matches none falls through to its own text.
func gitErrorMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errGitAuth):
		return "Authentication failed — check your access token."
	case errors.Is(err, errGitNonFastForward):
		return "Push rejected: the remote moved on. Pull, then push again."
	case errors.Is(err, errGitRepoNotFound):
		return "Repository not found — check the remote URL."
	case errors.Is(err, errGitTransport):
		// The fetch failed before any HTTP status. That is a real network error,
		// a CORS rejection, or — since the sample remote IS CORS-enabled for this
		// origin — very often a browser EXTENSION (an ad/tracker blocker) or
		// private-mode / tracking-protection blocking the cross-origin request.
		// The browser hides the specific reason from JS (it is only in the
		// devtools console), so append whatever the client did report.
		return "Couldn't reach the remote — a network error, CORS, or a browser extension / privacy setting blocking it: " + err.Error()
	case errors.Is(err, errNoGitRepo):
		return "Clone a repository first."
	case errors.Is(err, errNoGitFile):
		return "Open a file before committing."
	default:
		return err.Error()
	}
}

// --- the .tex file picker (pure, testable) -----------------------------------

// texFiles filters paths to the .tex files, preserving order.
func texFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.EqualFold(path.Ext(f), ".tex") {
			out = append(out, f)
		}
	}
	return out
}

// primaryTeX picks the .tex to open on clone: a top-level main.tex if present,
// else the shallowest/first .tex in sorted order, else "".
func primaryTeX(files []string) string {
	tex := texFiles(files)
	if len(tex) == 0 {
		return ""
	}
	for _, f := range tex {
		if f == "main.tex" {
			return f
		}
	}
	sorted := append([]string(nil), tex...)
	sort.Slice(sorted, func(i, j int) bool {
		di, dj := strings.Count(sorted[i], "/"), strings.Count(sorted[j], "/")
		if di != dj {
			return di < dj // prefer shallower paths
		}
		return sorted[i] < sorted[j]
	})
	return sorted[0]
}

// --- introspection for the host, the headless proof and tests ----------------

// GitActive reports whether the panel is open.
func (s *State) GitActive() bool { return s.git.open }

// SetGitOpen opens or closes the panel (host / headless-proof control). Opening
// prewarms the worker so git-worker.wasm streams in before the first clone.
func (s *State) SetGitOpen(open bool) {
	if open {
		s.git.openPanel()
	} else {
		s.git.open = false
	}
	s.git.refresh()
}

// GitBusy reports whether a network op is in flight (buttons are disabled).
func (s *State) GitBusy() bool { return s.git.busy.Get() }

// GitError / GitNotice expose the last error and the last success line.
func (s *State) GitError() string  { return s.git.errMsg.Get() }
func (s *State) GitNotice() string { return s.git.notice.Get() }

// GitLoadedPath is the working-tree path currently shown in the editor ("" none).
func (s *State) GitLoadedPath() string { return s.git.loaded.Get() }

// GitPrimaryPath is the .tex the render pane compiles ("" with no repo / no .tex).
func (s *State) GitPrimaryPath() string { return s.git.primaryPath }

// GitBufferDirty reports whether path's edit buffer differs from its committed /
// indexed content — the per-file dirtiness the sidebar badges each row from.
func (s *State) GitBufferDirty(path string) bool { return s.git.bufferDirty(path) }

// GitBufferPaths returns, sorted, every path that currently has an edit buffer
// (host / proof introspection for the multi-buffer model).
func (s *State) GitBufferPaths() []string {
	out := make([]string, 0, len(s.git.buffers))
	for p := range s.git.buffers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// GitTeXFiles are the .tex paths the picker offers.
func (s *State) GitTeXFiles() []string { return s.git.texFiles }

// Config setters/getters (the host wires these to the DOM form / persistence;
// tests drive them directly). Every setter routes through the observable so the
// panel repaints.
func (s *State) SetGitURL(u string)           { s.git.url.Set(u); s.git.refresh() }
func (s *State) SetGitBranch(b string)        { s.git.branch.Set(b); s.git.refresh() }
func (s *State) SetGitToken(t string)         { s.git.token.Set(t); s.git.refresh() }
func (s *State) SetGitAuthor(a string)        { s.git.author.Set(a); s.git.refresh() }
func (s *State) SetGitEmail(e string)         { s.git.email.Set(e); s.git.refresh() }
func (s *State) SetGitCommitMessage(m string) { s.git.msg.Set(m); s.git.refresh() }
func (s *State) GitURL() string               { return s.git.url.Get() }
func (s *State) GitBranch() string            { return s.git.branch.Get() }
func (s *State) GitCommitMessage() string     { return s.git.msg.Get() }

// GitStatusLine formats the status area's headline: the branch and, when the
// repo has enough history, its ahead/behind divergence plus dirty-file count.
func (s *State) GitStatusLine() string {
	v := s.git
	if !v.statusOK {
		return "No repository cloned."
	}
	st := v.status
	line := "On " + st.Branch
	if st.Ahead > 0 || st.Behind > 0 {
		line += fmt.Sprintf(" (ahead %d, behind %d)", st.Ahead, st.Behind)
	}
	if st.Clean {
		line += " — clean"
	} else {
		line += fmt.Sprintf(" — %d changed", st.DirtyFile)
	}
	return line
}

// GitLog returns the recent commits shown in the status area.
func (s *State) GitLog() []GitCommitInfo { return s.git.log }

// --- the panel: layout, draw, input ------------------------------------------

// DefaultRemoteURL is the repository the playground opens with: the go-tex
// sample documents, on the org's own Forgejo.
//
// It has to be that host and not GitHub. A clone from a browser is an ordinary
// fetch, so the server must send the CORS headers for this origin, and GitHub
// does not: reaching a GitHub repository from here needs a proxy, and the one
// this app was written against answers nothing. The Forgejo does send them —
// access-control-allow-origin: https://go-tex.github.io on the git endpoints —
// so the clone works with no intermediary at all.
const DefaultRemoteURL = "https://sources.mesocentre.plateau-de-saclay.net/go-tex/examples.git"

// gitPanelW is the panel's logical width.
const (
	gitPanelW = 420
)

// layout recomputes the launcher rect and, when open, the panel geometry and its
// interactive items. Idempotent and cheap; called before every draw and hit-test.
func (v *gitView) layout() {
	pad := toolkit.Scaled(8)
	bh := toolkit.Scaled(26)
	gap := toolkit.Scaled(6)
	v.buttons = v.buttons[:0]
	v.labels = v.labels[:0]
	v.boxes = v.boxes[:0]
	if !v.open {
		return
	}

	pw := toolkit.Scaled(gitPanelW)
	if pw > v.s.w-2*pad {
		pw = v.s.w - 2*pad
	}
	x := (v.s.w - pw) / 2
	y := pad + v.s.bodyTop()
	line := toolkit.Scaled(20)
	innerX := x + pad
	innerW := pw - 2*pad
	labelW := toolkit.Scaled(70)

	cur := y + pad
	addLabel := func(text string, ink toolkit.RGBA) {
		v.labels = append(v.labels, gitLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: text, ink: ink})
		cur += line
	}
	addField := func(f gitField, label string, masked bool) {
		v.labels = append(v.labels, gitLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: labelW, H: bh}, text: label})
		box := gitFieldBox{field: f, rect: toolkit.Rect{X: innerX + labelW, Y: cur, W: innerW - labelW, H: bh}, value: v.fieldObs(f).Get(), masked: masked}
		v.boxes = append(v.boxes, box)
		cur += bh + toolkit.Scaled(4)
	}
	addButtons := func(items ...gitItem) {
		n := len(items)
		bw := (innerW - (n-1)*gap) / n
		for i := range items {
			items[i].rect = toolkit.Rect{X: innerX + i*(bw+gap), Y: cur, W: bw, H: bh}
			v.buttons = append(v.buttons, items[i])
		}
		cur += bh + gap
	}

	// Title + close.
	v.labels = append(v.labels, gitLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW - bh, H: bh}, text: "Remote Git"})
	v.buttons = append(v.buttons, gitItem{role: gitRoleClose, rect: toolkit.Rect{X: x + pw - pad - bh, Y: cur, W: bh, H: bh}, label: "X"})
	cur += bh + toolkit.Scaled(4)

	// Help text: the two URL config forms.
	addLabel("URL: a CORS Forgejo base …/owner/repo.git,", toolkit.RGBA{})
	addLabel("or a proxy prefix …/github.com/owner/repo.git", toolkit.RGBA{})
	cur += toolkit.Scaled(2)

	// Remote + identity form.
	addField(gitFieldURL, "Remote", false)
	addField(gitFieldBranch, "Branch", false)
	addField(gitFieldToken, "Token", true)
	addField(gitFieldAuthor, "Author", false)
	addField(gitFieldEmail, "Email", false)

	// Clone / Pull.
	addButtons(gitItem{role: gitRoleClone, label: "Clone"}, gitItem{role: gitRolePull, label: "Pull"})

	// File picker (only with more than one .tex).
	if len(v.texFiles) > 1 {
		addLabel("Open:", toolkit.RGBA{})
		items := make([]gitItem, 0, len(v.texFiles))
		for i, f := range v.texFiles {
			items = append(items, gitItem{role: gitRolePickFile, label: shortName(f), arg: i})
		}
		// Wrap into rows of up to three.
		for start := 0; start < len(items); start += 3 {
			end := start + 3
			if end > len(items) {
				end = len(items)
			}
			addButtons(items[start:end]...)
		}
	}

	// Commit message + Commit / Push.
	addField(gitFieldMessage, "Message", false)
	addButtons(gitItem{role: gitRoleCommit, label: "Commit"}, gitItem{role: gitRolePush, label: "Push"})

	// Status area.
	cur += toolkit.Scaled(2)
	addLabel(v.s.GitStatusLine(), toolkit.RGBA{})
	if p := v.loaded.Get(); p != "" {
		addLabel("Editing "+p, toolkit.RGBA{})
	}
	for _, c := range v.log {
		addLabel("• "+shortHash(c.Hash)+" "+c.Subject, toolkit.RGBA{})
	}

	// Busy / error / notice line.
	switch {
	case v.busy.Get():
		addLabel("Working…", toolkit.RGBA{})
	case v.errMsg.Get() != "":
		addLabel("⚠ "+v.errMsg.Get(), toolkit.RGBA{R: 0xE5, G: 0x39, B: 0x35, A: 0xFF})
	case v.notice.Get() != "":
		addLabel(v.notice.Get(), toolkit.RGBA{R: 0x43, G: 0xA0, B: 0x47, A: 0xFF})
	}

	v.panel = toolkit.Rect{X: x, Y: y, W: pw, H: cur + pad - y}
}

// shortName is a working-tree path trimmed to its last two segments for a button.
func shortName(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// shortHash is the 7-char abbreviated commit hash (or the whole thing if short).
func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// ensureWidgets lazily builds the persistent toolkit widgets the panel is drawn
// from and routes events through. Called at the top of every draw / input path.
func (v *gitView) ensureWidgets() {
	if v.scrim != nil {
		return
	}
	v.scrim = &toolkit.Backdrop{Fill: toolkit.RGBA{A: 0x66}, Interactive: true}
	v.card = &toolkit.Backdrop{}
	v.btns = map[string]*toolkit.Button{}
	v.entries = map[gitField]*toolkit.Entry{}
}

// btnKey uniquely identifies a persistent button: its role plus the file-picker
// argument (0 for every non-picker button).
func btnKey(role gitRole, arg int) string { return fmt.Sprintf("%d:%d", role, arg) }

// btn returns the persistent Button for a role+arg, creating it (wired to dispatch
// that role/arg) on first use so its pressed / hover state survives between frames.
func (v *gitView) btn(role gitRole, arg int) *toolkit.Button {
	k := btnKey(role, arg)
	if b := v.btns[k]; b != nil {
		return b
	}
	rr, aa := role, arg
	b := toolkit.NewButton("", func() { v.dispatch(rr, aa) })
	v.btns[k] = b
	return b
}

// entry returns the persistent Entry for a field (the token field masked as
// bullets), creating it on first use so it keeps its caret between frames.
func (v *gitView) entry(f gitField, masked bool) *toolkit.Entry {
	if e := v.entries[f]; e != nil {
		return e
	}
	e := toolkit.NewEntry("")
	if masked {
		e.Mask = '•'
	}
	v.entries[f] = e
	return e
}

// label returns a reused, pooled Label for the i-th visible text line, growing
// the pool as needed so no widget is allocated per frame.
func (v *gitView) label(i int) *toolkit.Label {
	for len(v.labelPool) <= i {
		v.labelPool = append(v.labelPool, toolkit.NewLabel(""))
	}
	return v.labelPool[i]
}

// draw paints the launcher and, when open, the overlay panel — entirely from
// persistent toolkit widgets (a Backdrop scrim + card, Entry fields, Label lines,
// Button controls), so every element carries its own press / hover / focus
// feedback and nothing is hand-painted.
func (v *gitView) draw(p painter.Painter, theme *toolkit.Theme) {
	v.layout()
	v.ensureWidgets()

	if !v.open {
		return
	}

	// Modal scrim + panel body, each a Backdrop rather than hand-filled rects. The
	// scrim dims the body region between the toolbar and the status bar (below the
	// topZone band, above the bottomZone band).
	v.scrim.SetBounds(toolkit.Rect{X: 0, Y: v.s.bodyTop(), W: v.s.w, H: v.s.h - v.s.bodyTop() - v.s.statusH - v.s.bottomZoneH})
	v.scrim.Draw(p, theme)
	v.card.Fill = theme.Surface
	v.card.Stroke = theme.Border
	v.card.StrokeWidth = toolkit.Scaled(1)
	v.card.SetBounds(v.panel)
	v.card.Draw(p, theme)

	// Text fields: a real Entry each (own border, text/mask + focus caret).
	for _, b := range v.boxes {
		e := v.entry(b.field, b.masked)
		e.SetBounds(b.rect)
		e.SetText(b.value)
		e.SetFocused(v.focus == b.field)
		e.Draw(p, theme)
	}

	// Static text lines, each a reused Label.
	for i, l := range v.labels {
		lb := v.label(i)
		lb.SetBounds(l.rect)
		lb.Text().Set(l.text)
		lb.Ink = l.ink
		lb.Draw(p, theme)
	}
	// Action buttons, each a persistent Button so it depresses on press; the
	// network buttons go inert (disabled) while an operation is in flight.
	for _, b := range v.buttons {
		btn := v.btn(b.role, b.arg)
		btn.Label().Set(b.label)
		btn.SetBounds(b.rect)
		btn.Disabled().Set(v.busy.Get() && b.role != gitRoleClose)
		btn.Draw(p, theme)
	}
}

// handleClick routes a pointer press to the launcher or, when open, to a panel
// control, resolving each hit through the target widget's own HitTest and
// delivering a toolkit EventClick so a Button depresses + fires through the
// toolkit. An open panel is modal. Returns whether it consumed the click.
func (v *gitView) handleClick(x, y int) bool {
	v.layout()
	v.ensureWidgets()
	if !v.open {
		return false // no launcher: the workspace sidebar's Clone opens this panel
	}
	// Focus a text field.
	for _, b := range v.boxes {
		e := v.entry(b.field, b.masked)
		e.SetBounds(b.rect)
		if e.HitTest(x, y) {
			v.focus = b.field
			v.refresh()
			return true
		}
	}
	v.focus = gitFieldNone
	for _, b := range v.buttons {
		btn := v.btn(b.role, b.arg)
		btn.SetBounds(b.rect)
		if btn.HitTest(x, y) {
			btn.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.rect.X, Y: y - b.rect.Y})
			return true
		}
	}
	v.refresh()
	return true // modal: swallow everything over the body
}

// handleMove routes a pointer move over the open modal panel to its buttons so
// each raises or clears its hover face through the toolkit. A no-op while closed.
func (v *gitView) handleMove(x, y int) {
	if !v.open {
		return
	}
	v.layout()
	v.ensureWidgets()
	for _, b := range v.buttons {
		btn := v.btn(b.role, b.arg)
		btn.SetBounds(b.rect)
		btn.OnEvent(toolkit.Event{Kind: toolkit.EventMouseMove, X: x - b.rect.X, Y: y - b.rect.Y})
	}
	v.s.dirty = true
}

// handleRelease ends a press: every panel button clears its pressed face on the
// mouseup, so a depress is momentary. Called whenever this view captured the
// preceding press (even if the action closed the panel).
func (v *gitView) handleRelease(x, y int) {
	v.ensureWidgets()
	for _, b := range v.btns {
		b.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
	}
	v.refresh()
}

// dispatch runs the action of a clicked control. Network actions are ignored
// while busy (the buttons are drawn disabled, but a click race is guarded here).
func (v *gitView) dispatch(role gitRole, arg int) {
	switch role {
	case gitRoleClose:
		v.open = false
	case gitRoleClone:
		v.s.GitClone(nil)
	case gitRolePull:
		v.s.GitPull(nil)
	case gitRoleCommit:
		v.s.GitCommit(nil)
	case gitRolePush:
		v.s.GitPush(nil)
	case gitRolePickFile:
		if arg >= 0 && arg < len(v.texFiles) && !v.busy.Get() {
			// Picking a .tex makes it the document the render compiles, and opens it
			// (preserving every other file's edit buffer).
			v.primaryPath = v.texFiles[arg]
			v.openFile(v.texFiles[arg])
		}
	}
	v.refresh()
}

// resolveWorkspace answers the engine's request for a class, package or \input
// file out of the OPEN WORKSPACE, so a document can load a .sty that sits beside
// it in the repository.
//
// Without it the engine sees only its embedded set and the document's own
// \usepackage silently does nothing: every command that package defined becomes
// undefined, and — because an undefined command swallows its argument — the text
// those commands wrapped disappears from the page. The sample article lost 112
// characters that way, and the status bar counted three issues, before the
// workspace was wired here.
//
// nil when no repository is open, which is the engine's "disk and embedded set
// only" default.
func (v *gitView) resolveWorkspace() func(string) ([]byte, bool) {
	if v.backend == nil || !v.backend.HasRepo() {
		return nil
	}
	return func(name string) ([]byte, bool) {
		// The engine asks with the extension already on ("gotex-demo.sty"); look
		// for it at the repository root and anywhere else it may sit.
		for _, p := range v.files {
			if p == name || strings.HasSuffix(p, "/"+name) {
				if b, err := v.backend.ReadFile(p); err == nil {
					return b, true
				}
			}
		}
		return nil, false
	}
}

// compileSource is the LaTeX the render pane compiles: the ACTIVE .tex's LIVE
// buffer — the document the reader is looking at — so a workspace holding several
// .tex renders whichever one is open, not one hardwired file. openFile recompiles
// on every switch, so opening another .tex re-renders it.
//
// A LaTeX document (.tex/.ltx) compiles as-is; a Markdown file (.md/.markdown/.mkd)
// is converted to LaTeX first (markdownLaTeX), so a README renders as a document.
// Any other file — a .sty package, a .cls class, a .bib database — renders NOTHING:
// compileSource returns "" so the render pane blanks, rather than compiling
// package/database code into garbage or falling back to some other file. With no
// repo open (the sample document) or no active file, the editor's own buffer
// compiles.
func (v *gitView) compileSource() string {
	p := v.loaded.Get()
	if p == "" {
		return v.s.editor.Text().Get() // the sample document, or an empty editor
	}
	// The active file is always buffered — its live content is the editor — so
	// bufferContent never misses here.
	c, _ := v.bufferContent(p)
	switch {
	case isRenderableDoc(p):
		return c
	case isMarkdownDoc(p):
		return markdownLaTeX(c)
	}
	return "" // a non-document file → nothing to render
}

// isRenderableDoc reports whether path names a LaTeX document the engine should
// typeset directly (a .tex or .ltx), as opposed to a package/class/database file
// that is not a standalone document.
func isRenderableDoc(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".tex", ".ltx":
		return true
	}
	return false
}

// isMarkdownDoc reports whether path names a Markdown file (rendered as a document
// after conversion to LaTeX).
func isMarkdownDoc(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mkd":
		return true
	}
	return false
}

// handleChar types a printable character into the focused field. Returns whether
// it consumed the character.
func (v *gitView) handleChar(code string) bool {
	if !v.open || v.focus == gitFieldNone {
		return false
	}
	obs := v.fieldObs(v.focus)
	if obs == nil {
		return false
	}
	if r := []rune(code); len(r) == 1 {
		obs.Set(obs.Get() + code)
		v.refresh()
	}
	return true
}

// handleKey handles editing keys in the focused field (Backspace, Enter, Tab,
// Escape). Returns whether it consumed the key.
func (v *gitView) handleKey(code string) bool {
	if !v.open {
		return false
	}
	if code == "Escape" {
		if v.focus != gitFieldNone {
			v.focus = gitFieldNone
		} else {
			v.open = false
		}
		v.refresh()
		return true
	}
	if v.focus == gitFieldNone {
		return false
	}
	switch code {
	case "Backspace":
		obs := v.fieldObs(v.focus)
		if r := []rune(obs.Get()); len(r) > 0 {
			obs.Set(string(r[:len(r)-1]))
		}
	case "Enter", "Return", "Tab":
		v.focus = v.nextField()
	}
	v.refresh()
	return true
}

// nextField advances focus to the next editable field, wrapping around, so Tab /
// Enter walks the form.
func (v *gitView) nextField() gitField {
	order := []gitField{gitFieldURL, gitFieldBranch, gitFieldToken, gitFieldAuthor, gitFieldEmail, gitFieldMessage}
	for i, f := range order {
		if f == v.focus {
			return order[(i+1)%len(order)]
		}
	}
	return gitFieldURL
}

// --- the native no-op backend ------------------------------------------------

// nopGitBackend is the backend a native build (and every test that does not
// inject its own) gets: remote git needs a browser, so each network step reports
// that and nothing is cloned.
type nopGitBackend struct{}

func (nopGitBackend) Clone(_ gitConfig, done func([]string, error)) { done(nil, errNoBrowserGit) }
func (nopGitBackend) Pull(done func(error))                         { done(errNoBrowserGit) }

// Restore on a build with no browser git finds nothing, rather than failing: a
// native run has no saved workspace and never had one.
func (nopGitBackend) Restore(_ gitConfig, done func([]string, bool, error)) { done(nil, false, nil) }

// Forget on a build with no browser git has nothing saved to drop.
func (nopGitBackend) Forget(_ gitConfig, done func()) { done() }

func (nopGitBackend) Commit(_, _, _ string, done func(error)) { done(errNoBrowserGit) }
func (nopGitBackend) Stage(_, _ string, done func(error))     { done(errNoBrowserGit) }
func (nopGitBackend) WriteFiles(_ map[string]string, done func(error)) {
	done(errNoBrowserGit)
}
func (nopGitBackend) Push(done func(error))           { done(errNoBrowserGit) }
func (nopGitBackend) ReadFile(string) ([]byte, error) { return nil, errNoBrowserGit }
func (nopGitBackend) Status() (gitStatus, bool)       { return gitStatus{}, false }
func (nopGitBackend) Log(int) []GitCommitInfo         { return nil }
func (nopGitBackend) HasRepo() bool                   { return false }

// errNoBrowserGit explains why the no-op backend never clones.
var errNoBrowserGit = errors.New("git: remote git needs a browser")

// BootClone opens [DefaultRemoteURL] in the workspace as the application
// starts, and shows the workspace sidebar once it lands.
//
// It does NOT block the start. The app comes up on its built-in document
// immediately and the repository arrives when the network delivers it, so a
// slow or unreachable Forgejo costs a reader nothing but the samples — where
// waiting for the clone would have made every start as slow as the worst
// network, and a failed one leave a blank window.
//
// A reader who begins typing before the clone lands keeps what they wrote: the
// buffer at boot is remembered and the repository's primary .tex is only loaded
// if it is still untouched.
//
// It is a no-op with no backend (a headless test), with a repository already
// open, or when a clone is already in flight.
func (s *State) BootClone(done func(err error)) {
	v := s.git
	if v.backend == nil || v.busy.Get() || v.backend.HasRepo() {
		if done != nil {
			done(nil)
		}
		return
	}
	v.url.Set(DefaultRemoteURL)
	v.bootBuffer = s.Source()

	settle := func(err error) {
		if err == nil {
			// The sidebar has been open from the start since #67; this keeps
			// BootClone correct on its own terms rather than resting on that.
			s.sidebar.open = true
			s.layout()
			s.dirty = true
		} else {
			// The workspace stays closed, which on its own is indistinguishable
			// from a workspace nobody asked for. Say why.
			s.SetBootNotice(gitErrorMessage(err))
		}
		if done != nil {
			done(err)
		}
	}

	// A browser that already holds this repository should not fetch it again.
	// Restoring is local and quick; the network round-trip that follows is a
	// fast-forward, not a fresh clone, and it leaves any uncommitted edit alone.
	s.gitRestore(func(restored bool, err error) {
		switch {
		case restored:
			// Opened from this browser's own copy. Bring it up to date, but a
			// failed refresh is NOT a failed boot: the workspace is already
			// open and usable, and an unreachable remote should not empty it.
			s.GitPull(func(pullErr error) {
				if pullErr != nil {
					// The pull left its failure on the error line, which the
					// detail strip shows in red ahead of everything else. That
					// reads as "the workspace is broken" when in fact it opened
					// and is usable — only the update did not happen.
					//
					// WHY the failure matters, though. A remote we could not
					// reach is nobody's fault and needs no action, so it is said
					// plainly and the red is dropped. A rejected token or a
					// missing repository is a real problem the reader has to
					// fix, and quietly calling it "offline" would send them
					// looking for a network fault that is not there — so those
					// keep the error line they earned.
					if errors.Is(pullErr, errGitTransport) {
						v.errMsg.Set("")
						v.notice.Set("Offline - showing your saved copy.")
					}
				}
				settle(nil)
			})
		case err != nil:
			// Something WAS saved and could not be reopened: that is the
			// reader's own work going away, so it is said out loud rather than
			// silently replaced by a fresh clone.
			v.notice.Set(gitErrorMessage(err))
			s.gitClone(true, settle)
		default:
			s.gitClone(true, settle)
		}
	})
}

// gitRestore asks the backend to reopen a saved workspace, folding the result
// into the view the way a clone does. It reports whether one was found.
func (s *State) gitRestore(done func(restored bool, err error)) {
	v := s.git
	v.beginOp("Restoring")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Restore(v.config(), func(files []string, found bool, err error) {
		v.endOp()
		if !found {
			v.refresh()
			done(false, err)
			return
		}
		v.setFiles(files)
		v.buffers = map[string]*fileBuffer{}
		v.openTabs = nil
		s.SetBootNotice("")
		v.primaryPath = primaryTeX(files)
		v.log = v.backend.Log(gitLogLimit)
		v.refreshStatus()
		if v.primaryPath != "" {
			v.loadFresh(v.primaryPath)
		}
		v.bootBuffer = ""
		v.refresh()
		done(true, nil)
	})
}

// GitForget drops this browser's saved copy of the open repository, so the next
// visit clones afresh instead of reopening it.
//
// It deliberately does NOT touch what is open. A reader asking to forget a
// stored copy is asking about the future, not asking to lose the document they
// are editing — and a workspace that emptied itself on this button would be a
// destructive action wearing a harmless label.
func (s *State) GitForget(done func()) {
	v := s.git
	v.errMsg.Set("")
	v.backend.Forget(v.config(), func() {
		v.notice.Set("Saved copy dropped.")
		v.refresh()
		if done != nil {
			done()
		}
	})
}
