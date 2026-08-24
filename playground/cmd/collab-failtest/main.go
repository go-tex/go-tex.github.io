// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command collab-failtest is the real-browser proof that a WebRTC connection
// which cannot find a path to the peer surfaces a CLEAR failure in the
// Collaborate panel — the TURN-server guidance — instead of hanging silently on
// a "waiting…" line. It is the headless counterpart of the state-machine test in
// collab_test.go, driven by a headless Chrome from collab_fail_browser_test.go.
//
// It builds a guest that answers a host's GENUINE offer but is never accepted,
// so no candidate pair ever connects and the data-channel open-wait times out
// (webrtcBackend's connectTimeout, shortened here so the proof runs in seconds).
// The panel then moves to its failed phase and paints the failure message, which
// this program asserts and the driver screenshots.
//
// The verdict is left on globalThis.__result for the driver to read; each step
// is logged so a run can be followed.
package main

import (
	"fmt"
	"strings"
	"syscall/js"
	"time"

	playground "github.com/go-tex/go-tex.github.io/playground"
)

func main() {
	ok, detail := run()
	js.Global().Get("console").Call("log", "DONE "+detail)
	js.Global().Set("__result", map[string]any{"ok": ok, "detail": detail})
	// Stay running: a real RTCPeerConnection goes on firing events, and firing one
	// into a Go instance that has exited throws.
	select {}
}

func logf(format string, args ...any) {
	js.Global().Get("console").Call("log", fmt.Sprintf(format, args...))
}

func run() (bool, string) {
	playground.SetupText(1)

	// Shorten the open-wait so the unreachable-peer failure surfaces in a few
	// seconds rather than the production default.
	playground.SetCollabConnectTimeout(3 * time.Second)

	// A throwaway host, only to mint a genuine offer for the guest to answer. Its
	// own editor is irrelevant to the proof.
	host := playground.NewState(1000, 720, false)
	host.CompilePending()
	host.SetCollabName("Alice")
	host.EnableCollab(func() {})
	host.SetCollabICEServers("") // host-candidate only: fast ICE gathering

	// The guest is the one whose panel we prove. It answers the host's offer, but
	// the host will NEVER accept that answer, so no data channel ever opens.
	guest := playground.NewState(1000, 720, false)
	guest.CompilePending()
	guest.SetCollabName("Bob")
	render := showCanvas("guest", "Guest (Bob) — connection cannot be established", guest)
	guest.EnableCollab(render)
	guest.SetCollabICEServers("")
	guest.SetCollabOpen(true)
	render()

	host.CollabHost(func(offer string, err error) {
		if err != nil {
			logf("host offer failed: %v", err)
			return
		}
		logf("host produced a %d-byte offer; the guest will answer it — the host will NOT accept", len(offer))
		guest.CollabJoin(offer, func(_ string, err error) {
			if err != nil {
				logf("guest answer failed: %v", err)
				return
			}
			logf("guest answered; now waiting on a channel that will never open")
		})
	})

	logf("waiting for the guest's open-wait to time out and the panel to fail…")
	const phaseFailed = 5 // playground.CollabPhase(): 0 idle … 4 connected, 5 failed
	if !waitUntil(30*time.Second, func() bool { return guest.CollabPhase() == phaseFailed }) {
		return false, fmt.Sprintf("guest never reached the failed phase (phase=%d)", guest.CollabPhase())
	}
	render() // paint the failed panel so the screenshot shows the message

	msg := guest.CollabFailureMessage()
	logf("guest panel failed with message: %q", msg)
	if !strings.Contains(msg, "TURN server") {
		return false, fmt.Sprintf("failed phase but the message is not the TURN guidance: %q", msg)
	}
	logf("PASS the panel surfaced the connection-failure guidance instead of hanging")
	return true, "guest surfaced the TURN guidance on an unreachable peer"
}

// showCanvas appends a labelled <canvas> to the page for st and returns a repaint
// function that blits st.Draw onto it — so a screenshot of the page is real
// visual proof (the failed Collaborate panel with its guidance).
func showCanvas(id, caption string, st *playground.State) func() {
	doc := js.Global().Get("document")
	label := doc.Call("createElement", "div")
	label.Set("textContent", caption)
	label.Get("style").Set("font", "14px sans-serif")
	label.Get("style").Set("margin", "8px 0 2px")
	doc.Get("body").Call("appendChild", label)

	canvas := doc.Call("createElement", "canvas")
	w, h := st.Size()
	canvas.Set("width", w)
	canvas.Set("height", h)
	canvas.Set("id", id)
	canvas.Get("style").Set("border", "1px solid #999")
	canvas.Get("style").Set("width", "500px")
	doc.Get("body").Call("appendChild", canvas)

	ctx := canvas.Call("getContext", "2d")
	buf := make([]byte, 4*w*h)
	img := ctx.Call("createImageData", w, h)
	data := img.Get("data")
	return func() {
		st.Draw(buf)
		js.CopyBytesToJS(data, buf)
		ctx.Call("putImageData", img, 0, 0)
	}
}

// waitUntil polls cond until it holds or the timeout elapses, yielding to the
// page's event loop between polls (a time.After receive is what lets the WebRTC
// callbacks and the CRDT goroutines run).
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		<-time.After(50 * time.Millisecond)
	}
}
