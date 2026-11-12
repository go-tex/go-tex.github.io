// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"
)

// forgeModel presents the Remote Git panel as a [toolkit.ForgeModel], so the
// pane is the toolkit's widget rather than 1934 lines of our own layout(),
// draw() and handleClick/handleMove/handleRelease/handleChar/handleKey.
//
// ⛔ OUR VERBS ARE A TRANSPORT'S, NOT A FORGE'S, AND THAT IS THE WHOLE DESIGN
// OF THIS FILE. ForgeModel was written for a forge REST flow — Load, Propose
// changes…, Check for changes, Take theirs, Open the pull request. Our backend
// is browsergit over HTTP: clone, commit, push against a remote, with a commit
// message and an author identity that the forge flow has no field for. Mapping
// "Propose changes…" onto commit+push would make the pane's own word wrong, and
// a person reading that button would be told something untrue about what it
// does. So Propose and OpenLink are NIL here, and toolkit v0.373.0 removes the
// control of a nil command rather than greying it (go-widgets#566) — a greyed
// button says "not now" and invites a hunt for the missing precondition, where
// ours would mean "never".
//
// ⛔ CAPABILITIES ARE DECIDED ONCE, HERE, AT CONSTRUCTION. The pane reads the
// commands when it is built, so a nil must mean "never" and not "not yet". A
// model that grew a capability later would have a widget that never notices.
//
// The transport half arrives through [toolkit.ForgeCommitter], which the pane
// finds by type assertion: Message, AuthorName, AuthorEmail, Commit and Push.
type forgeModel struct {
	v *gitView

	// note composes what three of our observables say into the one string the
	// pane shows. The pane has a single Note; we have an error, a notice and the
	// name of the operation in flight, and which of them matters depends on the
	// state rather than on a fixed priority.
	note *mvvm.Observable[string]
	// dir and forgePick exist only because the pane's client layout lays out a
	// folder entry and a forge drop-down unconditionally. See the ⚠ on Dir.
	dir       *mvvm.Observable[string]
	forgePick *mvvm.Observable[int]
	mode      *mvvm.Observable[toolkit.ForgeMode]
	user      *mvvm.Observable[string]
	repos     *mvvm.Observable[[]string]
	repoPick  *mvvm.Observable[int]

	connect, disconnect, load    *mvvm.Command
	refresh, theirs              *mvvm.Command
	commit, push                 *mvvm.Command
	unsubscribe                  []func()
	repoLabel                    string
	committerReady, commandsMade bool
}

// newForgeModel builds the model over an attached panel.
func newForgeModel(v *gitView) *forgeModel {
	m := &forgeModel{
		v:         v,
		note:      mvvm.NewObservable(""),
		dir:       mvvm.NewObservable(""),
		forgePick: mvvm.NewObservable(0),
		mode:      mvvm.NewObservable(toolkit.ForgeClient),
		user:      mvvm.NewObservable(""),
		repos:     mvvm.NewObservableEq([]string(nil), func(a, b []string) bool { return sliceEq(a, b) }),
		repoPick:  mvvm.NewObservable(0),
	}
	m.makeCommands()
	m.watchNote()
	return m
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// makeCommands wires each control to the panel action it really performs.
//
// ⛔ THE MAPPING IS WRITTEN DOWN BECAUSE IT IS NOT OBVIOUS, and a mapping nobody
// can check is a place for a button to quietly do the wrong thing:
//
//	Connect      reopen what this browser saved, WITHOUT the network (Restore).
//	             The token is already live through Token(); "Connect" here means
//	             "open the repository I am configured for", which for a browser
//	             workspace is the saved copy when there is one.
//	Load         Clone: fetch the remote at the configured branch.
//	Disconnect   Forget: drop this browser's saved copy. The OPEN repository is
//	             left alone, because forgetting is about the next visit.
//	Refresh      re-read the working tree's status (Check for changes).
//	Take theirs  Pull: fast-forward onto origin, which is what taking theirs
//	             means for a transport.
//	Commit/Push  the ForgeCommitter half, and the reason this adoption is
//	             possible at all.
//
// Every one refuses while an operation is in flight, which is what the panel's
// own disabled-button rule already did.
func (m *forgeModel) makeCommands() {
	idle := func() bool { return m.v != nil && !m.v.busy.Get() }
	m.connect = mvvm.NewCommand(func() { m.v.s.gitRestore(nil) }, idle)
	m.load = mvvm.NewCommand(func() { m.v.s.GitClone(nil) }, idle)
	m.disconnect = mvvm.NewCommand(func() { m.v.s.GitForget(nil) }, idle)
	m.refresh = mvvm.NewCommand(func() { m.v.refreshStatus() }, idle)
	m.theirs = mvvm.NewCommand(func() { m.v.s.GitPull(nil) }, idle)
	m.commit = mvvm.NewCommand(func() { m.v.s.GitCommit(nil) }, idle)
	m.push = mvvm.NewCommand(func() { m.v.s.GitPush(nil) }, idle)
	m.commandsMade = true
}

// watchNote recomputes the pane's single note whenever any of the three sources
// changes, and re-raises every command's CanExecute when the busy flag moves —
// the pane greys a button from CanExecute and is not otherwise told.
func (m *forgeModel) watchNote() {
	recompute := func() {
		m.note.Set(m.composeNote())
	}
	m.unsubscribe = append(m.unsubscribe,
		m.v.errMsg.Subscribe(func(string) { recompute() }),
		m.v.notice.Subscribe(func(string) { recompute() }),
		m.v.op.Subscribe(func(string) { recompute() }),
		m.v.busy.Subscribe(func(bool) {
			for _, c := range m.allCommands() {
				c.RaiseCanExecuteChanged()
			}
			recompute()
		}),
	)
	recompute()
}

func (m *forgeModel) allCommands() []*mvvm.Command {
	return []*mvvm.Command{m.connect, m.disconnect, m.load, m.refresh, m.theirs, m.commit, m.push}
}

// composeNote picks what the person most needs to read.
//
// An error outranks everything: it is the one state a person must act on. The
// operation in flight comes next, because "Pulling…" explains a frozen panel
// where silence does not. A notice is what is left.
func (m *forgeModel) composeNote() string {
	if e := m.v.errMsg.Get(); e != "" {
		return e
	}
	if op := m.v.op.Get(); op != "" {
		return op + "…"
	}
	return m.v.notice.Get()
}

// close detaches the model from the panel's observables.
func (m *forgeModel) close() {
	for _, u := range m.unsubscribe {
		u()
	}
	m.unsubscribe = nil
}

// --- ForgeModel ---------------------------------------------------------------

// Forges is the forge drop-down's options.
//
// ⚠ WE HAVE NO FORGE CHOICE, AND THE PANE SHOWS THE CONTROL ANYWAY. Our backend
// is generic git over HTTP: a remote URL is a remote URL, and nothing downstream
// branches on which forge serves it. The pane lays out the drop-down
// unconditionally in the client layout, and a drop-down is an observable rather
// than a command, so the "a nil command hides its control" rule cannot reach it.
// One honest option is the least misleading thing available: it names what the
// panel talks, rather than offering a choice that changes nothing.
func (m *forgeModel) Forges() []string { return []string{"Git"} }

func (m *forgeModel) Forge() *mvvm.Observable[int] { return m.forgePick }

// Repo is the remote. ForgePane documents the string as shown-as-given, so a
// transport model may put a full URL where a forge model puts "owner/name".
func (m *forgeModel) Repo() *mvvm.Observable[string] { return m.v.url }

func (m *forgeModel) Ref() *mvvm.Observable[string] { return m.v.branch }

// Dir is the folder within the repository to work in.
//
// ⚠ IT DOES NOTHING HERE, AND THAT IS A DEFECT THIS ADOPTION INTRODUCES. Our
// workspace is the whole working tree; there is no subdirectory selection, and
// nothing reads this. A field a person can type into that changes nothing is the
// same shape as a \hangindent a document sets and the engine drops — the engine
// got a gate for that today, and a widget should not acquire one. Raised with
// go-widgets: the folder row needs the same capability rule the commands have.
func (m *forgeModel) Dir() *mvvm.Observable[string] { return m.dir }

func (m *forgeModel) Token() *mvvm.Observable[string]  { return m.v.token }
func (m *forgeModel) Remember() *mvvm.Observable[bool] { return m.v.remember }
func (m *forgeModel) Note() *mvvm.Observable[string]   { return m.note }

func (m *forgeModel) Mode() *mvvm.Observable[toolkit.ForgeMode] { return m.mode }

// User, Repos and RepoPick belong to the server layout, which we never show:
// no server holds credentials for us. They are present because the interface
// requires them and are never read.
func (m *forgeModel) User() *mvvm.Observable[string]    { return m.user }
func (m *forgeModel) Repos() *mvvm.Observable[[]string] { return m.repos }
func (m *forgeModel) RepoPick() *mvvm.Observable[int]   { return m.repoPick }

func (m *forgeModel) Connect() *mvvm.Command    { return m.connect }
func (m *forgeModel) Disconnect() *mvvm.Command { return m.disconnect }
func (m *forgeModel) Load() *mvvm.Command       { return m.load }
func (m *forgeModel) Refresh() *mvvm.Command    { return m.refresh }
func (m *forgeModel) TakeTheirs() *mvvm.Command { return m.theirs }

// ⛔ NIL IS "NEVER", AND EACH OF THESE FOUR IS A CAPABILITY WE DO NOT HAVE.
//
//	SignIn / ServerSignIn   an OAuth flow against a forge. We authenticate with
//	                        a token the person pastes, and that is the whole of
//	                        it.
//	Propose                 "Propose changes…" opens a pull request. We commit
//	                        and push to a branch; there is no forge API here to
//	                        open anything with.
//	OpenLink                there is no pull request to open, because nothing
//	                        here opens one.
//
// Returning nil removes the control. Greying it would say "not now" about
// something that is never, which sends a person looking for a precondition that
// does not exist.
func (m *forgeModel) SignIn() *mvvm.Command       { return nil }
func (m *forgeModel) ServerSignIn() *mvvm.Command { return nil }
func (m *forgeModel) Propose() *mvvm.Command      { return nil }
func (m *forgeModel) OpenLink() *mvvm.Command     { return nil }

// --- ForgeCommitter -----------------------------------------------------------

func (m *forgeModel) Message() *mvvm.Observable[string]     { return m.v.msg }
func (m *forgeModel) AuthorName() *mvvm.Observable[string]  { return m.v.author }
func (m *forgeModel) AuthorEmail() *mvvm.Observable[string] { return m.v.email }
func (m *forgeModel) Commit() *mvvm.Command                 { return m.commit }
func (m *forgeModel) Push() *mvvm.Command                   { return m.push }

// The compiler is the check that this model satisfies both halves: the
// ForgeCommitter one is found by type assertion at runtime, so nothing else
// would notice a method that drifted.
var (
	_ toolkit.ForgeModel     = (*forgeModel)(nil)
	_ toolkit.ForgeCommitter = (*forgeModel)(nil)
)
