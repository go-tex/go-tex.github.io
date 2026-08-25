// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command playground-wasm is the browser front-end of the go-tex playground: it
// drives a single go-widgets/toolkit canvas application (a LaTeX CodeEditor, a
// live gotex render pane, a minimap and a diagnostics Log — see package
// playground) against an HTML <canvas>.
//
// It forwards mouse (down/move/up), wheel and keyboard input into the playground
// State, blits State.Draw's RGBA buffer with putImageData, and debounces
// re-compiles on edit. The canvas backing store is sized to CSS pixels ×
// devicePixelRatio and the toolkit is rendered at that device scale (via
// playground.SetupText), so text is crisp on a HiDPI/Retina panel instead of
// upscaled-and-blurred; it re-fits on window resize AND devicePixelRatio change.
//
// It publishes gotexSetTheme(dark) so the host page's theme toggle recolours the
// canvas, and gotexPlaygroundReady so the page can reveal the canvas.
//
// The whole UI logic is in the tagless playground package (native-testable);
// this file is the thin, coverage-excluded js/wasm shell.
package main

import (
	"strings"
	"syscall/js"

	playground "github.com/go-tex/go-tex.github.io/playground"
)

// canvasID is the id of the <canvas> the host page provides.
const canvasID = "gotex-canvas"

// buildVersion and buildTime identify exactly which binary is running. The
// deploy workflow overrides them at link time with `-ldflags -X` (the git short
// SHA and a UTC build timestamp); a local `go build` leaves the honest defaults.
// They are handed to the playground State (SetBuildInfo) and shown in the status
// bar so a viewer can tell whether the deployed app is up to date — a fresh SHA
// on screen proves the new wasm loaded past any GitHub Pages / CDN cache.
var (
	buildVersion = "dev"
	buildTime    = "unknown"
)

func main() {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", canvasID)
	if canvas.IsUndefined() || canvas.IsNull() {
		println("playground-wasm: no #" + canvasID + " canvas in the host page")
		return
	}
	ctx := canvas.Call("getContext", "2d")

	// devicePixelRatio: how many device pixels back one CSS pixel (2 on Retina).
	dpr := func() float64 {
		r := js.Global().Get("devicePixelRatio")
		if r.Type() == js.TypeNumber && r.Float() > 0 {
			return r.Float()
		}
		return 1
	}
	// cssSize is the layout box in CSS pixels; deviceSize multiplies by dpr.
	cssSize := func() (int, int) {
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
	deviceSize := func() (int, int, float64) {
		cw, ch := cssSize()
		d := dpr()
		return int(float64(cw)*d + 0.5), int(float64(ch)*d + 0.5), d
	}

	dw, dh, d := deviceSize()
	playground.SetupText(d) // crisp text at the device scale, BEFORE first layout
	canvas.Set("width", dw)
	canvas.Set("height", dh)

	dark := detectDark(doc)
	state := playground.NewState(dw, dh, dark)
	// Stamp the running binary's identity into the status bar (git short SHA + UTC
	// build time, injected by the deploy workflow's -ldflags). Set once, at init.
	state.SetBuildInfo(buildVersion, buildTime)
	curDPR := d

	// Stamp each compile's Log entries with the viewer's local wall-clock time,
	// formatted by the browser (the toolkit LogView never reads a clock itself).
	state.SetTimeProvider(func() string {
		return js.Global().Get("Date").New().Call("toLocaleTimeString").String()
	})

	// Mirror every editor copy/cut to the real OS clipboard.
	state.SetClipboardWriter(func(s string) {
		if clip := clipboardAPI(); !clip.IsUndefined() && !clip.IsNull() {
			clip.Call("writeText", s)
		}
	})

	// Persist the editor buffer to localStorage (key "gotex-pg-src", shared with
	// the legacy textarea playground) so an edited document survives a reload.
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
	alloc(dw, dh)

	render := func() {
		state.Draw(local)
		js.CopyBytesToJS(dst, local)
		ctx.Call("putImageData", imageData, 0, 0)
	}

	// Live collaborative editing (WebRTC, server-less copy-paste signalling): the
	// canvas Collaborate panel drives it; this installs the real backend + the
	// OS-clipboard hooks it needs. See package playground (collab.go/collab_js.go).
	state.EnableCollab(render)

	// Remote git (browsergit over the Fetch RoundTripper): the canvas Git panel
	// drives Clone/Pull/Commit/Push against a CORS-enabled origin; this installs
	// the real backend. See package playground (git.go/git_js.go).
	state.EnableGit(render)

	// Debounced compile.
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

	// Fire the boot compile now that the time provider (and the rest) is wired,
	// so its Log entries carry the same clock format as every later compile.
	state.CompilePending()

	// coords maps a mouse event to DEVICE-pixel canvas coordinates: dividing the
	// CSS offset by (cssWidth/backingWidth) scales it up by dpr, which is exactly
	// the space State is laid out in.
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
	// mousemove/mouseup on the WINDOW so a drag that leaves the canvas still
	// tracks — the routing that makes the divider + scrollbar thumbs draggable.
	js.Global().Call("addEventListener", "mousemove", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		x, y := coords(args[0])
		if state.HandleMove(x, y) {
			render()
		}
		return nil
	}))
	js.Global().Call("addEventListener", "mouseup", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		x, y := coords(args[0])
		if state.HandleRelease(x, y) {
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
		// Forward BOTH axes so a horizontal two-finger swipe (deltaX) moves the
		// render pane's horizontal scrollbar, a vertical wheel (deltaY) the
		// vertical one. Each axis maps to +/-3 rows by sign, or 0 when flat.
		dx := signRows(e.Get("deltaX").Float())
		dy := signRows(e.Get("deltaY").Float())
		if state.HandleScroll(x, y, dx, dy) {
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
		mod := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()

		// Clipboard + select-all shortcuts, bridged to the browser clipboard.
		if mod && !e.Get("altKey").Bool() {
			switch strings.ToLower(key) {
			case "c":
				e.Call("preventDefault")
				if state.HandleCopy() {
					render()
				}
				return nil
			case "x":
				e.Call("preventDefault")
				if state.HandleCut() {
					render()
				}
				return nil
			case "a":
				e.Call("preventDefault")
				if state.HandleSelectAll() {
					render()
				}
				return nil
			case "v":
				e.Call("preventDefault")
				pasteFromClipboard(state, render)
				return nil
			}
		}

		// A dead key or an in-flight IME composition is committed by a following
		// composition/EventChar, so ignore the raw keydown. A bare modifier
		// (Shift/Control/Alt/Meta, which auto-repeats while held) carries no
		// character and must NOT reach the editor — forwarding it would reset an
		// in-progress Shift+Arrow selection between arrow presses.
		if key == "Dead" || e.Get("isComposing").Bool() || isModifierKey(key) {
			return nil
		}

		var changed bool
		if len([]rune(key)) == 1 && !mod {
			// Insert a single character whenever there is no Ctrl/Cmd. Do NOT
			// exclude Alt/Option: on a Mac a backslash (and @, #, {, }, …) is
			// typed with Option, so those keydowns carry altKey=true and would
			// otherwise be dropped.
			changed = state.HandleChar(key)
		} else {
			// Shift-prefix a NAVIGATION key so the editor extends its selection;
			// other keys pass through untouched.
			code := key
			if e.Get("shiftKey").Bool() && isNavKey(key) {
				code = "Shift+" + key
			}
			changed = state.HandleKeyDown(code)
		}
		if changed {
			e.Call("preventDefault")
			render()
		}
		return nil
	}))

	// refit re-sizes the backing store to the CSS box × current dpr, re-applying
	// the text scale when the dpr changed (moving the window to another display).
	refit := func() {
		nw, nh, nd := deviceSize()
		ow, oh := state.Size()
		if nw == ow && nh == oh && nd == curDPR {
			return
		}
		if nd != curDPR {
			playground.SetupText(nd)
			curDPR = nd
		}
		canvas.Set("width", nw)
		canvas.Set("height", nh)
		alloc(nw, nh)
		state.Resize(nw, nh)
		render()
	}
	js.Global().Call("addEventListener", "resize", js.FuncOf(func(js.Value, []js.Value) any { refit(); return nil }))
	if mq := js.Global().Call("matchMedia", "(resolution: 1dppx)"); mq.Truthy() {
		mq.Call("addEventListener", "change", js.FuncOf(func(js.Value, []js.Value) any { refit(); return nil }))
	}

	js.Global().Set("gotexSetTheme", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		state.SetTheme(args[0].Bool())
		render()
		return nil
	}))

	// Read-only introspection for headless verification (asserting a real state
	// change after a pointer / wheel / key interaction). The render pane's paging,
	// zoom and mode are the PagedView's MVVM observables, surfaced here.
	js.Global().Set("gotexDebug", js.FuncOf(func(js.Value, []js.Value) any {
		return map[string]any{
			"editorW":        state.EditorWidth(),
			"showLog":        state.ShowLog(),
			"dividerX":       state.DividerX(),
			"activeTab":      state.ActiveTab(),
			"zoomPercent":    state.ZoomPercent(),
			"selectedScheme": state.SelectedScheme(),
			"logEntryCount":  state.LogEntryCount(),
			"pageCount":      state.PageCount(),
			"drawnPages":     state.DrawnPages(),
			"renderMode":     state.RenderMode(),
			"currentPage":    state.RenderCurrentPage(),
			"visiblePages":   state.RenderVisiblePages(),
			"renderFocused":  state.RenderFocused(),
			"cursorLine":     state.CursorLine(),
			"cursorCol":      state.CursorCol(),
			"hasSelection":   state.HasSelection(),
			"selectionText":  state.SelectionText(),
		}
	}))

	// gotexRects exposes the device-pixel rectangles of the interactive targets
	// so a headless harness can click them precisely.
	js.Global().Set("gotexRects", js.FuncOf(func(js.Value, []js.Value) any {
		out := map[string]any{}
		for name, r := range state.DebugRects() {
			out[name] = []any{r[0], r[1], r[2], r[3]}
		}
		return out
	}))

	// gotexSource returns the current editor buffer, so a headless harness can
	// assert clipboard paste/cut changed the text.
	js.Global().Set("gotexSource", js.FuncOf(func(js.Value, []js.Value) any {
		return state.Source()
	}))

	// gotexSetSource replaces the editor buffer + recompiles, so a headless harness
	// can drive a multi-page document through the render pane.
	js.Global().Set("gotexSetSource", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && a[0].Type() == js.TypeString {
			state.SetSource(a[0].String())
			render()
		}
		return nil
	}))

	// gotexCaretPixel(line, col) -> [deviceX, deviceY] of that caret cell, so a
	// harness can click there and assert the caret round-trips (click accuracy).
	js.Global().Set("gotexCaretPixel", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) < 2 {
			return nil
		}
		x, y := state.CaretPixel(a[0].Int(), a[1].Int())
		return []any{x, y}
	}))

	// Source↔render linking introspection. gotexRenderLineAt(x,y) -> the 1-based
	// source line drawn at a device-pixel render-pane point (0 = none), so a
	// harness can find a glyph to click and assert the caret jumped there.
	js.Global().Set("gotexRenderLineAt", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) < 2 {
			return -1
		}
		return state.RenderLineAt(a[0].Int(), a[1].Int())
	}))
	// gotexLineToPage(line) -> the 1-based render page a source line's output lands
	// on (0 = none), so a harness can pick a caret target on a LATER page.
	js.Global().Set("gotexLineToPage", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) < 1 {
			return 0
		}
		return state.LineToRenderPage(a[0].Int())
	}))
	// gotexRenderScrollY() -> the render pane's vertical scroll offset, so a caret
	// move that scrolls the render is observable even in continuous mode.
	js.Global().Set("gotexRenderScrollY", js.FuncOf(func(js.Value, []js.Value) any {
		return state.RenderScrollY()
	}))
	// gotexSetPaginated(on) flips the render viewer's mode, so a caret-driven scroll
	// shows up as a crisp current-page change.
	js.Global().Set("gotexSetPaginated", js.FuncOf(func(_ js.Value, a []js.Value) any {
		state.SetRenderPaginated(len(a) > 0 && a[0].Bool())
		render()
		return nil
	}))

	// --- WYSIWYG mode (playground/wysiwyg.go) --------------------------------
	// A discrete, additive block of host bridges so the source-vs-visual toggle
	// and the headless introspection reach the LaTeX-only mode without touching
	// the handlers above.

	// gotexWysiwygToggle flips between the source editor and the visual RichEditor.
	js.Global().Set("gotexWysiwygToggle", js.FuncOf(func(js.Value, []js.Value) any {
		state.ToggleWysiwyg()
		render()
		return nil
	}))
	// gotexSetEditorTab(idx) selects the editor pane's tab (0 = Source, 1 = WYSIWYG)
	// directly — the reactive-tab equivalent of gotexWysiwygToggle, so a headless
	// harness can drive either tab explicitly.
	js.Global().Set("gotexSetEditorTab", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			state.SetEditorTab(a[0].Int())
			render()
		}
		return nil
	}))
	// gotexRichSelectBlock(idx) selects a whole block; gotexRichToggleStrong bolds
	// the current selection — the visual-edit verbs a headless run drives.
	js.Global().Set("gotexRichSelectBlock", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			state.RichSelectBlock(a[0].Int())
			render()
		}
		return nil
	}))
	js.Global().Set("gotexRichToggleStrong", js.FuncOf(func(js.Value, []js.Value) any {
		state.RichToggleStrong()
		render()
		return nil
	}))
	// gotexWysiwygDebug exposes the mode's state + parsed structure for headless
	// verification (the active editor-tab index, active flag, parse error, block
	// count, first-heading text + its \label anchor id, whether a bold run exists,
	// the v0.2.0 reference-node tallies footnotes/crossRefs/anchors, the document's
	// plain text, and the formatting-toolbar state: visible flag, button count,
	// strip rect, the caret block kind and each button's pressed/lit state).
	js.Global().Set("gotexWysiwygDebug", js.FuncOf(func(js.Value, []js.Value) any {
		n := state.RichToolbarButtonCount()
		pressed := make([]any, n)
		for i := 0; i < n; i++ {
			pressed[i] = state.RichToolbarButtonPressed(i)
		}
		tr := state.RichToolbarRect()
		return map[string]any{
			"activeTab":          state.ActiveEditorTab(),
			"active":             state.WysiwygActive(),
			"parseError":         state.WysiwygParseError(),
			"blockCount":         state.RichBlockCount(),
			"firstHeading":       state.RichFirstHeading(),
			"firstHeadingID":     state.RichFirstHeadingID(),
			"footnotes":          state.WysiwygFootnoteCount(),
			"crossRefs":          state.WysiwygCrossRefCount(),
			"anchors":            state.WysiwygAnchorCount(),
			"hasBold":            state.RichHasBold(),
			"plainText":          state.RichPlainText(),
			"toolbarVisible":     state.RichToolbarVisible(),
			"toolbarButtonCount": n,
			"toolbarRect":        []any{tr[0], tr[1], tr[2], tr[3]},
			"currentBlockKind":   state.RichCurrentBlockKind(),
			"buttonsPressed":     pressed,
		}
	}))

	// gotexRichToolbarRects exposes the device-pixel rectangle of every formatting
	// button, in grouped order (0..3 inline: Bold/Italic/Strike/Code; 4..9 block:
	// Paragraph/H1/H2/H3/Quote/CodeBlock; 10..11 lists: Bullet/Numbered), so a
	// headless harness can dispatch a real pointer click at a button's centre and
	// prove the click routes through the app to the editor verb.
	js.Global().Set("gotexRichToolbarRects", js.FuncOf(func(js.Value, []js.Value) any {
		rects := state.RichToolbarButtonRects()
		out := make([]any, len(rects))
		for i, r := range rects {
			out[i] = []any{r[0], r[1], r[2], r[3]}
		}
		return out
	}))

	// --- Collaborate (WebRTC) headless introspection -------------------------
	// The real two-tab proof (collab_twotab_test.go) drives the ACTUAL panel
	// buttons across two independent pages: gotexCollabRects locates each control
	// so the harness can click its real rect, and gotexCollabState reads back the
	// handshake phase, the signalling blobs and the remote carets to assert on.

	// gotexCollabRects() -> device-pixel [x,y,w,h] of the launcher and every
	// visible panel control, keyed by name.
	js.Global().Set("gotexCollabRects", js.FuncOf(func(js.Value, []js.Value) any {
		out := map[string]any{}
		for name, r := range state.CollabButtonRects() {
			out[name] = []any{r[0], r[1], r[2], r[3]}
		}
		return out
	}))

	// gotexSetICEServers(csv) reconfigures the WebRTC STUN/TURN servers and
	// persists the choice, so a deployment (or a headless run) can point the peers
	// at its own relay; see playground.SetCollabICEServers for the format.
	js.Global().Set("gotexSetICEServers", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && a[0].Type() == js.TypeString {
			csv := a[0].String()
			state.SetCollabICEServers(csv)
			if ls := js.Global().Get("localStorage"); ls.Truthy() {
				ls.Call("setItem", "gotex-collab-ice", csv)
			}
			render()
		}
		return nil
	}))

	// gotexCollabState() -> the live session snapshot for headless assertions.
	js.Global().Set("gotexCollabState", js.FuncOf(func(js.Value, []js.Value) any {
		decos := state.CollabRemoteDecorations()
		ds := make([]any, len(decos))
		for i, d := range decos {
			ds[i] = map[string]any{"label": d.Label, "color": d.ColorHex, "line": d.Line, "col": d.Col}
		}
		iceURLs := state.CollabICEServers()
		ice := make([]any, len(iceURLs))
		for i, u := range iceURLs {
			ice[i] = u
		}
		return map[string]any{
			"phase":       state.CollabPhase(),
			"connected":   state.CollabConnected(),
			"connecting":  state.CollabConnecting(),
			"peers":       state.CollabPeerCount(),
			"open":        state.CollabActive(),
			"offer":       state.CollabOffer(),
			"answer":      state.CollabAnswer(),
			"pasteText":   state.CollabPasteText(),
			"name":        state.CollabName(),
			"color":       state.CollabColorHex(),
			"iceServers":  ice,
			"decorations": ds,
		}
	}))

	// gotexGitDebug exposes the Git panel's state for headless verification: the
	// open flag, the busy/loaded/error/notice session state, the formatted status
	// line and the .tex files the picker offers.
	js.Global().Set("gotexGitDebug", js.FuncOf(func(js.Value, []js.Value) any {
		tex := state.GitTeXFiles()
		files := make([]any, len(tex))
		for i, f := range tex {
			files[i] = f
		}
		return map[string]any{
			"open":       state.GitActive(),
			"busy":       state.GitBusy(),
			"loadedPath": state.GitLoadedPath(),
			"error":      state.GitError(),
			"notice":     state.GitNotice(),
			"statusLine": state.GitStatusLine(),
			"texFiles":   files,
		}
	}))

	// Git panel action hooks for the headless two-wasm proof. They drive the SAME
	// State methods the panel buttons do (which route through the worker-RPC
	// backend), so a headless run exercises the real off-thread git client without
	// synthesising canvas clicks. Each network action is async (a worker
	// round-trip); the harness polls gotexGitDebug().busy to await completion.

	// gotexGitOpen(bool) opens/closes the panel; opening spawns the git worker.
	js.Global().Set("gotexGitOpen", js.FuncOf(func(_ js.Value, a []js.Value) any {
		state.SetGitOpen(len(a) > 0 && a[0].Bool())
		render()
		return nil
	}))
	// gotexGitConfigure(url, branch, author, email) fills the remote form.
	js.Global().Set("gotexGitConfigure", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) < 4 {
			return nil
		}
		state.SetGitURL(a[0].String())
		state.SetGitBranch(a[1].String())
		state.SetGitAuthor(a[2].String())
		state.SetGitEmail(a[3].String())
		render()
		return nil
	}))
	// gotexGitClone/Commit/Push trigger the async panel actions.
	js.Global().Set("gotexGitClone", js.FuncOf(func(js.Value, []js.Value) any {
		state.GitClone(nil)
		render()
		return nil
	}))
	js.Global().Set("gotexGitCommit", js.FuncOf(func(js.Value, []js.Value) any {
		state.GitCommit(nil)
		render()
		return nil
	}))
	js.Global().Set("gotexGitPush", js.FuncOf(func(js.Value, []js.Value) any {
		state.GitPush(nil)
		render()
		return nil
	}))

	render()
	js.Global().Set("gotexPlaygroundReady", true)
	select {} // keep the Go runtime alive so the callbacks live
}

// isModifierKey reports whether key is a bare modifier keydown (no character).
func isModifierKey(key string) bool {
	switch key {
	case "Shift", "Control", "Alt", "Meta", "AltGraph", "CapsLock", "NumLock", "ScrollLock":
		return true
	}
	return false
}

// isNavKey reports whether key is a caret-navigation key that Shift can extend
// into a selection.
func isNavKey(key string) bool {
	switch key {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End":
		return true
	}
	return false
}

// signRows maps a wheel delta to +3 / -3 / 0 rows by sign, so a flat axis sends
// no scroll on that axis (a pure vertical wheel leaves OffsetX untouched).
func signRows(d float64) int {
	switch {
	case d > 0:
		return 3
	case d < 0:
		return -3
	default:
		return 0
	}
}

// clipboardAPI returns navigator.clipboard (or a null js.Value when the browser
// does not expose it).
func clipboardAPI() js.Value {
	nav := js.Global().Get("navigator")
	if nav.IsUndefined() || nav.IsNull() {
		return js.Null()
	}
	return nav.Get("clipboard")
}

// pasteFromClipboard reads the OS clipboard asynchronously and, once resolved,
// inserts the text into the editor and repaints.
func pasteFromClipboard(state *playground.State, render func()) {
	clip := clipboardAPI()
	if clip.IsUndefined() || clip.IsNull() {
		return
	}
	promise := clip.Call("readText")
	if promise.IsUndefined() || promise.IsNull() {
		return
	}
	promise.Call("then", js.FuncOf(func(_ js.Value, a []js.Value) any {
		text := ""
		if len(a) > 0 && a[0].Type() == js.TypeString {
			text = a[0].String()
		}
		if state.HandlePaste(text) {
			render()
		}
		return nil
	}))
}

// detectDark reads the host page's theme preference.
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
