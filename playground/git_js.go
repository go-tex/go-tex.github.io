// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground

import (
	"sync"
	"syscall/js"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// This is the browser transport behind the [workerTransport] seam: it runs the
// remote-git client OFF the page's main thread, in a Web Worker executing a
// separate git-worker.wasm. The main playground.wasm therefore never imports
// go-git — it is dead-code-eliminated out of the base download, and only a user
// who opens the Git panel pays for git-worker.wasm (loaded on demand).
//
// The main thread and the worker speak the [gitrpc] JSON protocol over
// postMessage: each request carries a correlation id, and a pending-id map wakes
// the op-goroutine that is blocked awaiting the reply. This file is excluded from
// the native coverage gate (it is pure syscall/js glue); its proof is the headless
// two-wasm clone→commit→push run in git_two_wasm_browser_test.go.
//
// # Threading
//
// WebAssembly runs Go on the page's single thread and yields to JavaScript only at
// blocking points. Call is invoked only from the backend's op-goroutines
// (Clone/Pull/Commit/Push), so its block on the reply channel parks that goroutine
// and returns control to the event loop; the worker's onmessage js.Func (running
// on the event-loop goroutine) then delivers the reply through the pending map and
// unparks the op-goroutine. The synchronous panel reads never reach this transport
// (the backend serves them from its cache), so the event-loop goroutine never
// blocks on a postMessage.

// EnableGit installs the worker-RPC backend on the playground's Git affordance.
// The wasm driver calls it once, after building the State, passing its repaint
// function.
func (s *State) EnableGit(repaint func()) {
	t := newJSWorkerTransport()
	// The worker's own wasm download reports its progress back to the page (see
	// static/git-worker.js). Route it into the State the workspace reads, and
	// repaint: without this the sidebar sits blank through a multi-megabyte
	// download with nothing to explain the wait.
	t.onProgress = func(frac float64) {
		s.SetAssetProgress(frac)
		if repaint != nil {
			repaint()
		}
	}
	// Landed: the git client is instantiated, so the wait that follows belongs to
	// the network operation, not to the download. Clearing the asset name hands
	// the workspace message over to the operation's own name.
	t.onReady = func() {
		s.SetAssetLoading("")
		if repaint != nil {
			repaint()
		}
	}
	s.git.attach(newWorkerGitBackend(t), repaint)
}

// jsWorkerTransport owns the Web Worker and correlates replies to the
// op-goroutines waiting on them.
type jsWorkerTransport struct {
	mu      sync.Mutex
	worker  js.Value
	spawned bool
	// dead is raised when the Worker itself failed — its script or its wasm did
	// not load. Every call then fails immediately instead of posting into a void.
	dead    bool
	nextID  int
	pending map[int]chan gitrpc.Reply

	readyOnce sync.Once
	readyCh   chan struct{}
	// onProgress / onReady report the worker's wasm download to the host so the
	// workspace can say what it is waiting for. Both are nil in the native tests
	// (which never build a real Worker) and are only ever called from the
	// event-loop goroutine, inside the message handler.
	onProgress func(frac float64)
	onReady    func()
	onmessage  js.Func
	onerror    js.Func
}

// newJSWorkerTransport builds an unspawned transport; the Worker is created lazily
// by Spawn (on panel open) or by the first Call.
func newJSWorkerTransport() *jsWorkerTransport {
	return &jsWorkerTransport{pending: map[int]chan gitrpc.Reply{}, readyCh: make(chan struct{})}
}

// gitWorkerURL is the cache-busted URL of the worker bootstrap the host page
// publishes (globalThis.gotexGitWorkerURL, e.g. "git-worker.js?v=<sha>"). It falls
// back to the un-busted name for a plain static host / the headless test.
func gitWorkerURL() string {
	if u := js.Global().Get("gotexGitWorkerURL"); u.Type() == js.TypeString && u.String() != "" {
		return u.String()
	}
	return "git-worker.js"
}

// Spawn creates the Worker and starts loading git-worker.wasm. It is non-blocking
// and idempotent: the panel calls it on open so the download overlaps the user
// filling the form, and Call calls it too as a safety net.
func (w *jsWorkerTransport) Spawn() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.spawned {
		return
	}
	w.spawned = true

	w.worker = js.Global().Get("Worker").New(gitWorkerURL())

	w.onmessage = js.FuncOf(func(_ js.Value, args []js.Value) any {
		data := args[0].Get("data")
		// The bootstrap posts its download progress as an OBJECT, before any Go
		// code exists in the worker to speak the gitrpc protocol; RPC replies are
		// strings. Splitting on the type keeps the two channels apart without a
		// protocol change.
		if data.Type() == js.TypeObject {
			if data.Get("t").Type() == js.TypeString && data.Get("t").String() == gitAssetProgressMessage {
				if w.onProgress != nil {
					w.onProgress(assetFraction(data.Get("got"), data.Get("total")))
				}
			}
			return nil
		}
		if data.Type() != js.TypeString {
			return nil
		}
		reply, err := gitrpc.DecodeReply(data.String())
		if err != nil {
			return nil
		}
		if reply.Ready {
			w.readyOnce.Do(func() { close(w.readyCh) })
			if w.onReady != nil {
				w.onReady()
			}
			return nil
		}
		w.mu.Lock()
		ch := w.pending[reply.ID]
		delete(w.pending, reply.ID)
		w.mu.Unlock()
		if ch != nil {
			ch <- reply
		}
		return nil
	})
	w.worker.Set("onmessage", w.onmessage)

	// A worker-level error — its script 404s, its wasm fails to instantiate, a CSP
	// blocks it — must not hang git forever.
	//
	// Releasing the ready gate is not enough on its own, and that is what used to
	// happen: Call went on to post a message to a dead Worker and blocked on a
	// reply that could never arrive, so every git operation hung, silently and
	// permanently. Each pending call is failed here, and the transport is marked
	// dead so later ones fail at once.
	w.onerror = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		w.mu.Lock()
		w.dead = true
		pending := w.pending
		w.pending = map[int]chan gitrpc.Reply{}
		w.mu.Unlock()
		for id, ch := range pending {
			ch <- workerDeadReply(id)
		}
		w.readyOnce.Do(func() { close(w.readyCh) })
		return nil
	})
	w.worker.Set("onerror", w.onerror)
}

// Call posts req to the worker and blocks until the matching reply arrives. It is
// only invoked from op-goroutines, so the block yields to the page event loop.
func (w *jsWorkerTransport) Call(req gitrpc.Request) gitrpc.Reply {
	w.Spawn()
	<-w.readyCh // the worker signals ready once its wasm is instantiated

	ch := make(chan gitrpc.Reply, 1)
	w.mu.Lock()
	if w.dead {
		w.mu.Unlock()
		return workerDeadReply(req.ID)
	}
	w.nextID++
	req.ID = w.nextID
	w.pending[req.ID] = ch
	worker := w.worker
	w.mu.Unlock()

	worker.Call("postMessage", gitrpc.EncodeRequest(req))
	return <-ch
}

// workerDeadReply is what a call gets when the Worker itself never came up. It
// names the asset, because that is the actionable part: the git client is a
// separate binary, and "it did not load" is a different problem from "the clone
// was refused".
func workerDeadReply(id int) gitrpc.Reply {
	return gitrpc.Reply{
		ID:    id,
		OK:    false,
		Code:  "worker",
		Error: "the git client (git-worker.wasm) did not load",
	}
}

// gitAssetProgressMessage tags the bootstrap's download-progress messages. It is
// deliberately namespaced: the page and the worker share no other object
// messages, and a bare "progress" would collide with anything added later.
const gitAssetProgressMessage = "gotex-asset-progress"

// assetFraction turns the bootstrap's (got, total) pair into a fraction, or -1
// when the total is unusable — a host that sent no length, or a build with no
// published decompressed size. A bar that cannot measure says so by sliding
// rather than by claiming 0%.
func assetFraction(got, total js.Value) float64 {
	if got.Type() != js.TypeNumber || total.Type() != js.TypeNumber {
		return -1
	}
	t := total.Float()
	if t <= 0 {
		return -1
	}
	return got.Float() / t
}
