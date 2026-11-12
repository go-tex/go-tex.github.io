// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"sort"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// paneParts builds a real ForgePane over the model and returns the names of the
// controls it lays out.
//
// ⛔ IT READS THE PANE, NOT THE MODEL. What a person can click is the pane's
// business: asserting that Propose() returns nil says what I intended, and
// Parts() says what shipped. The two came apart once already in this repository
// — a status I believed reached the pixels did not — so the measurement is taken
// on the far side of the widget.
func paneParts(t *testing.T, m *forgeModel) []string {
	t.Helper()
	p := toolkit.NewForgePane(m)
	t.Cleanup(p.Close)
	p.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 320, H: 900})
	var names []string
	for k := range p.Parts() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func newTestForgeModel(t *testing.T) *forgeModel {
	t.Helper()
	s := newTestState(t, false)
	m := newForgeModel(s.git)
	t.Cleanup(m.close)
	return m
}

// ⛔ THE FOUR CAPABILITIES WE DO NOT HAVE MUST NOT REACH THE PANE AT ALL.
//
// A greyed button says "not now" and invites a person to hunt for the missing
// precondition. Ours would mean "never": we are a git transport, there is no
// forge API here to open a pull request with and no OAuth flow to sign in
// through. toolkit v0.373.0 removes the control of a nil command, and this
// asserts it on the pane rather than trusting the release note.
func TestTheForgeOnlyControlsAreAbsentFromThePane(t *testing.T) {
	got := paneParts(t, newTestForgeModel(t))
	joined := strings.Join(got, " ")
	for _, gone := range []string{"propose", "open", "signin", "ssignin"} {
		for _, name := range got {
			if name == gone {
				t.Errorf("%q is laid out, but its command is nil: a control for a capability "+
					"we will NEVER have. Parts = %v", gone, got)
			}
		}
	}
	// …and the transport half IS there, or this test would pass on a pane that
	// shows nothing at all — which is the way an absence check goes vacuous.
	for _, want := range []string{"commit", "push", "load", "refresh", "theirs", "connect", "disconnect"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is missing from the pane: the transport half did not arrive. Parts = %v", want, got)
		}
	}
}

// The three fields a forge flow has no home for are the reason this adoption was
// impossible before v0.373.0, so their arrival is asserted and not assumed.
func TestTheCommitMessageAndIdentityReachThePane(t *testing.T) {
	got := paneParts(t, newTestForgeModel(t))
	for _, want := range []string{"message", "author_name", "author_email"} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is not laid out: the ForgeCommitter half was not found by type "+
				"assertion. Parts = %v", want, got)
		}
	}
}

// ⚠ THE TWO CONTROLS THIS ADOPTION STILL SHOWS FOR NOTHING.
//
// This test does not fail. It RECORDS, so that the day the toolkit gains a rule
// for them the change is visible here rather than discovered. A drop-down and an
// entry are observables, not commands, so "a nil command hides its control"
// cannot reach them: the pane lays out the forge picker and the folder field
// unconditionally in the client layout, and neither changes anything we do.
//
// If this starts failing, the toolkit has fixed it and these lines come out.
func TestTwoControlsAreStillShownForNothing(t *testing.T) {
	got := paneParts(t, newTestForgeModel(t))
	for _, dead := range []string{"forge", "dir"} {
		found := false
		for _, name := range got {
			if name == dead {
				found = true
				break
			}
		}
		if !found {
			t.Logf("%q is no longer laid out — the toolkit gained a rule for a dead "+
				"OBSERVABLE, and this test and its workaround can go", dead)
		}
	}
}

// The pane shows ONE note; we have an error, an operation in flight and a
// notice. An error outranks everything because it is the one state a person must
// act on, and a running operation outranks a notice because "Pulling…" explains
// a frozen panel where a stale notice does not.
func TestTheNoteSaysWhatMostNeedsSaying(t *testing.T) {
	m := newTestForgeModel(t)
	v := m.v

	v.notice.Set("workspace restored")
	if got := m.Note().Get(); got != "workspace restored" {
		t.Errorf("note = %q, want the notice", got)
	}
	v.op.Set("Pulling")
	if got := m.Note().Get(); got != "Pulling…" {
		t.Errorf("note = %q, want the operation to outrank the notice", got)
	}
	v.errMsg.Set("remote refused: 403")
	if got := m.Note().Get(); got != "remote refused: 403" {
		t.Errorf("note = %q, want the error to outrank everything", got)
	}
	v.errMsg.Set("")
	if got := m.Note().Get(); got != "Pulling…" {
		t.Errorf("note = %q: clearing the error must fall back, not blank the pane", got)
	}
}

// Every command refuses while an operation is in flight, which is the rule the
// hand-rolled panel already had through its disabled buttons. The pane greys
// from CanExecute, so the rule has to live in the commands now.
func TestNoCommandRunsWhileAnOperationIsInFlight(t *testing.T) {
	m := newTestForgeModel(t)
	for _, c := range m.allCommands() {
		if !c.CanExecute() {
			t.Fatalf("a command refuses while idle: the test would pass for the wrong reason")
		}
	}
	m.v.busy.Set(true)
	for i, c := range m.allCommands() {
		if c.CanExecute() {
			t.Errorf("command %d still runs while busy", i)
		}
	}
	m.v.busy.Set(false)
	for i, c := range m.allCommands() {
		if !c.CanExecute() {
			t.Errorf("command %d stayed refused after the operation finished", i)
		}
	}
}

// The model is bound to the SAME observables the panel already drives, not to
// copies of them: a URL typed into the pane must be the URL a clone uses.
func TestTheModelSharesThePanelsObservablesRatherThanCopyingThem(t *testing.T) {
	m := newTestForgeModel(t)
	m.Repo().Set("https://example.invalid/o/r.git")
	if got := m.v.s.GitURL(); got != "https://example.invalid/o/r.git" {
		t.Errorf("GitURL = %q: the pane's field is a copy, so typing in it changes nothing", got)
	}
	m.Message().Set("a subject line")
	if got := m.v.s.GitCommitMessage(); got != "a subject line" {
		t.Errorf("GitCommitMessage = %q: the message field is a copy", got)
	}
}
