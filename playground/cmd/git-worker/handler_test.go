// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// fakeSession is a canned [gitSession] with per-method error injection, so the
// handler's dispatch, no-repo guards and error mapping are covered natively — no
// browser, no network, no git binary.
type fakeSession struct {
	hasRepo  bool
	files    []string
	contents map[string]string
	status   gitrpc.Status
	log      []gitrpc.Commit

	cloneErr, listErr, readErr, writeErr, commitErr, stageErr, pullErr, pushErr, statusErr, logErr error

	gotClone     [5]string
	gotWrite     [2]string
	gotCommitMsg string
	gotLogLimit  int
}

func (f *fakeSession) Clone(url, branch, token, author, email string) error {
	f.gotClone = [5]string{url, branch, token, author, email}
	if f.cloneErr != nil {
		return f.cloneErr
	}
	f.hasRepo = true
	return nil
}
func (f *fakeSession) List() ([]string, error) { return f.files, f.listErr }
func (f *fakeSession) ReadFile(p string) (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	return f.contents[p], nil
}
func (f *fakeSession) WriteFile(p, content string) error {
	f.gotWrite = [2]string{p, content}
	return f.writeErr
}
func (f *fakeSession) Commit(message string) error { f.gotCommitMsg = message; return f.commitErr }
func (f *fakeSession) Stage() error                { return f.stageErr }
func (f *fakeSession) Pull() error                 { return f.pullErr }
func (f *fakeSession) Push() error                 { return f.pushErr }
func (f *fakeSession) Status() (gitrpc.Status, error) {
	return f.status, f.statusErr
}
func (f *fakeSession) Log(limit int) ([]gitrpc.Commit, error) {
	f.gotLogLimit = limit
	return f.log, f.logErr
}
func (f *fakeSession) HasRepo() bool { return f.hasRepo }

// call dispatches one op with args and returns the reply.
func call(h *handler, op string, args gitrpc.Args) gitrpc.Reply {
	return h.dispatch(gitrpc.Request{ID: 1, Op: op, Args: args})
}

func TestHandleBadRequest(t *testing.T) {
	h := newHandler(&fakeSession{})
	reply, err := gitrpc.DecodeReply(h.handle("{not json"))
	if err != nil {
		t.Fatalf("reply decode: %v", err)
	}
	if reply.OK || reply.Code != gitrpc.CodeBadRequest || reply.ID != 0 {
		t.Fatalf("bad-request reply = %+v", reply)
	}
}

func TestHandleRoundTrip(t *testing.T) {
	// A full handle() encode/decode over a real op, proving the wire path.
	h := newHandler(&fakeSession{hasRepo: true, files: []string{"main.tex"}})
	req := gitrpc.EncodeRequest(gitrpc.Request{ID: 42, Op: gitrpc.OpList})
	reply, err := gitrpc.DecodeReply(h.handle(req))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reply.OK || reply.ID != 42 || len(reply.Files) != 1 {
		t.Fatalf("list reply = %+v", reply)
	}
}

func TestDispatchUnknownOp(t *testing.T) {
	h := newHandler(&fakeSession{})
	reply := call(h, "frobnicate", gitrpc.Args{})
	if reply.OK || reply.Code != gitrpc.CodeBadRequest {
		t.Fatalf("unknown op reply = %+v", reply)
	}
}

func TestCloneSuccessSnapshot(t *testing.T) {
	f := &fakeSession{
		files:    []string{"main.tex", "img.png", "sub/a.TEX", "refs.bib", "README.md"},
		contents: map[string]string{"main.tex": "root", "sub/a.TEX": "sub", "refs.bib": "@book{}", "README.md": "readme"},
		status:   gitrpc.Status{Branch: "main", Ahead: 1, DirtyFile: 1},
		log:      []gitrpc.Commit{{Hash: "abc1234", Subject: "seed", Author: "Ada"}},
	}
	h := newHandler(f)
	reply := call(h, gitrpc.OpClone, gitrpc.Args{URL: "u", Branch: "main", Token: "t", Author: "a", Email: "e"})
	if !reply.OK || !reply.HasRepo {
		t.Fatalf("clone reply = %+v", reply)
	}
	if f.gotClone != [5]string{"u", "main", "t", "a", "e"} {
		t.Fatalf("clone args forwarded wrong: %v", f.gotClone)
	}
	if len(reply.Files) != 5 {
		t.Fatalf("files = %v", reply.Files)
	}
	// Every text-source file (the two .tex, the .bib and the .md) carries
	// contents; the binary img.png does not.
	if len(reply.Contents) != 4 || reply.Contents["main.tex"] != "root" || reply.Contents["sub/a.TEX"] != "sub" ||
		reply.Contents["refs.bib"] != "@book{}" || reply.Contents["README.md"] != "readme" {
		t.Fatalf("contents = %v", reply.Contents)
	}
	if _, ok := reply.Contents["img.png"]; ok {
		t.Fatalf("binary img.png should not be pre-cached: %v", reply.Contents)
	}
	if reply.Status == nil || reply.Status.Branch != "main" || len(reply.Log) != 1 {
		t.Fatalf("status/log = %+v %v", reply.Status, reply.Log)
	}
}

func TestCloneError(t *testing.T) {
	h := newHandler(&fakeSession{cloneErr: browsergit.ErrAuth})
	reply := call(h, gitrpc.OpClone, gitrpc.Args{URL: "u"})
	if reply.OK || reply.Code != gitrpc.CodeAuth {
		t.Fatalf("clone error reply = %+v", reply)
	}
}

func TestRequireRepoGuards(t *testing.T) {
	h := newHandler(&fakeSession{}) // no repo
	for _, op := range []string{gitrpc.OpList, gitrpc.OpReadFile, gitrpc.OpWriteFile, gitrpc.OpStatus, gitrpc.OpCommit, gitrpc.OpStage, gitrpc.OpPull, gitrpc.OpPush, gitrpc.OpLog} {
		reply := call(h, op, gitrpc.Args{})
		if reply.OK || reply.Code != gitrpc.CodeNoRepo {
			t.Fatalf("%s without a repo = %+v", op, reply)
		}
	}
}

func TestDiscreteReads(t *testing.T) {
	f := &fakeSession{
		hasRepo:  true,
		files:    []string{"main.tex"},
		contents: map[string]string{"main.tex": "hello"},
		status:   gitrpc.Status{Branch: "main", Clean: true},
		log:      []gitrpc.Commit{{Hash: "h1"}},
	}
	h := newHandler(f)

	if r := call(h, gitrpc.OpList, gitrpc.Args{}); !r.OK || len(r.Files) != 1 {
		t.Fatalf("list = %+v", r)
	}
	if r := call(h, gitrpc.OpReadFile, gitrpc.Args{Path: "main.tex"}); !r.OK || r.Content != "hello" {
		t.Fatalf("readFile = %+v", r)
	}
	if r := call(h, gitrpc.OpStatus, gitrpc.Args{}); !r.OK || r.Status == nil || r.Status.Branch != "main" {
		t.Fatalf("status = %+v", r)
	}
	if r := call(h, gitrpc.OpLog, gitrpc.Args{Limit: 3}); !r.OK || len(r.Log) != 1 {
		t.Fatalf("log = %+v", r)
	}
	if f.gotLogLimit != 3 {
		t.Fatalf("log limit not forwarded: %d", f.gotLogLimit)
	}
	if r := call(h, gitrpc.OpWriteFile, gitrpc.Args{Path: "x.tex", Content: "c"}); !r.OK {
		t.Fatalf("writeFile = %+v", r)
	}
	if f.gotWrite != [2]string{"x.tex", "c"} {
		t.Fatalf("writeFile args = %v", f.gotWrite)
	}
}

func TestReadFileNotExist(t *testing.T) {
	h := newHandler(&fakeSession{hasRepo: true, readErr: browsergit.ErrNotExist})
	reply := call(h, gitrpc.OpReadFile, gitrpc.Args{Path: "gone.tex"})
	if reply.OK || reply.Code != gitrpc.CodeNotExist {
		t.Fatalf("readFile not-exist = %+v", reply)
	}
}

func TestListAndWriteErrors(t *testing.T) {
	h := newHandler(&fakeSession{hasRepo: true, listErr: errors.New("io")})
	if r := call(h, gitrpc.OpList, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("list error = %+v", r)
	}
	h2 := newHandler(&fakeSession{hasRepo: true, writeErr: errors.New("io")})
	if r := call(h2, gitrpc.OpWriteFile, gitrpc.Args{Path: "p"}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("writeFile error = %+v", r)
	}
	h3 := newHandler(&fakeSession{hasRepo: true, statusErr: errors.New("io")})
	if r := call(h3, gitrpc.OpStatus, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("status error = %+v", r)
	}
	h4 := newHandler(&fakeSession{hasRepo: true, logErr: errors.New("io")})
	if r := call(h4, gitrpc.OpLog, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("log error = %+v", r)
	}
}

func TestCommitPushPull(t *testing.T) {
	base := func() *fakeSession {
		return &fakeSession{hasRepo: true, files: []string{"main.tex"}, contents: map[string]string{"main.tex": "x"}, status: gitrpc.Status{Branch: "main"}, log: []gitrpc.Commit{{Hash: "h"}}}
	}

	// Commit success: writeFile + commit, reply carries status + log, no Files.
	f := base()
	h := newHandler(f)
	r := call(h, gitrpc.OpCommit, gitrpc.Args{Path: "main.tex", Content: "edited", Message: "m"})
	if !r.OK || r.Status == nil || len(r.Log) != 1 || r.Files != nil {
		t.Fatalf("commit reply = %+v", r)
	}
	if f.gotWrite != [2]string{"main.tex", "edited"} || f.gotCommitMsg != "m" {
		t.Fatalf("commit forwarded write=%v msg=%q", f.gotWrite, f.gotCommitMsg)
	}

	// Commit errors: writeFile failure, then commit failure.
	fw := base()
	fw.writeErr = browsergit.ErrTransport
	if r := call(newHandler(fw), gitrpc.OpCommit, gitrpc.Args{Path: "p"}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("commit write error = %+v", r)
	}
	fc := base()
	fc.commitErr = browsergit.ErrRepoNotFound
	if r := call(newHandler(fc), gitrpc.OpCommit, gitrpc.Args{Path: "p"}); r.OK || r.Code != gitrpc.CodeRepoNotFound {
		t.Fatalf("commit error = %+v", r)
	}

	// Pull success carries Files (mutatingReply withFiles).
	if r := call(newHandler(base()), gitrpc.OpPull, gitrpc.Args{}); !r.OK || len(r.Files) != 1 {
		t.Fatalf("pull reply = %+v", r)
	}
	fp := base()
	fp.pullErr = browsergit.ErrTransport
	if r := call(newHandler(fp), gitrpc.OpPull, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("pull error = %+v", r)
	}

	// Push success (no Files), and push failure.
	if r := call(newHandler(base()), gitrpc.OpPush, gitrpc.Args{}); !r.OK || r.Files != nil {
		t.Fatalf("push reply = %+v", r)
	}
	fpush := base()
	fpush.pushErr = browsergit.ErrNonFastForward
	if r := call(newHandler(fpush), gitrpc.OpPush, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeNonFastForward {
		t.Fatalf("push error = %+v", r)
	}
}

func TestStage(t *testing.T) {
	base := func() *fakeSession {
		return &fakeSession{hasRepo: true, files: []string{"main.tex"}, contents: map[string]string{"main.tex": "x"}, status: gitrpc.Status{Branch: "main", Changes: []gitrpc.Change{{Path: "main.tex", Status: "staged"}}}, log: []gitrpc.Commit{{Hash: "h"}}}
	}

	// Stage success: writeFile + stage, reply carries status (with per-file
	// changes) + log, no Files, and no commit was made.
	f := base()
	r := call(newHandler(f), gitrpc.OpStage, gitrpc.Args{Path: "main.tex", Content: "edited"})
	if !r.OK || r.Status == nil || len(r.Status.Changes) != 1 || r.Files != nil {
		t.Fatalf("stage reply = %+v", r)
	}
	if f.gotWrite != [2]string{"main.tex", "edited"} || f.gotCommitMsg != "" {
		t.Fatalf("stage forwarded write=%v commitMsg=%q (must not commit)", f.gotWrite, f.gotCommitMsg)
	}

	// Stage errors: writeFile failure, then stage failure.
	fw := base()
	fw.writeErr = browsergit.ErrTransport
	if r := call(newHandler(fw), gitrpc.OpStage, gitrpc.Args{Path: "p"}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("stage write error = %+v", r)
	}
	fs := base()
	fs.stageErr = browsergit.ErrRepoNotFound
	if r := call(newHandler(fs), gitrpc.OpStage, gitrpc.Args{Path: "p"}); r.OK || r.Code != gitrpc.CodeRepoNotFound {
		t.Fatalf("stage error = %+v", r)
	}
}

// TestMutatingReplyErrorBranches drives clone (withFiles=true) with each snapshot
// step failing in turn, covering mutatingReply's error returns.
func TestMutatingReplyErrorBranches(t *testing.T) {
	texFile := []string{"main.tex"}

	// Status step fails.
	if r := call(newHandler(&fakeSession{files: texFile, statusErr: errors.New("s")}), gitrpc.OpClone, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeTransport {
		t.Fatalf("clone status-fail = %+v", r)
	}
	// Log step fails.
	if r := call(newHandler(&fakeSession{files: texFile, logErr: errors.New("l")}), gitrpc.OpClone, gitrpc.Args{}); r.OK {
		t.Fatalf("clone log-fail = %+v", r)
	}
	// List step fails.
	if r := call(newHandler(&fakeSession{listErr: errors.New("li")}), gitrpc.OpClone, gitrpc.Args{}); r.OK {
		t.Fatalf("clone list-fail = %+v", r)
	}
	// ReadFile of a .tex fails.
	if r := call(newHandler(&fakeSession{files: texFile, readErr: browsergit.ErrNotExist}), gitrpc.OpClone, gitrpc.Args{}); r.OK || r.Code != gitrpc.CodeNotExist {
		t.Fatalf("clone read-fail = %+v", r)
	}
}

func TestCodeForErr(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{browsergit.ErrAuth, gitrpc.CodeAuth},
		{browsergit.ErrNonFastForward, gitrpc.CodeNonFastForward},
		{browsergit.ErrRepoNotFound, gitrpc.CodeRepoNotFound},
		{browsergit.ErrNotExist, gitrpc.CodeNotExist},
		{browsergit.ErrTransport, gitrpc.CodeTransport},
		{errors.New("novel"), gitrpc.CodeTransport},
	}
	for _, tc := range cases {
		if got := codeForErr(tc.err); got != tc.want {
			t.Fatalf("codeForErr(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestIsTeX(t *testing.T) {
	if !isTeX("a/b.tex") || !isTeX("X.TEX") || isTeX("readme.md") || isTeX("noext") {
		t.Fatal("isTeX classification wrong")
	}
}

func TestIsTextSource(t *testing.T) {
	for _, f := range []string{"a/b.tex", "X.TEX", "pkg.sty", "book.CLS", "refs.bib", "readme.md", "notes.txt"} {
		if !isTextSource(f) {
			t.Fatalf("isTextSource(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"img.png", "a.pdf", "noext", "font.ttf"} {
		if isTextSource(f) {
			t.Fatalf("isTextSource(%q) = true, want false", f)
		}
	}
}
