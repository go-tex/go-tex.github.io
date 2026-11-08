package playground

import (
	"strings"
	"testing"
)

// A created file must be a file, not a gesture: it has a name nothing else holds,
// it is open and editable, and — the part that was broken until #227 — it reaches
// the repository when the user commits.
func TestCreateFileLandsInTheWorkspaceAndCommits(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}seed\end{document}`
	s.BootClone(func(error) {})

	name := s.git.createFile()
	if name != "untitled.tex" {
		t.Fatalf("createFile returned %q: %s", name, s.git.errMsg.Get())
	}
	if _, ok := s.git.buffers[name]; !ok {
		t.Fatalf("the created file has no buffer; buffers = %v", writtenPaths(bufferTexts(s.git)))
	}
	if s.git.loaded.Get() != name {
		t.Errorf("the created file is not the active one: %q", s.git.loaded.Get())
	}
	// ⛔ It must be on screen, not merely in a map. A "New" that leaves the editor
	// showing the previous file looks like it did nothing.
	if got := s.Source(); !strings.Contains(got, name) {
		t.Errorf("the editor does not show the created file: %q", got)
	}

	s.GitCommit(nil)
	body, ok := f.gotWrites[name]
	if !ok {
		t.Fatalf("committing did not write the created file; it wrote %v", writtenPaths(f.gotWrites))
	}
	if !strings.Contains(body, name) {
		t.Errorf("the created file was committed as %q", body)
	}
}

// ⛔ It never overwrites, and it uses the SAME predicate as import rather than a
// second rule that could disagree about what "already taken" means.
func TestCreateFileNeverOverwrites(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"main.tex", "untitled.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}seed\end{document}`
	f.fileData["untitled.tex"] = "% a file that already exists in the repository\n"
	s.BootClone(func(error) {})

	first := s.git.createFile()
	if first != "untitled (2).tex" {
		t.Fatalf("createFile took %q although untitled.tex is in the tree", first)
	}
	second := s.git.createFile()
	if second != "untitled (3).tex" {
		t.Errorf("a second createFile took %q", second)
	}

	// The repository's own file is untouched: its buffer was never created, so a
	// flush cannot carry our template over it.
	if _, ok := s.git.buffers["untitled.tex"]; ok {
		t.Error("creating a file opened a buffer on the repository's existing untitled.tex")
	}
}

// A created file is not empty. An empty .tex renders as a blank page, which looks
// exactly like a file that failed to open — so the one thing a new file must do is
// look like itself.
func TestACreatedFileIsNotEmpty(t *testing.T) {
	s, _, _ := withGit(t)
	name := s.git.createFile()
	if name == "" {
		t.Fatalf("createFile failed: %s", s.git.errMsg.Get())
	}
	buf := s.git.buffers[name]
	if buf == nil || strings.TrimSpace(buf.text) == "" {
		t.Fatal("the created file is empty, which renders as a blank page")
	}
	// And what is in it must be inert in every class: a comment, nothing else.
	for _, line := range strings.Split(strings.TrimSpace(buf.text), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "%") {
			t.Errorf("a created file must contain only comments, found %q", line)
		}
	}
}

// The button exists where the reasoning says it should — with the repository
// commands — and reaches createFile when clicked.
func TestTheNewButtonCreatesAFile(t *testing.T) {
	s, _ := withClonedSidebar(t)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	before := len(s.git.buffers)
	r := findButton(t, s, sbRoleNew).rect
	s.HandleClick(r.X+r.W/2, r.Y+r.H/2)
	if len(s.git.buffers) != before+1 {
		t.Errorf("clicking New changed the buffer count %d → %d, want one more", before, len(s.git.buffers))
	}
	if !strings.Contains(s.git.notice.Get(), "Created") {
		t.Errorf("notice after New = %q", s.git.notice.Get())
	}
}

// bufferTexts is a path→text view of the open buffers, so a failure above can
// name what the workspace held.
func bufferTexts(v *gitView) map[string]string {
	out := make(map[string]string, len(v.buffers))
	for p, b := range v.buffers {
		out[p] = b.text
	}
	return out
}
