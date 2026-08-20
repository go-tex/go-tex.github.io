// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command collab-browsertest is the real-browser proof of the playground's live
// collaborative editing. Everything the feature needs a browser for — the
// RTCPeerConnection, ICE, the data channel — is here the browser's own, driven
// by a headless Chrome from collab_browser_test.go.
//
// It runs the whole thing in one page and pastes for itself: it builds TWO
// playground editor sessions, has one HOST and one JOIN, hands the offer and
// answer across in process (the copy-paste a person does), types LaTeX into the
// host and asserts that (a) the guest's editor buffer converges on the same text
// and (b) the guest shows a remote Decoration for the host — the host's name,
// colour and caret, painted over a genuine WebRTC connection with no server.
//
// The verdict is left on globalThis.__result for the driver to read, and each
// step is logged so a run can be followed.
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
	// The verdict is recorded and the driver reads it off the page. The program
	// stays running rather than returning: a real RTCPeerConnection goes on firing
	// events, and firing one into a Go instance that has exited throws.
	select {}
}

func logf(format string, args ...any) {
	js.Global().Get("console").Call("log", fmt.Sprintf(format, args...))
}

// marker is the distinctive text typed into the host, asserted to reach the
// guest character for character.
const marker = "CONVERGE"

func run() (bool, string) {
	playground.SetupText(1)

	host := playground.NewState(1000, 720, false)
	host.CompilePending()
	host.SetCollabName("Alice")
	renderHost := showCanvas("host", "Host (Alice)", host)
	host.EnableCollab(renderHost)

	guest := playground.NewState(1000, 720, false)
	guest.CompilePending()
	guest.SetCollabName("Bob")
	renderGuest := showCanvas("guest", "Guest (Bob) — watch for Alice's caret", guest)
	guest.EnableCollab(renderGuest)

	// This in-page proof runs both peers in one process, and its driver exposes
	// raw host candidates (--disable-features=WebRtcHideLocalIpsWithMdns), so the
	// peers meet on host candidates without a STUN server. Pin the config to
	// host-candidate-only: the public-STUN default (exercised by the two-tab proof)
	// would only add non-trickle ICE-gathering latency to a STUN server here, for
	// no gain, and slow this fast regression down.
	host.SetCollabICEServers("")
	guest.SetCollabICEServers("")

	// The copy-paste handshake, in process: host offers → guest answers → host
	// accepts. Each step's blob is handed straight to the next, the way a person
	// would carry it.
	host.CollabHost(func(offer string, err error) {
		if err != nil {
			logf("host offer failed: %v", err)
			return
		}
		logf("host produced an offer (%d bytes); handing it to the guest", len(offer))
		guest.CollabJoin(offer, func(answer string, err error) {
			if err != nil {
				logf("guest answer failed: %v", err)
				return
			}
			logf("guest produced an answer (%d bytes); handing it back to the host", len(answer))
			host.CollabAcceptAnswer(answer, func(err error) {
				if err != nil {
					logf("host accept failed: %v", err)
					return
				}
				logf("host accepted the answer; the channel should open now")
			})
		})
	})

	if !waitUntil(20*time.Second, func() bool { return host.CollabConnected() && guest.CollabConnected() }) {
		return false, fmt.Sprintf("peers did not both connect (host=%v guest=%v)", host.CollabConnected(), guest.CollabConnected())
	}
	logf("both peers connected over WebRTC")

	// Type the marker into the host, one character at a time as a user would.
	for _, r := range marker {
		if !host.HandleChar(string(r)) {
			return false, "host editor did not accept a character"
		}
	}
	logf("typed %q into the host editor", marker)

	// (a) The guest's buffer must converge on the host's text.
	if !waitUntil(15*time.Second, func() bool { return strings.Contains(guest.Source(), marker) }) {
		return false, fmt.Sprintf("guest buffer did not converge; guest head=%q", head(guest.Source(), 40))
	}
	logf("guest buffer converged: it now contains %q", marker)

	// (b) The guest must paint a remote Decoration for the host: the host's name,
	// its colour, and a caret at the marker's end.
	var deco playground.CollabDecoration
	if !waitUntil(15*time.Second, func() bool {
		for _, d := range guest.CollabRemoteDecorations() {
			if d.Label == "Alice" {
				deco = d
				return d.Line == 0 && d.Col == len(marker)
			}
		}
		return false
	}) {
		return false, fmt.Sprintf("guest did not show the host's remote caret; decorations=%v", guest.CollabRemoteDecorations())
	}
	logf("guest shows the host's remote caret: label=%q colour=%s line=%d col=%d",
		deco.Label, deco.ColorHex, deco.Line, deco.Col)

	if deco.ColorHex != host.CollabColorHex() {
		return false, fmt.Sprintf("remote caret colour %s != host colour %s", deco.ColorHex, host.CollabColorHex())
	}

	// Symmetry check: the host's own buffer holds the marker too, and both agree.
	if !strings.Contains(host.Source(), marker) || host.Source() != guest.Source() {
		return false, "host and guest buffers disagree after convergence"
	}
	logf("convergence complete: host and guest hold identical buffers")

	// Open both panels and repaint so the screenshot shows the connected state
	// (peer list + swatch) alongside the remote caret painted in the editor.
	host.SetCollabOpen(true)
	guest.SetCollabOpen(true)
	renderHost()
	renderGuest()

	return true, fmt.Sprintf("guest converged on %q and shows Alice's caret (%s) at 0:%d over WebRTC", marker, deco.ColorHex, deco.Col)
}

// showCanvas appends a labelled <canvas> to the page for st and returns a repaint
// function that blits st.Draw onto it — so a screenshot of the page is real
// visual proof (the converged text and the remote caret painted in the editor).
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

// head returns the first n bytes of s for a log line.
func head(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
