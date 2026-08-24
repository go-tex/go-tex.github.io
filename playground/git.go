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
//     via [State.SetSource]. Editing is then ordinary.
//   - Commit → the backend writes the editor's current source back to the loaded
//     path and commits it with the configured identity.
//   - Push → the backend pushes the branch to the origin.
//   - Pull → the backend fast-forwards, then the loaded file is reloaded.
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

// gitStatus is the display snapshot of the open repo's branch + divergence, the
// subset of [browsergit.Status] the panel shows.
type gitStatus struct {
	Branch    string
	Ahead     int
	Behind    int
	Clean     bool
	DirtyFile int
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
	// Pull fast-forwards the open repo against origin; done reports the error.
	Pull(done func(err error))
	// Commit writes content to path in the working tree, then commits with
	// message and the configured identity; done reports the error.
	Commit(path, content, message string, done func(err error))
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
	loaded *mvvm.Observable[string] // the working-tree path shown in the editor

	// derived display snapshots, refreshed on each successful op (not per frame).
	files    []string // the whole working-tree file list
	texFiles []string // just the .tex paths, for the picker
	status   gitStatus
	statusOK bool
	log      []GitCommitInfo

	// geometry, recomputed by layout() before every draw and hit-test.
	launcher toolkit.Rect
	panel    toolkit.Rect
	buttons  []gitItem
	labels   []gitLabel
	boxes    []gitFieldBox

	// Persistent toolkit widgets the panel is built from — created once and re-used
	// every frame so a Button keeps its pressed / hover face between the mousedown
	// that presses it and the mouseup that releases it, and an Entry keeps its
	// caret. The panel draws + routes events through these instead of hand-painting
	// rectangles and text. Buttons are keyed by role+arg (the file-picker has one
	// per .tex file); fields by their gitField.
	launcherBtn *toolkit.Button
	scrim       *toolkit.Backdrop
	card        *toolkit.Backdrop
	btns        map[string]*toolkit.Button
	entries     map[gitField]*toolkit.Entry
	labelPool   []*toolkit.Label
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
		url:     mvvm.NewObservable(""),
		branch:  mvvm.NewObservable(gitDefaultBranch),
		token:   mvvm.NewObservable(""),
		author:  mvvm.NewObservable(""),
		email:   mvvm.NewObservable(""),
		msg:     mvvm.NewObservable("Update from the go-tex playground"),
		busy:    mvvm.NewObservable(false),
		errMsg:  mvvm.NewObservable(""),
		notice:  mvvm.NewObservable(""),
		loaded:  mvvm.NewObservable(""),
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
func (s *State) GitClone(done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	v.busy.Set(true)
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Clone(v.config(), func(files []string, err error) {
		v.busy.Set(false)
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.setFiles(files)
			v.log = v.backend.Log(gitLogLimit)
			v.refreshStatus()
			if p := primaryTeX(files); p != "" {
				v.loadFile(p)
			} else {
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
	v.busy.Set(true)
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Pull(func(err error) {
		v.busy.Set(false)
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.log = v.backend.Log(gitLogLimit)
			v.refreshStatus()
			if p := v.loaded.Get(); p != "" {
				v.loadFile(p)
			}
			v.notice.Set("Pulled.")
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// GitCommit writes the editor's current source back to the loaded path and
// commits it.
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
	v.busy.Set(true)
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Commit(v.loaded.Get(), s.Source(), v.msg.Get(), func(err error) {
		v.busy.Set(false)
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.log = v.backend.Log(gitLogLimit)
			v.refreshStatus()
			v.notice.Set("Committed " + v.loaded.Get() + ".")
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
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
	v.busy.Set(true)
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.Push(func(err error) {
		v.busy.Set(false)
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

// loadFile reads path from the working tree and puts it in the source editor.
func (v *gitView) loadFile(p string) {
	data, err := v.backend.ReadFile(p)
	if err != nil {
		v.errMsg.Set(gitErrorMessage(err))
		return
	}
	v.loaded.Set(p)
	v.s.SetSource(string(data))
	v.notice.Set("Loaded " + p + ".")
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
		return "Network/CORS error reaching the remote — is it CORS-enabled?"
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

// gitPanelW / gitLauncherW are the panel + launcher logical sizes.
const (
	gitPanelW    = 420
	gitLauncherW = 64
)

// layout recomputes the launcher rect and, when open, the panel geometry and its
// interactive items. Idempotent and cheap; called before every draw and hit-test.
func (v *gitView) layout() {
	pad := toolkit.Scaled(8)
	bh := toolkit.Scaled(26)
	// Launcher pill: just left of the Collaborate launcher in the toolbar row.
	lw := toolkit.Scaled(gitLauncherW)
	gap := toolkit.Scaled(6)
	collabW := toolkit.Scaled(collabLauncherW)
	v.launcher = toolkit.Rect{X: v.s.w - collabW - pad - lw - gap, Y: toolkit.Scaled(4), W: lw, H: v.s.toolbarH - 2*toolkit.Scaled(4)}
	if v.s.toolbarH == 0 { // before the host's first layout
		v.launcher.H = bh
	}

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
	y := pad + v.s.toolbarH
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
	if v.launcherBtn != nil {
		return
	}
	v.launcherBtn = toolkit.NewButton("Git", func() { v.openPanel(); v.refresh() })
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

	v.launcherBtn.SetBounds(v.launcher)
	v.launcherBtn.Selected().Set(v.open)
	v.launcherBtn.Draw(p, theme)

	if !v.open {
		return
	}

	// Modal scrim + panel body, each a Backdrop rather than hand-filled rects.
	v.scrim.SetBounds(toolkit.Rect{X: 0, Y: v.s.toolbarH, W: v.s.w, H: v.s.h - v.s.toolbarH - v.s.statusH})
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
		v.launcherBtn.SetBounds(v.launcher)
		if v.launcherBtn.HitTest(x, y) {
			v.launcherBtn.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - v.launcher.X, Y: y - v.launcher.Y})
			return true
		}
		return false
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

// handleRelease ends a press: the launcher and every panel button clear their
// pressed face on the mouseup, so a depress is momentary. Called whenever this
// view captured the preceding press (even if the action closed the panel).
func (v *gitView) handleRelease(x, y int) {
	v.ensureWidgets()
	v.launcherBtn.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
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
			v.loadFile(v.texFiles[arg])
		}
	}
	v.refresh()
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
func (nopGitBackend) Commit(_, _, _ string, done func(error))       { done(errNoBrowserGit) }
func (nopGitBackend) Push(done func(error))                         { done(errNoBrowserGit) }
func (nopGitBackend) ReadFile(string) ([]byte, error)               { return nil, errNoBrowserGit }
func (nopGitBackend) Status() (gitStatus, bool)                     { return gitStatus{}, false }
func (nopGitBackend) Log(int) []GitCommitInfo                       { return nil }
func (nopGitBackend) HasRepo() bool                                 { return false }

// errNoBrowserGit explains why the no-op backend never clones.
var errNoBrowserGit = errors.New("git: remote git needs a browser")
