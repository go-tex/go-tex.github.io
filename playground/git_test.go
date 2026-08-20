// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
)

// fakeGitBackend is an in-memory [gitBackend] for the panel's state-machine
// tests. By default it replies through done synchronously; with hold=true it
// parks each op's done so a test can observe the busy state mid-flight and then
// release it.
type fakeGitBackend struct {
	files     []string
	fileData  map[string]string
	status    gitStatus
	statusOK  bool
	log       []GitCommitInfo
	hasRepo   bool
	readErr   error
	cloneErr  error
	pullErr   error
	commitErr error
	pushErr   error

	gotCfg           gitConfig
	gotCommitPath    string
	gotCommitContent string
	gotCommitMsg     string
	cloneCalls       int

	hold    bool
	release func() // parked done, invoked by the test when hold is set
}

func (f *fakeGitBackend) Clone(cfg gitConfig, done func([]string, error)) {
	f.cloneCalls++
	f.gotCfg = cfg
	fire := func() {
		if f.cloneErr != nil {
			done(nil, f.cloneErr)
			return
		}
		f.hasRepo = true
		done(f.files, nil)
	}
	if f.hold {
		f.release = fire
		return
	}
	fire()
}

func (f *fakeGitBackend) Pull(done func(error)) {
	if f.hold {
		f.release = func() { done(f.pullErr) }
		return
	}
	done(f.pullErr)
}

func (f *fakeGitBackend) Commit(path, content, message string, done func(error)) {
	f.gotCommitPath, f.gotCommitContent, f.gotCommitMsg = path, content, message
	if f.hold {
		f.release = func() { done(f.commitErr) }
		return
	}
	done(f.commitErr)
}

func (f *fakeGitBackend) Push(done func(error)) {
	if f.hold {
		f.release = func() { done(f.pushErr) }
		return
	}
	done(f.pushErr)
}

func (f *fakeGitBackend) ReadFile(path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if d, ok := f.fileData[path]; ok {
		return []byte(d), nil
	}
	return nil, browsergit.ErrNotExist
}

func (f *fakeGitBackend) Status() (gitStatus, bool) { return f.status, f.statusOK }
func (f *fakeGitBackend) Log(int) []GitCommitInfo   { return f.log }
func (f *fakeGitBackend) HasRepo() bool             { return f.hasRepo }

// withGit attaches a fresh fakeGitBackend to a fresh State, returning both plus
// a repaint counter.
func withGit(t *testing.T) (*State, *fakeGitBackend, *int) {
	t.Helper()
	s := newTestState(t, false)
	f := &fakeGitBackend{fileData: map[string]string{}}
	n := 0
	s.git.attach(f, func() { n++ })
	return s, f, &n
}

func TestGitConfigOptionsMapping(t *testing.T) {
	c := gitConfig{URL: "  https://forge/owner/repo.git  ", Branch: "dev", Token: " tok ", Author: " Ada ", Email: " ada@x "}
	o := c.options()
	if o.BaseURL != "https://forge/owner/repo.git" || o.Token != "tok" || o.Author != "Ada" || o.Email != "ada@x" {
		t.Fatalf("options mapping trimmed wrong: %+v", o)
	}
	if o.Provider != "generic" {
		t.Fatalf("provider = %q, want generic", o.Provider)
	}
}

func TestTeXFilesAndPrimary(t *testing.T) {
	files := []string{"README.md", "chapters/a.tex", "main.tex", "img.png", "b.TEX"}
	tex := texFiles(files)
	if len(tex) != 3 || tex[0] != "chapters/a.tex" || tex[1] != "main.tex" || tex[2] != "b.TEX" {
		t.Fatalf("texFiles = %v", tex)
	}
	// main.tex wins when present.
	if got := primaryTeX(files); got != "main.tex" {
		t.Fatalf("primaryTeX with main.tex = %q", got)
	}
	// No main.tex: the shallowest, then lexical, wins.
	if got := primaryTeX([]string{"deep/dir/z.tex", "top.tex", "deep/a.tex"}); got != "top.tex" {
		t.Fatalf("primaryTeX shallowest = %q, want top.tex", got)
	}
	if got := primaryTeX([]string{"z/one.tex", "a/two.tex"}); got != "a/two.tex" {
		t.Fatalf("primaryTeX same depth lexical = %q, want a/two.tex", got)
	}
	// No .tex at all.
	if got := primaryTeX([]string{"README.md"}); got != "" {
		t.Fatalf("primaryTeX no tex = %q, want empty", got)
	}
}

func TestGitErrorMessage(t *testing.T) {
	cases := []struct {
		in   error
		want string
	}{
		{nil, ""},
		{browsergit.ErrAuth, "Authentication failed — check your access token."},
		{browsergit.ErrNonFastForward, "Push rejected: the remote moved on. Pull, then push again."},
		{browsergit.ErrRepoNotFound, "Repository not found — check the remote URL."},
		{browsergit.ErrTransport, "Network/CORS error reaching the remote — is it CORS-enabled?"},
		{errNoGitRepo, "Clone a repository first."},
		{errNoGitFile, "Open a file before committing."},
		{errors.New("weird"), "weird"},
	}
	for _, tc := range cases {
		if got := gitErrorMessage(tc.in); got != tc.want {
			t.Fatalf("gitErrorMessage(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGitCloneLoadsPrimaryTeX(t *testing.T) {
	s, f, n := withGit(t)
	f.files = []string{"README.md", "main.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}Hi\end{document}`
	f.status = gitStatus{Branch: "main", Clean: true}
	f.statusOK = true
	f.log = []GitCommitInfo{{Hash: "abcdef1234", Subject: "seed", Author: "seed"}}
	s.SetGitURL("https://forge/owner/repo.git")

	var gotErr error
	called := false
	s.GitClone(func(err error) { gotErr, called = err, true })
	if !called || gotErr != nil {
		t.Fatalf("clone done: called=%v err=%v", called, gotErr)
	}
	if f.gotCfg.URL != "https://forge/owner/repo.git" {
		t.Fatalf("backend got cfg URL %q", f.gotCfg.URL)
	}
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("loaded path = %q, want main.tex", s.GitLoadedPath())
	}
	if s.Source() != f.fileData["main.tex"] {
		t.Fatalf("editor source not loaded from clone: %q", s.Source())
	}
	if s.GitNotice() == "" || s.GitError() != "" {
		t.Fatalf("notice=%q error=%q", s.GitNotice(), s.GitError())
	}
	if len(s.GitLog()) != 1 {
		t.Fatalf("log = %v", s.GitLog())
	}
	if *n == 0 {
		t.Fatal("clone did not repaint")
	}
}

func TestGitCloneNoTeX(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"README.md"}
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Clean: true}
	s.GitClone(nil)
	if s.GitLoadedPath() != "" {
		t.Fatalf("loaded = %q, want empty (no .tex)", s.GitLoadedPath())
	}
	if s.GitNotice() == "" {
		t.Fatal("expected a 'no .tex' notice")
	}
}

func TestGitCloneErrorAndBusyGuard(t *testing.T) {
	s, f, _ := withGit(t)
	f.cloneErr = browsergit.ErrRepoNotFound
	s.GitClone(nil)
	if s.GitError() == "" || s.GitLoadedPath() != "" {
		t.Fatalf("clone error not surfaced: err=%q", s.GitError())
	}

	// Busy guard: a second op while one is in flight is dropped.
	s2, f2, _ := withGit(t)
	f2.hold = true
	f2.files = []string{"main.tex"}
	f2.fileData["main.tex"] = "x"
	s2.GitClone(nil)
	if !s2.GitBusy() {
		t.Fatal("clone should be busy while held")
	}
	s2.GitClone(nil) // dropped by the busy guard
	if f2.cloneCalls != 1 {
		t.Fatalf("busy guard failed: cloneCalls=%d, want 1", f2.cloneCalls)
	}
	f2.release() // let the first clone finish
	if s2.GitBusy() {
		t.Fatal("clone should not be busy after release")
	}
}

func TestGitLoadFileReadError(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"main.tex"}
	f.readErr = browsergit.ErrTransport
	s.GitClone(nil)
	if s.GitError() == "" {
		t.Fatal("a read error during load should surface on the panel")
	}
}

func TestGitCommitWritesEditorSource(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = "seed"
	s.GitClone(nil)
	s.SetSource("edited body")
	s.SetGitCommitMessage("my message")

	var err error
	s.GitCommit(func(e error) { err = e })
	if err != nil {
		t.Fatalf("commit err = %v", err)
	}
	if f.gotCommitPath != "main.tex" || f.gotCommitContent != "edited body" || f.gotCommitMsg != "my message" {
		t.Fatalf("commit got path=%q content=%q msg=%q", f.gotCommitPath, f.gotCommitContent, f.gotCommitMsg)
	}
	if s.GitNotice() == "" {
		t.Fatal("commit notice missing")
	}
}

func TestGitCommitGuards(t *testing.T) {
	// No repo.
	s, _, _ := withGit(t)
	var err error
	s.GitCommit(func(e error) { err = e })
	if !errors.Is(err, errNoGitRepo) || s.GitError() == "" {
		t.Fatalf("no-repo commit: err=%v panelErr=%q", err, s.GitError())
	}

	// Repo but no file loaded.
	s2, f2, _ := withGit(t)
	f2.files = []string{"README.md"} // no .tex → nothing loaded
	s2.GitClone(nil)
	err = nil
	s2.GitCommit(func(e error) { err = e })
	if !errors.Is(err, errNoGitFile) {
		t.Fatalf("no-file commit err = %v, want errNoGitFile", err)
	}
}

func TestGitCommitErrorAndBusy(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = "x"
	s.GitClone(nil)
	f.commitErr = browsergit.ErrAuth
	f.hold = true
	s.GitCommit(nil)
	if !s.GitBusy() {
		t.Fatal("commit should be busy while held")
	}
	s.GitCommit(nil) // busy guard: dropped
	f.release()
	if s.GitError() == "" || s.GitBusy() {
		t.Fatalf("commit error path: err=%q busy=%v", s.GitError(), s.GitBusy())
	}
}

func TestGitPushSuccessErrorAndGuards(t *testing.T) {
	// No repo.
	s, _, _ := withGit(t)
	var err error
	s.GitPush(func(e error) { err = e })
	if !errors.Is(err, errNoGitRepo) {
		t.Fatalf("no-repo push err = %v", err)
	}

	// Success.
	s2, f2, _ := withGit(t)
	f2.files = []string{"main.tex"}
	f2.fileData["main.tex"] = "x"
	f2.statusOK = true
	f2.status = gitStatus{Branch: "main", Clean: true}
	s2.GitClone(nil)
	pushed := false
	s2.GitPush(func(e error) { pushed = e == nil })
	if !pushed || s2.GitError() != "" || s2.GitNotice() == "" {
		t.Fatalf("push success: pushed=%v err=%q notice=%q", pushed, s2.GitError(), s2.GitNotice())
	}

	// Error + busy.
	s3, f3, _ := withGit(t)
	f3.files = []string{"main.tex"}
	f3.fileData["main.tex"] = "x"
	s3.GitClone(nil)
	f3.pushErr = browsergit.ErrNonFastForward
	f3.hold = true
	s3.GitPush(nil)
	if !s3.GitBusy() {
		t.Fatal("push should be busy while held")
	}
	s3.GitPush(nil) // busy guard
	f3.release()
	if s3.GitError() == "" {
		t.Fatal("push error not surfaced")
	}
}

func TestGitPullReloadsAndGuards(t *testing.T) {
	// No repo.
	s, _, _ := withGit(t)
	var err error
	s.GitPull(func(e error) { err = e })
	if !errors.Is(err, errNoGitRepo) {
		t.Fatalf("no-repo pull err = %v", err)
	}

	// Success reloads the loaded file (its content changed on the remote).
	s2, f2, _ := withGit(t)
	f2.files = []string{"main.tex"}
	f2.fileData["main.tex"] = "before"
	f2.statusOK = true
	f2.status = gitStatus{Branch: "main", Clean: true}
	s2.GitClone(nil)
	f2.fileData["main.tex"] = "after pull"
	pulled := false
	s2.GitPull(func(e error) { pulled = e == nil })
	if !pulled || s2.Source() != "after pull" {
		t.Fatalf("pull did not reload the file: pulled=%v source=%q", pulled, s2.Source())
	}
	if s2.GitNotice() == "" {
		t.Fatal("pull notice missing")
	}

	// A pull with no file loaded (a repo without a .tex) skips the reload branch.
	s4, f4, _ := withGit(t)
	f4.files = []string{"README.md"}
	f4.statusOK = true
	f4.status = gitStatus{Branch: "main", Clean: true}
	s4.GitClone(nil)
	s4.GitPull(nil)
	if s4.GitError() != "" {
		t.Fatalf("no-file pull error = %q", s4.GitError())
	}

	// Error + busy.
	s3, f3, _ := withGit(t)
	f3.files = []string{"main.tex"}
	f3.fileData["main.tex"] = "x"
	s3.GitClone(nil)
	f3.pullErr = browsergit.ErrTransport
	f3.hold = true
	s3.GitPull(nil)
	if !s3.GitBusy() {
		t.Fatal("pull should be busy while held")
	}
	s3.GitPull(nil) // busy guard
	f3.release()
	if s3.GitError() == "" {
		t.Fatal("pull error not surfaced")
	}
}

func TestGitStatusLine(t *testing.T) {
	s, f, _ := withGit(t)
	if s.GitStatusLine() != "No repository cloned." {
		t.Fatalf("no-repo status line = %q", s.GitStatusLine())
	}
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Clean: true}
	s.git.refreshStatus()
	if got := s.GitStatusLine(); got != "On main — clean" {
		t.Fatalf("clean status line = %q", got)
	}
	f.status = gitStatus{Branch: "main", Ahead: 2, Behind: 1, Clean: false, DirtyFile: 3}
	s.git.refreshStatus()
	if got := s.GitStatusLine(); got != "On main (ahead 2, behind 1) — 3 changed" {
		t.Fatalf("dirty status line = %q", got)
	}
}

func TestGitConfigSettersAndGetters(t *testing.T) {
	s, _, _ := withGit(t)
	s.SetGitURL("u")
	s.SetGitBranch("b")
	s.SetGitToken("t")
	s.SetGitAuthor("a")
	s.SetGitEmail("e")
	s.SetGitCommitMessage("m")
	if s.GitURL() != "u" || s.GitBranch() != "b" || s.GitCommitMessage() != "m" {
		t.Fatalf("config getters wrong: %q %q %q", s.GitURL(), s.GitBranch(), s.GitCommitMessage())
	}
	cfg := s.git.config()
	if cfg.Token != "t" || cfg.Author != "a" || cfg.Email != "e" {
		t.Fatalf("config() = %+v", cfg)
	}
}

func TestGitShortHelpers(t *testing.T) {
	if shortHash("abcdef1234567") != "abcdef1" {
		t.Fatalf("shortHash long = %q", shortHash("abcdef1234567"))
	}
	if shortHash("abc") != "abc" {
		t.Fatalf("shortHash short = %q", shortHash("abc"))
	}
	if shortName("main.tex") != "main.tex" {
		t.Fatalf("shortName top = %q", shortName("main.tex"))
	}
	if shortName("a/b") != "a/b" {
		t.Fatalf("shortName two = %q", shortName("a/b"))
	}
	if shortName("a/b/c/d.tex") != ".../c/d.tex" {
		t.Fatalf("shortName deep = %q", shortName("a/b/c/d.tex"))
	}
}

func TestGitFieldObsNoneAndNextField(t *testing.T) {
	s, _, _ := withGit(t)
	v := s.git
	if v.fieldObs(gitFieldNone) != nil {
		t.Fatal("fieldObs(none) should be nil")
	}
	// nextField wraps URL→Branch→…→Message→URL.
	v.focus = gitFieldMessage
	if v.nextField() != gitFieldURL {
		t.Fatal("nextField should wrap Message→URL")
	}
	v.focus = gitFieldURL
	if v.nextField() != gitFieldBranch {
		t.Fatal("nextField URL→Branch")
	}
	// A focus not in the order falls back to URL.
	v.focus = gitFieldNone
	if v.nextField() != gitFieldURL {
		t.Fatal("nextField(none) should fall back to URL")
	}
}

func TestGitAccessorsAndOpen(t *testing.T) {
	s, _, _ := withGit(t)
	if s.GitActive() {
		t.Fatal("fresh panel should be closed")
	}
	s.SetGitOpen(true)
	if !s.GitActive() {
		t.Fatal("SetGitOpen(true) did not open")
	}
	s.SetGitOpen(false)
	if s.GitActive() {
		t.Fatal("SetGitOpen(false) did not close")
	}
	if s.GitBusy() {
		t.Fatal("fresh panel should not be busy")
	}
	if len(s.GitTeXFiles()) != 0 {
		t.Fatal("fresh panel should have no tex files")
	}
}

func TestNopGitBackend(t *testing.T) {
	var b nopGitBackend
	if b.HasRepo() {
		t.Fatal("nop backend has no repo")
	}
	if st, ok := b.Status(); ok || st.Branch != "" {
		t.Fatalf("nop status = %+v ok=%v", st, ok)
	}
	if b.Log(5) != nil {
		t.Fatal("nop log should be nil")
	}
	if _, err := b.ReadFile("x"); !errors.Is(err, errNoBrowserGit) {
		t.Fatalf("nop read err = %v", err)
	}
	var ce, pe, cme, pue error
	b.Clone(gitConfig{}, func(_ []string, e error) { ce = e })
	b.Pull(func(e error) { pue = e })
	b.Commit("p", "c", "m", func(e error) { cme = e })
	b.Push(func(e error) { pe = e })
	for _, e := range []error{ce, pe, cme, pue} {
		if !errors.Is(e, errNoBrowserGit) {
			t.Fatalf("nop op err = %v, want errNoBrowserGit", e)
		}
	}
}

// TestGitCloneThroughNopBackend proves a native State (default nop backend) turns
// a clone into a clear "needs a browser" panel error, without a fake.
func TestGitCloneThroughNopBackend(t *testing.T) {
	s := newTestState(t, false)
	s.GitClone(nil)
	if s.GitError() == "" {
		t.Fatal("nop-backed clone should surface an error")
	}
}
