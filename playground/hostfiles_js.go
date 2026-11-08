// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground

import "syscall/js"

// The browser half of host file import/export. Everything that DECIDES anything
// is in hostfiles.go, where a native test can reach it; what is left here is the
// interop that can only run in a browser: a Blob and a download anchor out, an
// <input type="file"> and a FileReader in.

// enableHostFiles installs the host file IO. The wasm driver's EnableGit calls
// it, so the workspace's Import/Export buttons light up exactly when there is a
// host on the other side of them — and stay plainly disabled when there is not.
func (s *State) enableHostFiles(repaint func()) {
	s.git.io = hostFileIO{
		Export: func(name string, data []byte) { hostExport(name, data) },
		Import: func(deliver hostFileDeliver) { hostImport(deliver, repaint) },
	}
}

// hostExport hands bytes to the browser's own download path.
//
// An object URL and an anchor carrying `download`, rather than any API that
// writes a file directly: this is the one route that works without a permission
// prompt, in every browser the playground targets, and it puts the file exactly
// where the user's other downloads go. The URL is revoked immediately — the
// click has already queued the save, and an unrevoked object URL pins the whole
// buffer in memory for the life of the document.
func hostExport(name string, data []byte) {
	doc := js.Global().Get("document")
	if doc.IsUndefined() {
		return
	}
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	parts := js.Global().Get("Array").New(1)
	parts.SetIndex(0, buf)
	// text/plain and not a TeX media type: the document is text, and a type the
	// host does not know can make a browser append an extension of its own.
	opts := js.Global().Get("Object").New()
	opts.Set("type", "text/plain;charset=utf-8")
	blob := js.Global().Get("Blob").New(parts, opts)

	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := doc.Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", name)
	a.Call("click")
	js.Global().Get("URL").Call("revokeObjectURL", url)
}

// hostImport opens the browser's file picker and delivers each chosen file.
//
// ⛔ NO `accept` FILTER, deliberately. A picker's accept list and the code that
// refuses an import are two halves of one verb, and when they disagree the list
// wins silently: it hides files that would have been accepted, and the user sees
// an empty folder with no reason given. The predicate that decides what may come
// in is importRefusal — size and valid UTF-8, NOT an extension — so a .cls, a
// .sty, a Makefile or an extensionless file is legitimate input here. An accept
// list naming ".tex,.bib" would have hidden every one of them while importRefusal
// stood ready to take them.
//
// `multiple` is on: bringing a paper in means bringing its .bib and its class
// file too, and one file per dialogue would make that four trips.
func hostImport(deliver hostFileDeliver, repaint func()) {
	doc := js.Global().Get("document")
	if doc.IsUndefined() {
		return
	}
	input := doc.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("multiple", true)
	// Off-screen rather than display:none — a hidden input is ignored by some
	// browsers when clicked programmatically, and this one is never shown to the
	// user: the dialogue it opens is the whole interface.
	input.Get("style").Set("position", "fixed")
	input.Get("style").Set("left", "-10000px")

	var onChange, onCancel js.Func
	// Both handlers are released exactly once, whichever fires: a js.Func left
	// registered keeps its Go closure (and the buffers it captured) alive for the
	// life of the page, and a picker is opened repeatedly.
	done := false
	cleanup := func() {
		if done {
			return
		}
		done = true
		input.Call("remove")
		onChange.Release()
		onCancel.Release()
	}

	onChange = js.FuncOf(func(this js.Value, _ []js.Value) any {
		files := input.Get("files")
		n := files.Get("length").Int()
		for i := 0; i < n; i++ {
			readOneFile(files.Index(i), deliver, repaint)
		}
		cleanup()
		return nil
	})
	// A cancelled dialogue fires no change event. Without this the input element
	// and its two handlers would leak once per cancel.
	onCancel = js.FuncOf(func(js.Value, []js.Value) any {
		cleanup()
		return nil
	})
	input.Call("addEventListener", "change", onChange)
	input.Call("addEventListener", "cancel", onCancel)

	doc.Get("body").Call("appendChild", input)
	input.Call("click")
}

// readOneFile reads one picked File and delivers its bytes.
//
// A FileReader rather than file.arrayBuffer(): both are asynchronous, and the
// reader's event handler keeps the delivery on the event-loop goroutine, which
// is where every other mutation of the workspace happens. Reading a file is not
// instant — a multi-megabyte file off a slow disk takes a moment — so the
// repaint is here, with the arrival, not at the call that opened the dialogue.
func readOneFile(file js.Value, deliver hostFileDeliver, repaint func()) {
	name := file.Get("name").String()
	reader := js.Global().Get("FileReader").New()
	var onLoad, onErr js.Func
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		onLoad.Release()
		onErr.Release()
	}
	onLoad = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		res := reader.Get("result")
		if res.IsNull() || res.IsUndefined() {
			// onload with no result is a failed read, not an empty file.
			deliver(name, nil, true)
			if repaint != nil {
				repaint()
			}
			return nil
		}
		view := js.Global().Get("Uint8Array").New(res)
		data := make([]byte, view.Get("length").Int())
		js.CopyBytesToGo(data, view)
		deliver(name, data, false)
		if repaint != nil {
			repaint()
		}
		return nil
	})
	// A read that fails (the file moved, or permission was withdrawn between the
	// pick and the read) must say so rather than leave the user looking at a
	// workspace that silently did not change.
	onErr = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		deliver(name, nil, true)
		if repaint != nil {
			repaint()
		}
		return nil
	})
	reader.Set("onload", onLoad)
	reader.Set("onerror", onErr)
	reader.Call("readAsArrayBuffer", file)
}
