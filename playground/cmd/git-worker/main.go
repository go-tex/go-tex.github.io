// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package main

import (
	"syscall/js"

	"github.com/go-tex/go-tex.github.io/playground/internal/gitrpc"
)

// The Web Worker entry point. It instantiates the browsergit-backed session, wires
// the worker's postMessage channel to the tagless [handler], and posts a one-shot
// ready message so the main app knows the wasm has loaded before it sends any RPC.
//
// # Threading
//
// A dedicated worker runs Go on its own thread, separate from the page's. Requests
// are drained by ONE goroutine, off the worker's event-loop goroutine, so each
// blocking Fetch (a clone/pull/push round-trip) yields to the worker event loop
// instead of freezing it — and, being single, the goroutine serialises access to
// the browsergit worktree, which is not safe for concurrent use.
func main() {
	h := newHandler(&browsergitSession{})
	// Progress notifications are posted OUT OF BAND, from the drain goroutine while
	// a clone/pull's Fetch is in flight (it yields to the worker event loop at each
	// blocking point, and the sideband writer runs on that same goroutine). The main
	// app tells a progress notification from the terminal reply by its Progress field.
	h.emit = func(r gitrpc.Reply) { js.Global().Call("postMessage", gitrpc.EncodeReply(r)) }
	requests := make(chan string, 64)

	go func() {
		for reqJSON := range requests {
			js.Global().Call("postMessage", h.handle(reqJSON))
		}
	}()

	onmessage := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		if data := args[0].Get("data"); data.Type() == js.TypeString {
			requests <- data.String()
		}
		return nil
	})
	js.Global().Set("onmessage", onmessage)

	// Tell the main app the worker is ready to serve requests.
	js.Global().Call("postMessage", gitrpc.EncodeReply(gitrpc.ReadyReply()))

	select {} // keep the runtime alive so onmessage keeps firing
}
