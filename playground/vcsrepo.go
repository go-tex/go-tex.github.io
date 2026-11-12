// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"context"

	"github.com/go-widgets/vcs"
)

// gitRepo presents the Git panel's working tree as a [vcs.Repository], the
// interface [toolkit.FileTree] already knows how to be told about.
//
// ⛔ WE HAD RE-IMPLEMENTED THIS, BADLY, IN ONE FUNCTION. sidebar.fileStatus
// mapped our own status strings to toolkit.FileStatus by hand, and the widget
// we were feeding has carried SetRepository all along — with the three rules
// that function did not have: a FOLDER is marked when anything under it is
// (conflicts outranking), a refresh cancels the one in flight so a stale answer
// cannot overwrite a newer one, and marks refresh on every Entries set rather
// than when somebody remembers. go-widgets/vcs was already in our go.mod, as
// `// indirect`: pulled in by the toolkit, never used by us.
//
// The vocabulary is deliberately not git's — the six states are what git,
// Mercurial, Subversion, Jujutsu and Fossil share — so this adapter is the only
// place in the playground that knows our backend speaks git.
//
// ⛔ AND THE HOST STILL SETS WHAT THE REPOSITORY CANNOT KNOW. An editor buffer
// edited and not yet written is modified in the person's eyes and invisible to
// any working tree. FileTree's contract settles the collision for us: "a status
// set on a FileEntry by the host wins over the repository's" (RefreshStatus),
// so sidebar.fileStatus now carries only that half and this carries the rest.
type gitRepo struct{ v *gitView }

// Root is the working tree's top. The playground's tree is a browser file
// system with no OS path, so it is the virtual root rather than a directory
// anyone could open — the interface wants a string to make its paths relative
// to, and every path we report is already relative to this.
func (r gitRepo) Root() string { return "/" }

// Status reports the paths that are not unchanged.
//
// ⛔ IT READS A SNAPSHOT AND DOES NOT ASK THE WORKER, which is why this is safe
// to run on the UI goroutine and why [toolkit.FileTree.PostUI] is deliberately
// NOT set for this tree. The backend keeps the last status it received; a round
// trip to the git worker would have to be awaited, and in wasm a goroutine
// awaiting a postMessage reply on the event-loop goroutine waits for a message
// only that loop can deliver. PostUI exists for exactly that case and would be
// wrong here: it would move this read onto another goroutine and race it
// against the worker's reply writing the same field.
func (r gitRepo) Status(ctx context.Context) (map[string]vcs.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.v == nil {
		return nil, errNoStatusYet
	}
	// ⛔ NOT GATED ON statusOK, AND THAT WAS A MISTAKE I MADE AND MEASURED. The
	// first draft refused to answer until the backend had reported once,
	// reasoning that an empty map claims "everything is unchanged" where
	// silence would be honest. Nobody had asked for that property, the code
	// this replaces never had it, and three tests went red saying so — one of
	// them that "a workspace with modified and staged files paints exactly
	// like a clean one". An invented guarantee that changes behaviour is a
	// defect wearing a principle's clothes. What we hold is what we report.
	out := make(map[string]vcs.State, len(r.v.status.Changes))
	for _, c := range r.v.status.Changes {
		if s, ok := vcsState(c.Status); ok {
			out[c.Path] = s
		}
	}
	return out, nil
}

// refreshMarks re-asks the attached repository and re-marks the tree. The error
// is deliberately dropped: RefreshStatus keeps the previous marks on failure
// and publishes the reason on FileTree.StatusError, which is where a panel
// would read it — swallowing it here and showing nothing would be the choice
// worth objecting to, and this is not that.
func (b *sidebar) refreshMarks() {
	_ = b.tree.RefreshStatus(context.Background())
}

// errNoStatusYet is returned before the backend has reported once. It is an
// error and not an empty map on purpose: RefreshStatus keeps the previous marks
// on failure, and "I have not been told yet" must not read as "nothing has
// changed".
var errNoStatusYet = errNoStatus{}

type errNoStatus struct{}

func (errNoStatus) Error() string { return "git: no status reported yet" }

// vcsState maps a backend status word to the shared vocabulary. An unknown word
// is reported as unknown rather than guessed at: a wrong mark on a file is a
// statement about someone's work.
func vcsState(s string) (vcs.State, bool) {
	switch s {
	case "modified":
		return vcs.Modified, true
	case "untracked":
		return vcs.Untracked, true
	case "deleted":
		return vcs.Deleted, true
	case "staged":
		return vcs.Added, true
	}
	return vcs.Unchanged, false
}
