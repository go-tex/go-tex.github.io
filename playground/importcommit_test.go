package playground

import "testing"

// ⛔⛔ AN IMPORTED FILE MUST BE COMMITTABLE, and #224 shipped without this test.
//
// A file brought in from the host lives only as a buffer: importFile puts it in
// v.buffers and nothing writes it to the worktree. Staging and committing go
// through flushDirty → dirtyBuffers → bufferDirty, and bufferDirty asks the
// BACKEND for the file's current content:
//
//	data, err := v.backend.ReadFile(p)
//	if err != nil {
//		return false
//	}
//
// For a path the repository has never heard of that read fails, so the buffer is
// judged NOT dirty and is silently left out of the write-back. The import would
// then look complete — a tab, the text on screen, a notice — and the file would
// never reach a commit. "Nothing to commit" for a file the user just imported is
// the shape of work lost.
func TestAnImportedFileIsStaged(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}seed\end{document}`
	s.BootClone(func(error) {})

	const body = `\section{Brought in from the host}`
	if got := s.git.importFile("extra.tex", []byte(body)); got != "extra.tex" {
		t.Fatalf("importFile returned %q: %s", got, s.git.errMsg.Get())
	}

	s.GitStage(nil)

	got, ok := f.gotWrites["extra.tex"]
	if !ok {
		t.Fatalf("staging never wrote the imported file; it wrote %v", writtenPaths(f.gotWrites))
	}
	if got != body {
		t.Errorf("the imported file was staged as %q, want %q", got, body)
	}
}

// The same for a commit, which is the path a user actually reaches for: Stage and
// Commit both flush, and one of them working is not evidence about the other.
func TestAnImportedFileIsCommitted(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}seed\end{document}`
	s.BootClone(func(error) {})

	const body = `% imported and committed`
	if got := s.git.importFile("notes.bib", []byte(body)); got != "notes.bib" {
		t.Fatalf("importFile returned %q: %s", got, s.git.errMsg.Get())
	}

	s.GitCommit(nil)

	if got, ok := f.gotWrites["notes.bib"]; !ok || got != body {
		t.Fatalf("committing did not write the imported file (ok=%v, got=%q); it wrote %v",
			ok, got, writtenPaths(f.gotWrites))
	}
}

// ⛔ The other direction, which is what keeps the fix honest: a file the
// repository DOES list, whose read fails, must stay OUT of the write-back. The
// cheap repair for the two tests above would have been "a failed read means
// dirty", and that writes a buffer back over a file we could not read — including
// resurrecting one that was deleted. Only an unlisted path is new.
func TestAnUnreadableTrackedFileIsNotCalledDirty(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"main.tex", "fragile.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}seed\end{document}`
	s.BootClone(func(error) {})

	// A buffer for a file the tree lists but the backend cannot read: fileData has
	// no entry for it, so ReadFile fails exactly as a transport failure would.
	s.git.buffers["fragile.tex"] = &fileBuffer{text: "whatever the buffer holds"}

	s.GitStage(nil)

	if _, ok := f.gotWrites["fragile.tex"]; ok {
		t.Errorf("a tracked file whose read failed was written back anyway; writes = %v",
			writtenPaths(f.gotWrites))
	}
}

// writtenPaths names what the write-back actually carried, so a failure says
// which files were there instead of only that one was missing.
func writtenPaths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
