// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command collab-icetest is the real-browser proof of the Collaborate panel's
// "ICE servers (STUN/TURN)" field. It builds one playground session with the
// real browser backend (so [playground.State.EnableCollab] installs the genuine
// localStorage persist hook), opens the Collaborate panel, and drives the field
// through the SAME input path a user does — a pointer press to focus it, real
// character and key events to edit and commit it — then asserts the visible
// field is wired to the live ICE configuration and to localStorage:
//
//   - out of the box the field is pre-filled with the public STUN default;
//   - typing a credentialed TURN relay and pressing Enter reconfigures the live
//     [playground.State.CollabICEConfig] AND persists the value to localStorage;
//   - clearing the field falls back to the public STUN default rather than
//     breaking collaboration.
//
// The panel is painted onto a page canvas so the headless driver
// (collab_ice_browser_test.go) can screenshot the field, and the verdict is left
// on globalThis.__result for the driver to read.
package main

import (
	"fmt"
	"strings"
	"syscall/js"

	playground "github.com/go-tex/go-tex.github.io/playground"
)

// iceStorageKey mirrors the localStorage key EnableCollab persists the ICE
// configuration under (playground/collab_js.go).
const iceStorageKey = "gotex-collab-ice"

// customRelay is the credentialed TURN relay typed into the field: a URL with a
// username and credential, the shape only a TURN entry (not a bare STUN default)
// carries.
const customRelay = "turn:relay.example:3478|alice|s3cret"

func main() {
	ok, detail := run()
	js.Global().Get("console").Call("log", "DONE "+detail)
	js.Global().Set("__result", map[string]any{"ok": ok, "detail": detail})
	// Stay running: the page keeps the rendered panel up for the screenshot.
	select {}
}

func logf(format string, args ...any) {
	js.Global().Get("console").Call("log", fmt.Sprintf(format, args...))
}

func run() (bool, string) {
	playground.SetupText(1)
	st := playground.NewState(760, 720, false)
	render := showCanvas("collab", "Collaborate panel — ICE servers (STUN/TURN) field", st)
	// The real backend + the real localStorage persist hook.
	st.EnableCollab(render)

	// 1. Out of the box the field is pre-filled with the public STUN default.
	def := st.CollabICEText()
	if !strings.HasPrefix(def, "stun:") {
		return false, "field not pre-filled with the STUN default: " + def
	}
	logf("field pre-filled with default: %q", def)

	// Open the panel and confirm the ICE field is visible (a real rect).
	st.SetCollabOpen(true)
	ice, ok := st.CollabButtonRects()["ice"]
	if !ok || ice[2] <= 0 || ice[3] <= 0 {
		return false, "ICE field not visible in the open panel"
	}
	render()
	logf("ICE field visible at [x=%d y=%d w=%d h=%d]", ice[0], ice[1], ice[2], ice[3])

	cx, cy := ice[0]+4, ice[1]+ice[3]/2

	// 2. Focus the field, clear the default and type a credentialed TURN relay
	// through the real event path, then commit with Enter.
	st.HandleClick(cx, cy)
	clearField(st)
	for _, r := range customRelay {
		st.HandleChar(string(r))
	}
	if got := st.CollabICEText(); got != customRelay {
		return false, "typed text = " + got
	}
	st.HandleKeyDown("Enter")
	render()

	// The live configuration reflects the typed TURN relay, credentials included.
	cfg := st.CollabICEConfig()
	if len(cfg) != 1 || cfg[0].URL != "turn:relay.example:3478" || cfg[0].Username != "alice" || cfg[0].Credential != "s3cret" {
		return false, fmt.Sprintf("CollabICEConfig after typing = %+v", cfg)
	}
	// The gotexCollabState read-back (CollabICEServers) reflects it too.
	if urls := st.CollabICEServers(); len(urls) != 1 || urls[0] != "turn:relay.example:3478" {
		return false, fmt.Sprintf("CollabICEServers read-back = %v", urls)
	}
	logf("typed TURN relay applied to the live config and read-back: %+v", cfg)

	// …and it persisted to localStorage under the shared key.
	stored := localStorageGet(iceStorageKey)
	if stored != customRelay {
		return false, "localStorage not updated (got " + stored + ")"
	}
	logf("persisted to localStorage: %q", stored)

	// 3. Clearing the field falls back to the public STUN default.
	st.HandleClick(cx, cy)
	clearField(st)
	st.HandleKeyDown("Enter")
	render()
	back := st.CollabICEServers()
	if len(back) == 0 {
		return false, "clearing did not fall back to a default"
	}
	for _, u := range back {
		if !strings.HasPrefix(u, "stun:") {
			return false, "fallback is not STUN: " + u
		}
	}
	logf("cleared field fell back to the STUN default: %v", back)

	return true, "ICE field wired: prefilled STUN default, typed TURN applied+persisted, cleared falls back to STUN"
}

// clearField backspaces the focused ICE field to empty.
func clearField(st *playground.State) {
	for st.CollabICEText() != "" {
		st.HandleKeyDown("Backspace")
	}
}

// localStorageGet reads a string from the browser's localStorage, or "".
func localStorageGet(key string) string {
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return ""
	}
	if v := ls.Call("getItem", key); v.Type() == js.TypeString {
		return v.String()
	}
	return ""
}

// showCanvas appends a labelled <canvas> for st and returns a repaint hook that
// blits State.Draw's RGBA buffer into it.
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
