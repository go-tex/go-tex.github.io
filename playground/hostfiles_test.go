package playground

import (
	"strings"
	"testing"
)

// ⛔ An import must never overwrite. The workspace IS the only copy of an edited
// file until it is committed, so a silent replace destroys work that exists
// nowhere else — and a user who picks "main.tex" off their machine is at least
// as likely to want it beside the one they have as instead of it.
func TestUniqueImportNameNeverOverwrites(t *testing.T) {
	taken := map[string]bool{"main.tex": true, "main (2).tex": true, "refs.bib": true}

	for _, tc := range []struct{ in, want string }{
		{"notes.tex", "notes.tex"},       // free
		{"main.tex", "main (3).tex"},     // 2 is taken too, so it keeps counting
		{"refs.bib", "refs (2).bib"},     // suffix goes BEFORE the extension
		{"Makefile", "Makefile"},         // no extension, free
		{"main.tex.bak", "main.tex.bak"}, // only the last extension is one
	} {
		if got := uniqueImportName(taken, tc.in); got != tc.want {
			t.Errorf("uniqueImportName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// ⛔ Bounded rather than looping for ever: a workspace holding a thousand
	// copies of one name is not a case to serve, and an unbounded search would
	// hang the page instead of refusing.
	many := map[string]bool{"x.tex": true}
	for n := 2; n < 1000; n++ {
		many[uniqueImportName(many, "x.tex")] = true
	}
	if got := uniqueImportName(many, "x.tex"); got != "" {
		t.Errorf("after a thousand collisions uniqueImportName = %q, want \"\" so the caller can refuse", got)
	}
}

// Each refusal is a reason a reader can act on, not a silent drop.
func TestImportRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		reject string // a distinctive fragment of the expected reason, "" to accept
	}{
		{"main.tex", []byte(`\documentclass{article}`), ""},
		{"", []byte("x"), "no name"},
		{"   ", []byte("x"), "no name"},
		// ⛔ A path, not a name. A picker gives "report.tex"; a drag can give a
		// path, and the workspace is a flat set of repository-relative names.
		// Silently taking the base name would put a file somewhere nobody chose.
		{"../../etc/passwd", []byte("x"), "is a path"},
		{"sub/dir/a.tex", []byte("x"), "is a path"},
		{`C:\x.tex`, []byte("x"), "is a path"},
		{".gitignore", []byte("x"), "dot file"},
		{"huge.tex", make([]byte, maxImportBytes+1), "over the"},
		// Not text: a PDF opened in a text editor reads as mojibake and looks
		// like corruption of the user's own file.
		{"logo.png", []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}, "not UTF-8"},
		// Exactly at the limit is allowed: a bound that rejected its own value
		// would be off by one in the direction that loses a legitimate file.
		{"big.tex", make([]byte, maxImportBytes), ""},
	} {
		got := importRefusal(tc.name, tc.data)
		switch {
		case tc.reject == "" && got != "":
			t.Errorf("importRefusal(%q, %d bytes) = %q, want accepted", tc.name, len(tc.data), got)
		case tc.reject != "" && !strings.Contains(got, tc.reject):
			t.Errorf("importRefusal(%q) = %q, want it to mention %q", tc.name, got, tc.reject)
		}
	}
}

// A refused import must leave the workspace exactly as it was: the failure mode
// to avoid is a half-import that adds a tab for a file with no usable content.
func TestImportFileRefusedChangesNothing(t *testing.T) {
	s := newTestState(t, false)
	v := s.git
	before := len(v.buffers)
	beforeTabs := len(v.openTabs)

	if got := v.importFile("sub/dir/a.tex", []byte("x")); got != "" {
		t.Errorf("importFile accepted a path and called it %q", got)
	}
	if len(v.buffers) != before || len(v.openTabs) != beforeTabs {
		t.Errorf("a refused import changed the workspace: %d→%d buffers, %d→%d tabs",
			before, len(v.buffers), beforeTabs, len(v.openTabs))
	}
	if e := v.errMsg.Get(); !strings.Contains(e, "Not imported") {
		t.Errorf("errMsg = %q, want it to say the file was not imported", e)
	}
}

// Export hands over the BUFFER, not the committed file: what a user means by
// "export" is what they are looking at, and exporting the committed version of a
// file they have just edited is a quiet way to lose the edit.
func TestExportActiveSendsTheBufferAndRefusesWithNoFile(t *testing.T) {
	s := newTestState(t, false)
	v := s.git

	var gotName string
	var gotData []byte
	v.io.Export = func(name string, data []byte) { gotName, gotData = name, data }

	// No file open: refused, with a reason, and the host is never called.
	if v.exportActive() {
		t.Error("exportActive succeeded with no file open")
	}
	if gotName != "" {
		t.Errorf("the host was asked to save %q when nothing was open", gotName)
	}
	if e := v.errMsg.Get(); !strings.Contains(e, "Nothing to export") {
		t.Errorf("errMsg = %q, want it to say there is nothing to export", e)
	}

	// With a file: the host gets its base name and the text ON SCREEN.
	//
	// ⛔ The stale buffer is the point. A buffer map entry is only as fresh as the
	// last time the view stashed it, so an export that read the map would hand over
	// a version from before the user's latest keystrokes — exactly the quiet loss
	// this is meant to avoid. The editor is the truth; the test proves the export
	// goes through the same stash the tab switcher uses.
	v.buffers["paper/main.tex"] = &fileBuffer{text: `STALE — must not be exported`}
	v.loaded.Set("paper/main.tex")
	v.s.SetSource(`\documentclass{article}`)
	if !v.exportActive() {
		t.Fatalf("exportActive failed with a file open: %q", v.errMsg.Get())
	}
	if gotName != "main.tex" {
		t.Errorf("the host was given %q, want the base name main.tex", gotName)
	}
	if string(gotData) != `\documentclass{article}` {
		t.Errorf("the host was given %q, want the text on screen", gotData)
	}
}

// With no host there is nothing to export to, and saying so beats appearing to
// work: off-wasm (and in a test) io.Export is nil.
func TestExportWithoutAHostDoesNotClaimSuccess(t *testing.T) {
	s := newTestState(t, false)
	v := s.git
	v.loaded.Set("main.tex")
	v.s.SetSource("x")
	if v.exportActive() {
		t.Error("exportActive reported success with no host file IO installed")
	}
}
