// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Files in and out of the host machine.
//
// The workspace could only ever be filled by cloning a remote: there was no way
// to bring a .tex off this computer into it, and no way to get one back out. A
// playground you cannot feed your own document to is a demonstration, not a
// tool.
//
// ⛔ WHAT IS DECIDED HERE AND WHAT IS NOT. The browser half — a file picker, a
// Blob, an anchor carrying `download` — is twenty lines of js interop and lives
// in hostfiles_js.go. Everything a test can judge is here: what an imported file
// may be called, what happens when it collides with a file already open, and
// what is refused. A policy written beside `syscall/js` can only be checked in a
// browser, which is where this repository's tests are least able to run (#212).

// maxImportBytes bounds a single imported file.
//
// The engine already caps one paragraph at 200 000 nodes and refuses a document
// that would exhaust memory, and this is the same argument one step earlier: the
// workspace holds every buffer in memory and the page has no way to page them
// out. 4 MiB is far above any real .tex (the largest in the arXiv corpus this
// engine is measured on is under 700 KiB) and far below what makes a tab
// unresponsive.
const maxImportBytes = 4 << 20

// importRefusal says why a file cannot be taken in, or "" when it can.
//
// It refuses on three grounds, each with a reason a reader can act on rather
// than a silent drop:
//
//   - a path, not a name. A picker hands back "report.tex", but a drag from a
//     folder can hand back "../../etc/passwd" or "C:\x.tex", and the workspace
//     is a flat set of repository-relative paths. Taking the base name is not
//     enough: a caller that passed a path meant something by it, and guessing
//     which part it meant is how a file lands somewhere nobody chose.
//   - too big (see maxImportBytes).
//   - not text. The editor is a text editor; a PDF or a PNG dropped on it would
//     open as mojibake and look like corruption of the user's own file.
func importRefusal(name string, data []byte) string {
	switch {
	case strings.TrimSpace(name) == "":
		return "a file with no name"
	case name != path.Base(name) || strings.ContainsAny(name, `\/`):
		return "“" + name + "” is a path, not a file name"
	case strings.HasPrefix(name, "."):
		return "“" + name + "” is a dot file; rename it to bring it in"
	case len(data) > maxImportBytes:
		return "“" + name + "” is " + strconv.Itoa(len(data)>>10) + " KiB, over the " +
			strconv.Itoa(maxImportBytes>>20) + " MiB limit for one file"
	case !utf8.Valid(data):
		return "“" + name + "” is not UTF-8 text; the editor opens text files"
	}
	return ""
}

// uniqueImportName returns the name an imported file should take, given the
// names already in the workspace.
//
// ⛔ It never overwrites. An import that silently replaced an open buffer would
// destroy work the user had not saved anywhere else — the workspace IS the only
// copy until a commit — and the user who picks "main.tex" from their machine is
// at least as likely to want it beside the one they have as instead of it. So a
// collision suffixes before the extension, the way a browser's own download
// folder does: main.tex, main (2).tex, main (3).tex.
func uniqueImportName(taken map[string]bool, name string) string {
	if !taken[name] {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	// Bounded: a workspace with thousands of copies of one name is not a case to
	// serve, and an unbounded loop here would hang the page rather than refuse.
	for n := 2; n < 1000; n++ {
		candidate := stem + " (" + strconv.Itoa(n) + ")" + ext
		if !taken[candidate] {
			return candidate
		}
	}
	return ""
}

// hostFileIO is the browser half, replaced in tests.
//
// Export takes the bytes and the name to suggest; Import asks the user to pick
// files and calls back once per file, on the event-loop goroutine. Both are nil
// off-wasm, which is what makes the workspace's buttons disable themselves
// rather than fail when there is no host to talk to.
type hostFileIO struct {
	Export func(name string, data []byte)
	Import func(deliver hostFileDeliver)
}

// hostFileDeliver receives one picked file.
//
// ⛔ readErr is a separate channel and not an empty `data`, because the two are
// otherwise indistinguishable and importRefusal ACCEPTS empty data — a zero-byte
// .tex is a real file a user may pick. A failed read reported as empty bytes
// would therefore succeed: it would add a tab, name it after the user's file, and
// show an empty document, which is exactly the shape of a lost file. The import
// must be able to tell "nothing in it" from "could not read it".
type hostFileDeliver func(name string, data []byte, readErr bool)

// workspaceNames is the set of names the workspace already holds — the open
// buffers plus whatever the repository tree lists.
func (v *gitView) workspaceNames() map[string]bool {
	taken := make(map[string]bool, len(v.buffers)+len(v.files))
	for p := range v.buffers {
		taken[p] = true
	}
	// The working-tree list too, not just the open buffers: a file that exists in
	// the repository but has never been opened is still a name an import must not
	// land on, and it is the one a collision check that only looked at buffers
	// would walk straight into.
	for _, p := range v.files {
		taken[p] = true
	}
	return taken
}

// importFile takes one picked file into the workspace and opens it, or records
// why it could not.
//
// It returns the name the file took, or "" when it was refused — the caller
// reports a refusal rather than this function deciding how to phrase it twice.
func (v *gitView) importFile(name string, data []byte) string {
	if why := importRefusal(name, data); why != "" {
		v.errMsg.Set("Not imported: " + why)
		return ""
	}
	taken := v.workspaceNames()
	final := uniqueImportName(taken, name)
	if final == "" {
		v.errMsg.Set("Not imported: too many files already called “" + name + "”")
		return ""
	}
	// Straight into a buffer, which is the same state openFile builds for a file
	// read out of the repository — so an imported file is editable, renderable
	// and stageable by every path that already works, with no second notion of
	// "a file that came from outside".
	v.stashActive()
	v.buffers[final] = &fileBuffer{text: string(data)}
	v.loaded.Set(final)
	v.addTab(final)
	v.s.SetSource(string(data))
	v.notice.Set("Imported " + final)
	v.errMsg.Set("")
	return final
}

// importDelivered is what the host's picker calls back, once per chosen file.
//
// It is separate from importFile because the picker is asynchronous: it returns
// long before the user has chosen anything, and each file arrives on the event
// loop afterwards. So the repaint belongs here, with the arrival, and not at the
// call site that merely opened a dialogue.
func (v *gitView) importDelivered(name string, data []byte, readErr bool) {
	if readErr {
		v.errMsg.Set("Not imported: “" + name + "” could not be read")
		v.refresh()
		return
	}
	v.importFile(name, data)
	v.refresh()
}

// exportActive hands the active file's current text to the host.
//
// The buffer and not the repository: what a user means by "export" is what they
// are looking at, including edits they have not committed — exporting the
// committed version of a file they have just changed would be a quiet way to
// lose the change.
func (v *gitView) exportActive() bool {
	if v.io.Export == nil {
		return false
	}
	name := v.loaded.Get()
	if name == "" {
		v.errMsg.Set("Nothing to export: no file is open")
		return false
	}
	v.stashActive()
	buf := v.buffers[name]
	if buf == nil {
		v.errMsg.Set("Nothing to export: “" + name + "” has no buffer")
		return false
	}
	v.io.Export(path.Base(name), []byte(buf.text))
	v.notice.Set("Exported " + path.Base(name))
	v.errMsg.Set("")
	return true
}
