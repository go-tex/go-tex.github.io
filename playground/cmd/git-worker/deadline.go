// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"time"
)

// networkDeadline bounds a remote git operation inside the worker.
//
// ⛔⛔ IT IS A BOUND ON THE LEAK, NOT A JUDGEMENT ABOUT SPEED. The operations it
// covers were started with context.Background(), which can never be cancelled:
// under GOOS=js each one is a Fetch, and a fetch that never settles parks its
// goroutine for the life of the page. #212 is that state observed from outside —
// the clone is posted to a live worker and reports nothing at all, not even the
// first sideband line, so it never got past its opening exchange.
//
// The consequence is worse than the one hang. The main thread's idle deadline
// (playground/gitworker.go) lets the PAGE give up and say so, but the worker's
// goroutine stays parked, so a retry queues behind an operation that will never
// finish and only reloading the page helps. A context that always ends is what
// makes "try again" mean anything.
//
// So the value is deliberately far above any real operation — a shallow clone of
// a document repository is seconds, and the largest arXiv sources are under a
// megabyte — because its job is to guarantee termination, never to police a slow
// link. Anything tighter would risk cutting off a clone that was working, which
// is the mistake the main thread's deadline avoids by watching for silence rather
// than counting elapsed time; go-git's API here gives no such hook, so the honest
// substitute is a bound generous enough that reaching it means something is
// genuinely wrong.
const networkDeadline = 10 * time.Minute

// networkContext returns the context a remote operation runs under, and the
// cancel the caller must defer. It exists so the policy above is reachable from
// a native test: the session that uses it is js-only and cannot be.
func networkContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), networkDeadline)
}
