// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package main

// The saved-workspace store: where a cloned repository is written down so a
// reload does not have to fetch it again.
//
// # Why the Cache API and not IndexedDB
//
// A snapshot is one opaque blob per repository, written whole and read whole.
// That is exactly a Cache entry, and the Cache API expresses it in a handful of
// promise calls where IndexedDB would need a database version, an upgrade
// handler, a transaction and a request per access. Both live under the same
// origin quota and the same eviction rules, so nothing is given up. The store
// is keyed by a URL under this worker's own origin that is never actually
// fetched — put and match never touch the network.
//
// # What a failure means here
//
// Storage that is absent, refused or full is NOT an error: it is
// indistinguishable, to the reader, from never having visited before, and the
// caller simply clones. The one failure worth reporting is a saved entry that
// EXISTS and cannot be reopened, because that is the reader's own uncommitted
// work going away — they should be told rather than left to notice.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"syscall/js"
)

// cacheName is the single Cache the workspaces live in.
const cacheName = "gotex-workspace-v1"

// errSavedWorkspaceUnreadable is the one storage failure the reader hears about:
// something WAS saved for this repository and could not be reopened.
var errSavedWorkspaceUnreadable = errors.New("the saved workspace could not be reopened; starting from the remote instead")

// workspaceKey is the cache key for one repository at one branch. It is a URL
// under this worker's origin — the Cache API keys on requests — with the remote
// hashed rather than embedded, so a token that happens to sit in a remote URL
// never becomes a stored key.
func workspaceKey(url, branch string) string {
	sum := sha256.Sum256([]byte(url + "\n" + branch))
	origin := js.Global().Get("location").Get("origin").String()
	return origin + "/__gotex-workspace/" + hex.EncodeToString(sum[:])
}

// await blocks the calling goroutine on a JS promise. It is only ever called
// from the worker's single request goroutine, never from the event loop, so the
// block yields to JavaScript rather than deadlocking it — the same contract the
// git round-trips already rely on.
func await(promise js.Value) (js.Value, error) {
	type result struct {
		value js.Value
		err   error
	}
	ch := make(chan result, 1)

	onOK := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0]
		}
		ch <- result{value: v}
		return nil
	})
	defer onOK.Release()

	onErr := js.FuncOf(func(_ js.Value, args []js.Value) any {
		msg := "rejected"
		if len(args) > 0 {
			msg = js.Global().Get("String").Invoke(args[0]).String()
		}
		ch <- result{err: errors.New(msg)}
		return nil
	})
	defer onErr.Release()

	promise.Call("then", onOK, onErr)
	r := <-ch
	return r.value, r.err
}

// openCache opens the workspace cache, or reports that this browser has none to
// offer (a private window, storage disabled, a non-secure context).
func openCache() (js.Value, bool) {
	caches := js.Global().Get("caches")
	if !caches.Truthy() {
		return js.Undefined(), false
	}
	c, err := await(caches.Call("open", cacheName))
	if err != nil || !c.Truthy() {
		return js.Undefined(), false
	}
	return c, true
}

// loadSnapshot returns the snapshot saved for key. The second result says
// whether an entry existed at all, which is what separates "never been here"
// from "your saved work is unreadable".
func loadSnapshot(key string) ([]byte, bool) {
	cache, ok := openCache()
	if !ok {
		return nil, false
	}
	match, err := await(cache.Call("match", key))
	if err != nil || !match.Truthy() {
		return nil, false
	}
	buf, err := await(match.Call("arrayBuffer"))
	if err != nil || !buf.Truthy() {
		return nil, true // it is there; we just cannot read it
	}
	u8 := js.Global().Get("Uint8Array").New(buf)
	snap := make([]byte, u8.Get("length").Int())
	js.CopyBytesToGo(snap, u8)
	return snap, true
}

// saveSnapshot writes snap under key, best-effort: a browser that refuses the
// write must not turn the git operation that just succeeded into a failure.
func saveSnapshot(key string, snap []byte) {
	cache, ok := openCache()
	if !ok {
		return
	}
	u8 := js.Global().Get("Uint8Array").New(len(snap))
	js.CopyBytesToJS(u8, snap)
	resp := js.Global().Get("Response").New(u8)
	_, _ = await(cache.Call("put", key, resp))
}

// dropSnapshot removes an entry that could not be reopened, so the next visit
// starts clean instead of tripping over the same bytes again.
func dropSnapshot(key string) {
	if cache, ok := openCache(); ok {
		_, _ = await(cache.Call("delete", key))
	}
}
