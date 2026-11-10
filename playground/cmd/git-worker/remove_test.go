package main

import (
	"strings"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// ⛔ Removal is the one file verb that cannot be expressed as a write. Creating a
// file is writeFile, importing one is writeFile, and so is the first half of a
// rename — but removing a path has no content to carry it, so a workspace with
// only writeFile can add files for ever and never lose one.
func TestRemoveFileDeletesAndAnswersWithWhatRemains(t *testing.T) {
	f := &fakeSession{
		hasRepo: true,
		files:   []string{"main.tex", "sections/old.tex", "refs.bib"},
		status:  gitrpc.Status{Branch: "main"},
	}
	h := newHandler(f)

	reply := h.dispatch(gitrpc.Request{ID: 4, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "sections/old.tex"}})
	if !reply.OK {
		t.Fatalf("removeFile reply = %+v", reply)
	}
	if f.gotRemove != "sections/old.tex" {
		t.Errorf("the session was asked to remove %q", f.gotRemove)
	}

	// ⛔ The reply carries the NEW file list, which writeFile has no need to. A
	// write leaves the set of paths as it was; a removal changes it, and a host
	// left holding the old list keeps showing a row for a file that is gone — and
	// offers to open it.
	if got := strings.Join(reply.Files, ","); got != "main.tex,refs.bib" {
		t.Errorf("reply.Files = %q, want the two survivors", got)
	}
	if !reply.HasRepo {
		t.Error("a removal must not make the reply look repo-less")
	}
	// Saved, because a removal the reader has not committed is still work and a
	// reload that brought the file back would undo a deliberate act.
	if f.persists == 0 {
		t.Error("the removal was not persisted")
	}
}

// A path that is already gone is NOT an error: removal's goal is a STATE — "this
// path is not here" — so asking twice, or asking for something a pull already
// deleted, has got the caller what they wanted. Failing there would force every
// honest caller to write a pre-check that races with the operation it guards.
func TestRemovingAVanishedPathSucceeds(t *testing.T) {
	f := &fakeSession{hasRepo: true, files: []string{"main.tex"}, status: gitrpc.Status{Branch: "main"}}
	h := newHandler(f)

	first := h.dispatch(gitrpc.Request{ID: 1, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "main.tex"}})
	if !first.OK {
		t.Fatalf("first removal failed: %+v", first)
	}
	second := h.dispatch(gitrpc.Request{ID: 2, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "main.tex"}})
	if !second.OK {
		t.Fatalf("removing an already-gone path reported %+v", second)
	}
	if len(second.Files) != 0 {
		t.Errorf("reply.Files = %v, want empty", second.Files)
	}
}

// A real failure still fails, or the tolerance above would swallow everything.
func TestRemoveFileReportsARealFailure(t *testing.T) {
	f := &fakeSession{hasRepo: true, files: []string{"main.tex"}, removeErr: browsergit.ErrTransport}
	h := newHandler(f)
	reply := h.dispatch(gitrpc.Request{ID: 9, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "main.tex"}})
	if reply.OK {
		t.Fatal("a transport failure was reported as a successful removal")
	}
	if reply.Code == "" {
		t.Errorf("the failure carries no code: %+v", reply)
	}
}

// ⛔ The removal succeeded and the LIST could not be read: that is reported as a
// failure, deliberately. The alternative — answer OK and carry the list we had
// before — hands the host a list that is now known to be wrong, and a host
// cannot tell a stale list from a current one. After this the list is UNKNOWN,
// not old, and saying so is the only honest reply.
func TestRemoveFileFailsWhenTheNewListCannotBeRead(t *testing.T) {
	f := &fakeSession{
		hasRepo: true,
		files:   []string{"main.tex", "gone.tex"},
		listErr: browsergit.ErrTransport,
		status:  gitrpc.Status{Branch: "main"},
	}
	h := newHandler(f)
	reply := h.dispatch(gitrpc.Request{ID: 7, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "gone.tex"}})
	if reply.OK {
		t.Fatal("an unreadable file list after a removal was reported as success")
	}
	// The removal itself did happen — this is not a claim that it was rolled back.
	if f.gotRemove != "gone.tex" {
		t.Errorf("the session was asked to remove %q", f.gotRemove)
	}
	if len(reply.Files) != 0 {
		t.Errorf("a failed reply must not carry a file list: %v", reply.Files)
	}
}

// And with no repository open it refuses like every other op that needs one,
// rather than quietly doing nothing.
func TestRemoveFileNeedsARepository(t *testing.T) {
	h := newHandler(&fakeSession{})
	reply := h.dispatch(gitrpc.Request{ID: 3, Op: gitrpc.OpRemoveFile, Args: gitrpc.Args{Path: "main.tex"}})
	if reply.OK {
		t.Fatal("removeFile succeeded with no repository open")
	}
}
