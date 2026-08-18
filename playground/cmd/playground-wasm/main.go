// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command playground-wasm is the browser front-end of the go-tex playground: it
// drives a single go-widgets/toolkit canvas application (a LaTeX CodeEditor and
// a live gotex render pane, see package playground) against an HTML <canvas>.
//
// It forwards mouse / wheel / keyboard input into the playground State, blits
// State.Draw's RGBA buffer with putImageData, and debounces re-compiles on edit.
// It publishes gotexSetTheme(dark) so the host page's theme toggle recolours the
// canvas, and gotexPlaygroundReady so the page can reveal the canvas.
//
// The whole UI logic is in the tagless playground package (native-testable);
// this file is the thin, coverage-excluded js/wasm shell.
package main

import (
	"syscall/js"

	playground "github.com/go-tex/go-tex.github.io/playground"
)

// canvasID is the id of the <canvas> the host page provides.
const canvasID = "gotex-canvas"

func main() {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", canvasID)
	if canvas.IsUndefined() || canvas.IsNull() {
		println("playground-wasm: no #" + canvasID + " canvas in the host page")
		return
	}

	// Size the surface to the canvas's CSS box (1 device pixel per CSS pixel).
	sizeOf := func() (int, int) {
		w := canvas.Get("clientWidth").Int()
		h := canvas.Get("clientHeight").Int()
		if w < 320 {
			w = 320
		}
		if h < 240 {
			h = 240
		}
		return w, h
	}
	w, h := sizeOf()
	canvas.Set("width", w)
	canvas.Set("height", h)
	ctx := canvas.Call("getContext", "2d")

	dark := detectDark(doc)
	state := playground.NewState(w, h, dark)

	// Persist the editor buffer to localStorage (key "gotex-pg-src", shared with
	// the legacy textarea playground) so an edited document survives a reload.
	// Restore any saved buffer BEFORE the first render; the sample document is
	// only the first-visit seed. Saving is wired to OnEdit — independent of the
	// compile path — so a document persists even when its compile errors.
	const storeKey = "gotex-pg-src"
	ls := js.Global().Get("localStorage")
	if !ls.IsUndefined() && !ls.IsNull() {
		if saved := ls.Call("getItem", storeKey); saved.Type() == js.TypeString && saved.String() != "" {
			state.SetSource(saved.String())
		}
		state.OnEdit = func(text string) { ls.Call("setItem", storeKey, text) }
	}

	// Reusable frame buffers, re-allocated on resize.
	var local []byte
	var imageData, dst js.Value
	alloc := func(w, h int) {
		local = make([]byte, 4*w*h)
		imageData = ctx.Call("createImageData", w, h)
		dst = imageData.Get("data")
	}
	alloc(w, h)

	render := func() {
		state.Draw(local)
		js.CopyBytesToJS(dst, local)
		ctx.Call("putImageData", imageData, 0, 0)
	}

	// Debounced compile: an edit latches pendingCompile; the timer fires once
	// after the burst settles, compiles, and repaints.
	var timer js.Value
	schedule := func() {
		if !timer.IsUndefined() {
			js.Global().Call("clearTimeout", timer)
		}
		timer = js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
			if state.TakePendingCompile() {
				state.Compile()
				render()
			}
			return nil
		}), 300)
	}
	state.OnCompileNeeded = schedule

	coords := func(e js.Value) (int, int) {
		rect := canvas.Call("getBoundingClientRect")
		sx := rect.Get("width").Float() / float64(mustInt(canvas.Get("width")))
		sy := rect.Get("height").Float() / float64(mustInt(canvas.Get("height")))
		if sx == 0 {
			sx = 1
		}
		if sy == 0 {
			sy = 1
		}
		x := int((e.Get("clientX").Float() - rect.Get("left").Float()) / sx)
		y := int((e.Get("clientY").Float() - rect.Get("top").Float()) / sy)
		return x, y
	}

	canvas.Call("addEventListener", "mousedown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 || args[0].Get("button").Int() != 0 {
			return nil
		}
		x, y := coords(args[0])
		if state.HandleClick(x, y) {
			render()
		}
		return nil
	}))
	canvas.Call("addEventListener", "wheel", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		e := args[0]
		e.Call("preventDefault")
		x, y := coords(e)
		d := 3
		if e.Get("deltaY").Float() < 0 {
			d = -3
		}
		if state.HandleScroll(x, y, d) {
			render()
		}
		return nil
	}), map[string]any{"passive": false})

	js.Global().Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		e := args[0]
		key := e.Get("key").String()
		var changed bool
		if len([]rune(key)) == 1 && !e.Get("ctrlKey").Bool() && !e.Get("metaKey").Bool() && !e.Get("altKey").Bool() {
			changed = state.HandleChar(key)
		} else {
			changed = state.HandleKeyDown(key)
		}
		if changed {
			e.Call("preventDefault")
			render()
		}
		return nil
	}))

	// Resize: re-fit the surface to the canvas box.
	js.Global().Call("addEventListener", "resize", js.FuncOf(func(js.Value, []js.Value) any {
		nw, nh := sizeOf()
		ow, oh := state.Size()
		if nw == ow && nh == oh {
			return nil
		}
		canvas.Set("width", nw)
		canvas.Set("height", nh)
		alloc(nw, nh)
		state.Resize(nw, nh)
		render()
		return nil
	}))

	// Theme toggle hook for the host page.
	js.Global().Set("gotexSetTheme", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		state.SetTheme(args[0].Bool())
		render()
		return nil
	}))

	render()
	js.Global().Set("gotexPlaygroundReady", true)
	select {} // keep the Go runtime alive so the callbacks live
}

// detectDark reads the host page's theme preference: an explicit
// data-theme="dark" wins, otherwise the OS prefers-color-scheme.
func detectDark(doc js.Value) bool {
	root := doc.Get("documentElement")
	if t := root.Get("dataset").Get("theme"); t.Type() == js.TypeString {
		switch t.String() {
		case "dark":
			return true
		case "light":
			return false
		}
	}
	m := js.Global().Call("matchMedia", "(prefers-color-scheme: dark)")
	return !m.IsUndefined() && m.Get("matches").Bool()
}

func mustInt(v js.Value) int {
	if v.Type() == js.TypeNumber {
		return v.Int()
	}
	return 1
}
