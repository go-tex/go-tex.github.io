package playground

import (
	"testing"
	"time"
)

// ⛔⛔ THE GIT PANEL COULD WAIT FOR EVER, AND NOTHING COULD SAY SO.
//
// The browser transport blocked on its ready channel with no deadline, and the
// one case where that channel never closes is the one measured in #212: the
// worker asks for git-worker.wasm and nothing follows — no error event, so the
// error path never runs. Every git call then waited for ever behind an
// indeterminate progress bar, which is the honest rendering of "cannot tell how
// far" and cannot express "this is not coming".
//
// Reported by a user as "the git component is always very slow to appear without
// really knowing why". In that case it was not slow, it was hung.
//
// The policy lives in gitworker.go rather than beside the Web Worker precisely so
// that this test can exist off-wasm: a deadline written in a js-only file can
// only be checked in a browser, and the browser tests that would have caught
// this are the ones #212 has red.
func TestAwaitSignalGivesUpOnAWorkerThatNeverReports(t *testing.T) {
	// Never closed: the #212 case.
	never := make(chan struct{})
	start := time.Now()
	if awaitSignal(never, 20*time.Millisecond) {
		t.Error("awaitSignal said a channel that never closes had signalled")
	}
	if waited := time.Since(start); waited < 20*time.Millisecond {
		t.Errorf("gave up after %v, before its own deadline of 20ms", waited)
	}

	// Already closed: the ordinary case, and it must not wait at all.
	ready := make(chan struct{})
	close(ready)
	start = time.Now()
	if !awaitSignal(ready, time.Hour) {
		t.Error("awaitSignal missed a channel that was already closed")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("a ready worker cost %v — the deadline is an upper bound, not a delay", waited)
	}

	// Closed while waiting: the slow-but-fine case, which must succeed rather
	// than be called hung.
	late := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		close(late)
	}()
	if !awaitSignal(late, time.Minute) {
		t.Error("a worker that reported late was called hung")
	}
}

// The deadline has to outlast a slow transfer of a multi-megabyte wasm, and it
// has to be short enough that a reader is told something within one sitting.
// Pinned so neither is lost to a casual edit.
func TestWorkerBootDeadlineIsPlausible(t *testing.T) {
	if workerBootDeadline < 10*time.Second {
		t.Errorf("workerBootDeadline = %v: too short to outlast a slow download of "+
			"a multi-megabyte wasm, which would report a healthy worker as hung",
			workerBootDeadline)
	}
	if workerBootDeadline > 60*time.Second {
		t.Errorf("workerBootDeadline = %v: a reader who is told nothing for that long "+
			"has the defect this was written to remove", workerBootDeadline)
	}
}
