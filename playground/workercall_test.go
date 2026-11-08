package playground

import (
	"strings"
	"testing"
	"time"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// A reply that is already there is returned without waiting: the deadline is an
// upper bound on silence, never a delay added to a healthy call.
func TestAwaitReplyReturnsAnAnswerAtOnce(t *testing.T) {
	reply := make(chan gitrpc.Reply, 1)
	reply <- gitrpc.Reply{ID: 7, OK: true}

	start := time.Now()
	got, ok := awaitReply(reply, make(chan struct{}), time.Hour)
	if !ok {
		t.Fatal("awaitReply reported no answer when one was waiting")
	}
	if got.ID != 7 || !got.OK {
		t.Errorf("awaitReply returned %+v", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("awaitReply took %v for a reply already in the channel", elapsed)
	}
}

// Total silence is the only thing it refuses.
func TestAwaitReplyGivesUpOnSilence(t *testing.T) {
	start := time.Now()
	if _, ok := awaitReply(make(chan gitrpc.Reply), make(chan struct{}), 40*time.Millisecond); ok {
		t.Fatal("awaitReply reported an answer from a channel nothing was ever sent on")
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("awaitReply gave up after %v, before its own deadline", elapsed)
	}
}

// ⛔⛔ THE POINT OF THE WHOLE DESIGN. A clone of a large repository legitimately
// runs far longer than the deadline, and a call killed while it was still
// reporting progress would be a worse defect than the hang it replaces: a working
// clone turned into a failure the user cannot retry into success. So progress is
// proof of life and restarts the clock, and the test outlives the deadline
// several times over to say so.
func TestProgressKeepsALongOperationAlive(t *testing.T) {
	const idle = 60 * time.Millisecond
	reply := make(chan gitrpc.Reply, 1)
	alive := make(chan struct{}, 1)

	// Report progress every 20ms for ~5 idle periods, then answer.
	go func() {
		for i := 0; i < 15; i++ {
			time.Sleep(20 * time.Millisecond)
			select {
			case alive <- struct{}{}:
			default: // a dropped token is still an answered question
			}
		}
		reply <- gitrpc.Reply{ID: 3, OK: true}
	}()

	start := time.Now()
	got, ok := awaitReply(reply, alive, idle)
	if !ok {
		t.Fatalf("a call that reported progress throughout was called stalled after %v", time.Since(start))
	}
	if got.ID != 3 {
		t.Errorf("awaitReply returned %+v", got)
	}
	if elapsed := time.Since(start); elapsed < 4*idle {
		t.Errorf("the operation finished in %v, too fast to have outlived the %v deadline — the test proves nothing", elapsed, idle)
	}
}

// ⛔ A transport that forgets to wire the proof-of-life channel kills exactly the
// operations the deadline was written to protect, and it does so SILENTLY: every
// short call still works, so the mistake only shows on the long clone nobody
// runs in a unit test. Here the progress channel is never ready while the reply
// comes late, which is that mistake's exact shape, and the answer must be
// "stalled" — the deadline must still be real when nothing feeds it.
//
// (This is also the control for the test above, kept rather than discarded: with
// progress ignored, the long operation IS cut off, which is what makes the test
// above evidence of anything.)
func TestWithoutProofOfLifeALongCallIsCutOff(t *testing.T) {
	reply := make(chan gitrpc.Reply, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		reply <- gitrpc.Reply{ID: 5, OK: true}
	}()
	if _, ok := awaitReply(reply, make(chan struct{}), 40*time.Millisecond); ok {
		t.Fatal("a call with no proof-of-life channel outlived its idle deadline; the deadline is not real")
	}
}

// Progress that STOPS is the case the deadline is for: alive for a while, then
// silent, must be refused — otherwise a clone that dies half way through still
// hangs for ever and we have moved the defect rather than fixed it.
func TestProgressThatStopsIsStillStalled(t *testing.T) {
	const idle = 50 * time.Millisecond
	alive := make(chan struct{}, 1)
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(15 * time.Millisecond)
			select {
			case alive <- struct{}{}:
			default:
			}
		}
		// and then nothing, for ever
	}()

	if _, ok := awaitReply(make(chan gitrpc.Reply), alive, idle); ok {
		t.Fatal("a worker that went quiet after a few notifications was reported as having answered")
	}
}

// The refusal must say WHERE it stopped. A reader who is told "the clone was
// refused" looks at the remote; one told "it stopped responding" retries or
// reloads — and only the second is true here.
func TestStalledReplyNamesTheOperation(t *testing.T) {
	r := workerStalledReply(11, gitrpc.OpClone)
	if r.OK {
		t.Error("a stalled reply must not report OK")
	}
	if r.ID != 11 {
		t.Errorf("stalled reply ID = %d, want 11", r.ID)
	}
	if !strings.Contains(r.Error, gitrpc.OpClone) {
		t.Errorf("stalled reply %q does not name the operation", r.Error)
	}
	if !strings.Contains(r.Error, "stopped responding") {
		t.Errorf("stalled reply %q does not say it stopped responding", r.Error)
	}
	// ⛔ Distinct from the client never loading. Those are different problems with
	// different remedies, and one code for both would merge them again.
	if r.Code != "stalled" {
		t.Errorf("stalled reply code = %q, want \"stalled\" — distinct from \"worker\"", r.Code)
	}
	// An unnamed op still produces a sentence, not a dangling "during ".
	if e := workerStalledReply(1, "").Error; strings.Contains(e, "during  ") || strings.HasSuffix(e, "during ") {
		t.Errorf("an unnamed operation left a gap in %q", e)
	}
}

// The deadline has to be long enough that no healthy operation meets it and short
// enough to land inside the CI driver's own 90s wait — otherwise the diagnosis
// never reaches the log it was written for.
func TestCallIdleDeadlineIsBoundedAtBothEnds(t *testing.T) {
	if workerCallIdleDeadline < 30*time.Second {
		t.Errorf("workerCallIdleDeadline = %v, too short: a slow mid-clone pause would be called a hang", workerCallIdleDeadline)
	}
	if workerCallIdleDeadline >= 90*time.Second {
		t.Errorf("workerCallIdleDeadline = %v, too long: the browser proofs give up at 90s, so the reason would never be reported", workerCallIdleDeadline)
	}
}
