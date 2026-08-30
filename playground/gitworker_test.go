// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"sync"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// fakeTransport is a canned [workerTransport]: it returns a per-op reply and
// records the requests + spawn count. It is mutex-guarded because Call runs on the
// backend's op-goroutine while the test inspects from the main goroutine.
type fakeTransport struct {
	mu      sync.Mutex
	replies map[string]gitrpc.Reply
	calls   []gitrpc.Request
	spawns  int
}

func (f *fakeTransport) Spawn() {
	f.mu.Lock()
	f.spawns++
	f.mu.Unlock()
}

func (f *fakeTransport) Call(req gitrpc.Request) gitrpc.Reply {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	r := f.replies[req.Op]
	f.mu.Unlock()
	return r
}

func (f *fakeTransport) spawnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawns
}

func (f *fakeTransport) lastArgs(op string) (gitrpc.Args, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Op == op {
			return f.calls[i].Args, true
		}
	}
	return gitrpc.Args{}, false
}

// cloneSync drives Clone and blocks for its done callback.
func cloneSync(b *workerGitBackend, cfg gitConfig) ([]string, error) {
	type res struct {
		f []string
		e error
	}
	ch := make(chan res, 1)
	b.Clone(cfg, func(f []string, e error) { ch <- res{f, e} })
	r := <-ch
	return r.f, r.e
}

// errSync drives an error-only async op (Pull/Push/Commit) and blocks for done.
func errSync(run func(done func(error))) error {
	ch := make(chan error, 1)
	run(func(e error) { ch <- e })
	return <-ch
}

func TestWorkerBackendCloneSuccess(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone: {
			OK:       true,
			Files:    []string{"main.tex", "README.md"},
			Contents: map[string]string{"main.tex": "body"},
			Status:   &gitrpc.Status{Branch: "main", Ahead: 1, Behind: 0, Clean: false, DirtyFile: 2},
			Log:      []gitrpc.Commit{{Hash: "abc1234", Subject: "seed", Author: "Ada"}},
		},
	}}
	b := newWorkerGitBackend(tr)

	files, err := cloneSync(b, gitConfig{URL: " https://forge/o/r.git ", Branch: "main"})
	if err != nil {
		t.Fatalf("clone err = %v", err)
	}
	if len(files) != 2 || !b.HasRepo() {
		t.Fatalf("files=%v hasRepo=%v", files, b.HasRepo())
	}
	// The clone args were trimmed by argsFromConfig.
	if a, _ := tr.lastArgs(gitrpc.OpClone); a.URL != "https://forge/o/r.git" {
		t.Fatalf("clone URL arg = %q", a.URL)
	}
	// Contents cached → ReadFile serves without a round-trip.
	data, err := b.ReadFile("main.tex")
	if err != nil || string(data) != "body" {
		t.Fatalf("ReadFile = %q err=%v", data, err)
	}
	if _, err := b.ReadFile("absent.tex"); !errors.Is(err, errGitNotExist) {
		t.Fatalf("ReadFile(absent) err = %v", err)
	}
	st, ok := b.Status()
	if !ok || st.Branch != "main" || st.DirtyFile != 2 {
		t.Fatalf("status = %+v ok=%v", st, ok)
	}
	if log := b.Log(5); len(log) != 1 || log[0].Hash != "abc1234" {
		t.Fatalf("log = %v", log)
	}
}

func TestWorkerBackendCloneError(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone: {OK: false, Code: gitrpc.CodeRepoNotFound, Error: "gone"},
	}}
	b := newWorkerGitBackend(tr)
	files, err := cloneSync(b, gitConfig{URL: "u"})
	if files != nil || !errors.Is(err, errGitRepoNotFound) {
		t.Fatalf("clone error: files=%v err=%v", files, err)
	}
	if b.HasRepo() {
		t.Fatal("a failed clone must not mark a repo open")
	}
}

func TestWorkerBackendCommitPushPull(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone:  {OK: true, Files: []string{"main.tex"}, Contents: map[string]string{"main.tex": "seed"}, Status: &gitrpc.Status{Branch: "main", Clean: true}},
		gitrpc.OpCommit: {OK: true, Status: &gitrpc.Status{Branch: "main", Ahead: 1, Clean: true}, Log: []gitrpc.Commit{{Hash: "c0ffee1", Subject: "edit"}}},
		gitrpc.OpPush:   {OK: true, Status: &gitrpc.Status{Branch: "main", Clean: true}},
		gitrpc.OpPull:   {OK: true, Files: []string{"main.tex"}, Contents: map[string]string{"main.tex": "pulled"}, Status: &gitrpc.Status{Branch: "main", Clean: true}},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}

	if err := errSync(func(done func(error)) { b.Commit("main.tex", "new body", "msg", done) }); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Commit updated the content cache and folded the fresh log.
	if data, _ := b.ReadFile("main.tex"); string(data) != "new body" {
		t.Fatalf("commit did not update the cache: %q", data)
	}
	if a, _ := tr.lastArgs(gitrpc.OpCommit); a.Path != "main.tex" || a.Content != "new body" || a.Message != "msg" {
		t.Fatalf("commit args = %+v", a)
	}
	if log := b.Log(5); len(log) != 1 || log[0].Hash != "c0ffee1" {
		t.Fatalf("commit log = %v", log)
	}

	if err := errSync(b.Push); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := errSync(b.Pull); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if data, _ := b.ReadFile("main.tex"); string(data) != "pulled" {
		t.Fatalf("pull did not refresh the cache: %q", data)
	}
}

func TestWorkerBackendStage(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone: {OK: true, Files: []string{"main.tex"}, Contents: map[string]string{"main.tex": "seed"}, Status: &gitrpc.Status{Branch: "main", Clean: true}},
		gitrpc.OpStage: {OK: true, Status: &gitrpc.Status{Branch: "main", DirtyFile: 1, Changes: []gitrpc.Change{{Path: "main.tex", Status: "staged"}}}},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := errSync(func(done func(error)) { b.Stage("main.tex", "staged body", done) }); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Stage forwarded the write and cached the content (no commit message).
	if a, _ := tr.lastArgs(gitrpc.OpStage); a.Path != "main.tex" || a.Content != "staged body" || a.Message != "" {
		t.Fatalf("stage args = %+v", a)
	}
	if data, _ := b.ReadFile("main.tex"); string(data) != "staged body" {
		t.Fatalf("stage did not update the cache: %q", data)
	}
	// The per-file changes threaded through into the cached status.
	st, ok := b.Status()
	if !ok || len(st.Changes) != 1 || st.Changes[0].Path != "main.tex" || st.Changes[0].Status != "staged" {
		t.Fatalf("stage status.Changes = %+v ok=%v", st.Changes, ok)
	}
}

func TestWorkerBackendStageError(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone: {OK: true, Files: []string{"main.tex"}, Status: &gitrpc.Status{Branch: "main"}},
		gitrpc.OpStage: {OK: false, Code: gitrpc.CodeTransport, Error: "boom"},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := errSync(func(done func(error)) { b.Stage("main.tex", "x", done) }); !errors.Is(err, errGitTransport) {
		t.Fatalf("stage err = %v", err)
	}
}

// TestWorkerBackendWriteFiles covers the multi-file write-back: an empty set is a
// no-op success, a real set writes + caches each file (so ReadFile serves the new
// content without a round-trip), and a write failure aborts and is mapped to a
// sentinel.
func TestWorkerBackendWriteFiles(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone:     {OK: true, Files: []string{"a.tex"}, Contents: map[string]string{"a.tex": "seed"}, Status: &gitrpc.Status{Branch: "main"}},
		gitrpc.OpWriteFile: {OK: true},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	// Empty set writes nothing and reports success.
	if err := errSync(func(done func(error)) { b.WriteFiles(nil, done) }); err != nil {
		t.Fatalf("empty WriteFiles err = %v", err)
	}
	// A real set writes each file and caches it.
	files := map[string]string{"a.tex": "A body", "b.sty": "B body"}
	if err := errSync(func(done func(error)) { b.WriteFiles(files, done) }); err != nil {
		t.Fatalf("WriteFiles err = %v", err)
	}
	for p, c := range files {
		if data, _ := b.ReadFile(p); string(data) != c {
			t.Fatalf("WriteFiles did not cache %s: %q", p, data)
		}
	}
	// A write failure aborts and maps onto a sentinel.
	tr.mu.Lock()
	tr.replies[gitrpc.OpWriteFile] = gitrpc.Reply{OK: false, Code: gitrpc.CodeTransport, Error: "boom"}
	tr.mu.Unlock()
	if err := errSync(func(done func(error)) { b.WriteFiles(map[string]string{"c.tex": "x"}, done) }); !errors.Is(err, errGitTransport) {
		t.Fatalf("WriteFiles error = %v", err)
	}
}

func TestChangesFrom(t *testing.T) {
	if changesFrom(nil) != nil {
		t.Fatal("changesFrom(nil) should be nil")
	}
	got := changesFrom([]gitrpc.Change{{Path: "p", Status: "modified"}})
	if len(got) != 1 || got[0].Path != "p" || got[0].Status != "modified" {
		t.Fatalf("changesFrom = %+v", got)
	}
}

func TestWorkerBackendMutatingErrors(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone:  {OK: true, Files: []string{"main.tex"}, Contents: map[string]string{"main.tex": "x"}, Status: &gitrpc.Status{Branch: "main"}},
		gitrpc.OpCommit: {OK: false, Code: gitrpc.CodeAuth, Error: "401"},
		gitrpc.OpPush:   {OK: false, Code: gitrpc.CodeNonFastForward, Error: "reject"},
		gitrpc.OpPull:   {OK: false, Code: gitrpc.CodeTransport, Error: "cors"},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := errSync(func(done func(error)) { b.Commit("main.tex", "c", "m", done) }); !errors.Is(err, errGitAuth) {
		t.Fatalf("commit err = %v", err)
	}
	if err := errSync(b.Push); !errors.Is(err, errGitNonFastForward) {
		t.Fatalf("push err = %v", err)
	}
	if err := errSync(b.Pull); !errors.Is(err, errGitTransport) {
		t.Fatalf("pull err = %v", err)
	}
}

// TestWorkerBackendApplyNilStatusAndEmptyLog covers the branches where a reply
// carries no status (statusOK stays false) and an empty log (cached as nil).
func TestWorkerBackendApplyNilStatusAndEmptyLog(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpClone: {OK: true, Files: nil, Status: nil, Log: nil},
	}}
	b := newWorkerGitBackend(tr)
	if _, err := cloneSync(b, gitConfig{URL: "u"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, ok := b.Status(); ok {
		t.Fatal("status should not be OK when the reply carried none")
	}
	if b.Log(5) != nil {
		t.Fatal("empty log should cache as nil")
	}
	if !b.HasRepo() {
		t.Fatal("a successful clone marks the repo open even with no status")
	}
}

func TestWorkerBackendPrewarm(t *testing.T) {
	tr := &fakeTransport{}
	b := newWorkerGitBackend(tr)
	b.Prewarm()
	b.Prewarm()
	if tr.spawnCount() != 2 {
		t.Fatalf("Prewarm should call Spawn each time: %d", tr.spawnCount())
	}
}

// TestOpenPanelPrewarmsWorkerBackend proves the panel prewarms a backend that
// implements gitPrewarmer (the worker-RPC backend) when it opens, so
// git-worker.wasm starts downloading on the first Git-panel open.
func TestOpenPanelPrewarmsWorkerBackend(t *testing.T) {
	s := newTestState(t, false)
	tr := &fakeTransport{}
	s.git.attach(newWorkerGitBackend(tr), func() {})

	s.SetGitOpen(true)
	if !s.GitActive() || tr.spawnCount() == 0 {
		t.Fatalf("opening the panel should prewarm the worker: open=%v spawns=%d", s.GitActive(), tr.spawnCount())
	}

	// Opening from the workspace sidebar also opens + prewarms.
	s2 := newTestState(t, false)
	tr2 := &fakeTransport{}
	s2.git.attach(newWorkerGitBackend(tr2), func() {})
	s2.sidebar.dispatch(sbRoleClone)
	if !s2.GitActive() || tr2.spawnCount() == 0 {
		t.Fatalf("opening from the workspace should prewarm the worker: spawns=%d", tr2.spawnCount())
	}
}

func TestCodeToError(t *testing.T) {
	cases := []struct {
		code, detail string
		want         error
	}{
		{gitrpc.CodeAuth, "x", errGitAuth},
		{gitrpc.CodeNonFastForward, "x", errGitNonFastForward},
		{gitrpc.CodeRepoNotFound, "x", errGitRepoNotFound},
		{gitrpc.CodeTransport, "x", errGitTransport},
		{gitrpc.CodeNotExist, "x", errGitNotExist},
		{gitrpc.CodeNoRepo, "x", errNoGitRepo},
	}
	for _, tc := range cases {
		if got := codeToError(tc.code, tc.detail); !errors.Is(got, tc.want) {
			t.Fatalf("codeToError(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
	// Unknown code keeps the detail verbatim.
	if got := codeToError("weird", "boom detail"); got.Error() != "boom detail" {
		t.Fatalf("unknown-code error = %q", got.Error())
	}
	// Unknown code with no detail falls back to a generic message.
	if got := codeToError("weird", ""); got.Error() != "git: worker error" {
		t.Fatalf("unknown-code empty-detail error = %q", got.Error())
	}
}

func TestCommitsFrom(t *testing.T) {
	if commitsFrom(nil) != nil {
		t.Fatal("commitsFrom(nil) should be nil")
	}
	got := commitsFrom([]gitrpc.Commit{{Hash: "h", Subject: "s", Author: "a"}})
	if len(got) != 1 || got[0].Hash != "h" || got[0].Subject != "s" || got[0].Author != "a" {
		t.Fatalf("commitsFrom = %+v", got)
	}
}

// restoreSync drives the async Restore and blocks for its callback.
func restoreSync(b *workerGitBackend, cfg gitConfig) ([]string, bool, error) {
	type res struct {
		f     []string
		found bool
		e     error
	}
	ch := make(chan res, 1)
	b.Restore(cfg, func(f []string, found bool, e error) { ch <- res{f, found, e} })
	r := <-ch
	return r.f, r.found, r.e
}

func TestWorkerBackendRestore(t *testing.T) {
	// A saved workspace comes back with its files, contents, status and log —
	// everything a clone returns, without the clone.
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpRestore: {
			OK:       true,
			Restored: true,
			Files:    []string{"main.tex"},
			Contents: map[string]string{"main.tex": "body"},
			Status:   &gitrpc.Status{Branch: "main", Clean: true},
			Log:      []gitrpc.Commit{{Hash: "abc1234", Subject: "seed", Author: "Ada"}},
		},
	}}
	b := newWorkerGitBackend(tr)
	files, found, err := restoreSync(b, gitConfig{URL: "https://forge/o/r.git", Branch: "main"})
	if err != nil || !found {
		t.Fatalf("restore found=%v err=%v, want found with no error", found, err)
	}
	if len(files) != 1 || files[0] != "main.tex" {
		t.Fatalf("files = %v", files)
	}
	if !b.HasRepo() {
		t.Fatal("a restored repository must count as open")
	}
	if data, err := b.ReadFile("main.tex"); err != nil || string(data) != "body" {
		t.Fatalf("contents were not cached: %q %v", data, err)
	}

	// Nothing saved: an ordinary answer, no error, no repo.
	tr = &fakeTransport{replies: map[string]gitrpc.Reply{gitrpc.OpRestore: {OK: true}}}
	b = newWorkerGitBackend(tr)
	files, found, err = restoreSync(b, gitConfig{URL: "https://forge/o/r.git"})
	if err != nil || found || files != nil {
		t.Fatalf("a first visit reported found=%v files=%v err=%v", found, files, err)
	}
	if b.HasRepo() {
		t.Fatal("nothing was restored, yet the backend claims a repository")
	}

	// A saved entry that cannot be reopened is an error the reader hears about.
	tr = &fakeTransport{replies: map[string]gitrpc.Reply{
		gitrpc.OpRestore: {OK: false, Code: gitrpc.CodeTransport, Error: "unreadable"},
	}}
	b = newWorkerGitBackend(tr)
	if _, found, err = restoreSync(b, gitConfig{}); err == nil || found {
		t.Fatalf("a failed restore reported found=%v err=%v", found, err)
	}
}

func TestWorkerBackendForget(t *testing.T) {
	tr := &fakeTransport{replies: map[string]gitrpc.Reply{gitrpc.OpForget: {OK: true}}}
	b := newWorkerGitBackend(tr)
	done := make(chan struct{})
	b.Forget(gitConfig{URL: " https://forge/o/r.git ", Branch: "main"}, func() { close(done) })
	<-done
	a, ok := tr.lastArgs(gitrpc.OpForget)
	if !ok {
		t.Fatal("the worker was never asked to forget anything")
	}
	if a.URL != "https://forge/o/r.git" || a.Branch != "main" {
		t.Fatalf("forget args = %+v, want the trimmed remote and its branch", a)
	}
}
