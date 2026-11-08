// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"context"
	"errors"
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

// storeDeadline bounds a single Cache API promise.
//
// ⛔⛔ THIS IS THE ONE THAT WEDGES THE WHOLE WORKER. Requests are drained by ONE
// goroutine, on purpose — it serialises access to a worktree that is not safe for
// concurrent use (main.go) — so a promise that never settles does not merely fail
// its own operation: every later request sits unhandled in the channel behind it,
// for ever.
//
// That is the shape of #212 as the origin's own trace finally showed it. The
// clone's reply never came AND the test's git origin recorded "not one request
// arrived": the clone was never executed at all, so nothing could have reached the
// network. The worker had already posted `ready`, so the boot deadline was right
// not to fire — and the one operation that runs before any clone, and the only one
// that touches this store, is the boot-time Restore.
//
// Five seconds, because this is a LOCAL store: opening a cache and reading one
// entry is sub-millisecond when it works at all. A browser that cannot answer in
// five seconds is a browser with no storage to offer — a private window, storage
// disabled, a non-secure context, an ephemeral profile in headless Chrome — and
// openCache already has an answer for that case. Failing it is strictly better
// than parking on it: a saved workspace is a convenience, and losing the
// convenience costs a clone, while parking costs the whole session.
const storeDeadline = 5 * time.Second

// errStoreUnresponsive is what a store call reports when its promise never
// settled. It is distinct from a rejection, because they mean different things:
// a rejection is the browser answering "no", and this is the browser not
// answering — the second is the one that used to take the worker down with it.
var errStoreUnresponsive = errors.New("the browser's cache storage did not answer within " + storeDeadline.String())
