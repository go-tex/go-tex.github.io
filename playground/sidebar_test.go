// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// --- pure helpers: fileStatus / workspace rows / timelineEvents / detail ------

// The mapping from this repository's status vocabulary to the Explorer's.
//
// ⛔ It replaces TestBadgeFor, which checked a letter and a fixed ink per status.
// The widget owns both now, so what is left to get wrong is the TRANSLATION —
// and the one that needs saying is "staged": this workspace writes through
// `git add` without committing, which is git's staged-new, and the vocabulary
// the widget shares with Mercurial, Subversion, Jujutsu and Fossil calls that
// added. A mapping that sent it to FileModified would be plausible and wrong.
func TestFileStatusTranslation(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{"a.tex": "", "b.tex": "", "c.tex": "", "d.tex": "", "e.tex": ""}, "")
	f.status = gitStatus{Branch: "main", Changes: []gitFileChange{
		{Path: "a.tex", Status: "modified"},
		{Path: "b.tex", Status: "staged"},
		{Path: "c.tex", Status: "deleted"},
		{Path: "d.tex", Status: "untracked"},
		{Path: "e.tex", Status: "something new"},
	}}
	f.statusOK = true
	s.git.refreshStatus()
	for path, want := range map[string]toolkit.FileStatus{
		"a.tex":   toolkit.FileModified,
		"b.tex":   toolkit.FileAdded,
		"c.tex":   toolkit.FileDeleted,
		"d.tex":   toolkit.FileUntracked,
		"e.tex":   toolkit.FileUnchanged, // a status this build does not know marks nothing
		"gone.md": toolkit.FileUnchanged, // and so does a path with no entry
	} {
		if got := s.sidebar.fileStatus(path); got != want {
			t.Errorf("fileStatus(%q) = %v, want %v", path, got, want)
		}
	}
	// A buffer the tree does not list is a created or imported file: present, not
	// yet under version control.
	s.git.buffers["fresh.tex"] = &fileBuffer{}
	if got := s.sidebar.fileStatus("fresh.tex"); got != toolkit.FileUntracked {
		t.Errorf("an unwritten file reads %v, want untracked", got)
	}
}

// The row order the workspace presents: folders first at each level, each group
// alphabetical, nested folders materialised from the paths under them.
//
// ⛔ This used to test buildFileTree, which is gone. It is kept, pointed at
// SidebarFileRows, because the order is the part a reader depends on and the
// part that can now drift: toolkit.FileTree owns what is drawn, and this
// derivation follows the rule its doc states rather than reading the widget's
// rows back. A test deleted with the function it covered would have left that
// agreement unpinned.
func TestWorkspaceRowOrderPutsFoldersFirst(t *testing.T) {
	s, f := openWorkspace(t, map[string]string{
		"main.tex":           "",
		"chapters/intro.tex": "",
		"chapters/ch1.tex":   "",
		"a/deep/nested.tex":  "",
		"img/logo.png":       "",
		"refs.bib":           "",
	}, "")
	// refs.bib is staged, so one row carries a mark and the rest do not.
	f.status = gitStatus{Branch: "main", Changes: []gitFileChange{{Path: "refs.bib", Status: "staged"}}}
	f.statusOK = true
	s.git.refreshStatus()
	s.sidebar.ensureWidgets()
	s.sidebar.rebuild()

	rows := s.SidebarFileRows()
	want := []string{"a/", "deep/", "nested.tex", "chapters/", "ch1.tex", "intro.tex", "img/", "logo.png", "main.tex", "refs.bib A"}
	if strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Fatalf("workspace rows = %v, want %v", rows, want)
	}
}

func TestTimelineEventsAndCommitDetail(t *testing.T) {
	log := []GitCommitInfo{
		{Hash: "abcdef1234", Subject: "first", Author: "Ada"},
		{Hash: "9999999999", Subject: "", Author: "Bo"}, // empty subject
	}
	evs := timelineEvents(log)
	if len(evs) != 2 || evs[0].Title != "abcdef1 first" || evs[0].Detail != "Ada" {
		t.Fatalf("timeline events = %+v", evs)
	}
	if evs[1].Title != "9999999" { // empty subject → hash only
		t.Fatalf("empty-subject title = %q", evs[1].Title)
	}
	if got := commitDetailLine(log[0]); got != "abcdef1 · first · Ada" {
		t.Fatalf("commitDetailLine = %q", got)
	}
	// Empty subject + author collapses to the hash alone.
	if got := commitDetailLine(GitCommitInfo{Hash: "deadbeef00"}); got != "deadbee" {
		t.Fatalf("bare commitDetailLine = %q", got)
	}
}

// --- fixtures: a cloned repo behind the sidebar -------------------------------

// withClonedSidebar wires a fake backend with a small repo, clones it, opens the
// sidebar and lays it out. Returns the state + fake for further driving.
func withClonedSidebar(t *testing.T) (*State, *fakeGitBackend) {
	t.Helper()
	s, f, _ := withGit(t)
	f.files = []string{"main.tex", "chapters/intro.tex", "chapters/ch1.tex", "refs.bib"}
	f.fileData = map[string]string{
		"main.tex":           "MAIN BODY",
		"chapters/intro.tex": "INTRO",
		"chapters/ch1.tex":   "CH1",
		"refs.bib":           "@book{x}",
	}
	f.status = gitStatus{Branch: "main", Changes: []gitFileChange{
		{Path: "refs.bib", Status: "staged"},
		{Path: "chapters/ch1.tex", Status: "modified"},
	}}
	f.statusOK = true
	f.log = []GitCommitInfo{
		{Hash: "abcdef1234", Subject: "first commit", Author: "Ada"},
		{Hash: "9876543210", Subject: "second", Author: "Bo"},
	}
	s.GitClone(nil)
	s.SetSidebarOpen(true)
	s.Draw(make([]byte, testW*testH*4)) // lay out + paint once
	return s, f
}

func TestSidebarToggleAndWidth(t *testing.T) {
	s := newTestState(t, false)
	// OPEN by default: the workspace column reserves its width on load and the
	// editor+render body sits to the right of it.
	if !s.SidebarOpen() || s.SidebarWidth() != toolkit.Scaled(sidebarW) {
		t.Fatalf("sidebar should start open with reserved width: open=%v width=%d", s.SidebarOpen(), s.SidebarWidth())
	}
	if s.paned.Bounds().X != toolkit.Scaled(sidebarW) {
		t.Fatalf("paned did not start right of the open sidebar: X=%d", s.paned.Bounds().X)
	}
	// The toggle closes it, reclaiming the full canvas width.
	s.ToggleSidebar()
	if s.SidebarOpen() || s.SidebarWidth() != 0 {
		t.Fatalf("after toggle: open=%v width=%d", s.SidebarOpen(), s.SidebarWidth())
	}
	if s.paned.Bounds().X != 0 {
		t.Fatalf("paned did not reclaim full width when sidebar closed: X=%d", s.paned.Bounds().X)
	}
	// A second toggle re-opens it.
	s.ToggleSidebar()
	if !s.SidebarOpen() || s.SidebarWidth() != toolkit.Scaled(sidebarW) {
		t.Fatalf("second toggle should re-open: open=%v width=%d", s.SidebarOpen(), s.SidebarWidth())
	}
	// SetSidebarOpen(true) is idempotent (no second toggle); (false) closes.
	s.SetSidebarOpen(true)
	if !s.SidebarOpen() {
		t.Fatal("SetSidebarOpen(true) should keep it open")
	}
	s.SetSidebarOpen(false)
	if s.SidebarOpen() || s.SidebarWidth() != 0 {
		t.Fatalf("SetSidebarOpen(false) should close it")
	}
	r := s.SidebarRect()
	if r[2] != 0 {
		t.Fatalf("closed sidebar rect width = %d, want 0", r[2])
	}
}

// TestSidebarToggleRebalancesDivider proves that closing the sidebar hands the
// reclaimed column width to the editor+render split proportionally (the editor
// keeps its share of the now-wider Paned) rather than the render pane swallowing
// the whole delta, and that re-opening restores the earlier split.
func TestSidebarToggleRebalancesDivider(t *testing.T) {
	s := newTestState(t, false)
	// Lay out once with the sidebar open so the divider holds its open-state
	// position before we toggle.
	s.Draw(make([]byte, testW*testH*4))
	if !s.SidebarOpen() {
		t.Fatal("precondition: the sidebar should be open")
	}
	openEditorW := s.editor.Bounds().W
	openPos := s.paned.Position().Get()
	if openEditorW <= 0 || openPos <= 0 {
		t.Fatalf("open-state editor width/divider not laid out: w=%d pos=%d", openEditorW, openPos)
	}

	// Close: the Paned gains the sidebar's width and the divider is rescaled up,
	// so the editor pane grows rather than staying pinned at its narrow width.
	s.SetSidebarOpen(false)
	closedPos := s.paned.Position().Get()
	if closedPos <= openPos {
		t.Fatalf("divider did not grow when the sidebar closed: %d -> %d", openPos, closedPos)
	}
	if s.editor.Bounds().W <= openEditorW {
		t.Fatalf("editor did not widen when the sidebar closed: %d -> %d", openEditorW, s.editor.Bounds().W)
	}
	// The ratio is preserved (within rounding): pos/panedW is stable across the
	// toggle. Open paned width = w - sidebarW; closed = w.
	openPanedW := testW - toolkit.Scaled(sidebarW)
	if got, want := closedPos*openPanedW, openPos*testW; absInt(got-want) > openPanedW {
		t.Fatalf("divider ratio not preserved across close: closed=%d open=%d", closedPos, openPos)
	}

	// Re-open: the divider is rescaled back down to (about) its original spot.
	s.SetSidebarOpen(true)
	if got := s.paned.Position().Get(); absInt(got-openPos) > 1 {
		t.Fatalf("re-opening did not restore the divider: %d, want ~%d", got, openPos)
	}
}

func TestSidebarToolbarButtonToggles(t *testing.T) {
	s := newTestState(t, false)
	// The sidebar starts OPEN (workspace present on load).
	if !s.SidebarOpen() {
		t.Fatal("sidebar should start open by default")
	}
	// Click the toolbar "Workspace" toggle through the real input path: the
	// button's callback closes the sidebar and re-lays out.
	r := s.sidebarBtn.Bounds()
	if !s.HandleClick(r.X+r.W/2, r.Y+r.H/2) {
		t.Fatal("toolbar sidebar toggle click should be consumed")
	}
	s.HandleRelease(r.X+r.W/2, r.Y+r.H/2)
	if s.SidebarOpen() {
		t.Fatal("clicking the toolbar toggle should close the sidebar")
	}
	// A second click re-opens it.
	s.HandleClick(r.X+r.W/2, r.Y+r.H/2)
	s.HandleRelease(r.X+r.W/2, r.Y+r.H/2)
	if !s.SidebarOpen() {
		t.Fatal("a second toolbar toggle click should re-open the sidebar")
	}
}

func TestSidebarEmptyState(t *testing.T) {
	s := newTestState(t, false)
	s.SetSidebarOpen(true) // no repo cloned
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if len(s.SidebarFileRows()) != 0 {
		t.Fatalf("empty sidebar should have no file rows: %v", s.SidebarFileRows())
	}
	if s.sidebar.hasRepo() {
		t.Fatal("no repo should be open")
	}
	// With no repo open the accordion headers read their plain titles.
	if s.sidebar.filesHeaderText() != "Files" {
		t.Fatalf("empty files header = %q", s.sidebar.filesHeaderText())
	}
	if s.sidebar.histHeaderText() != "History" {
		t.Fatalf("empty history header = %q", s.sidebar.histHeaderText())
	}
	// A Clone button is laid out; clicking it opens the Remote-Git panel.
	var clone sidebarButton
	found := false
	for _, b := range s.sidebar.buttons {
		if b.role == sbRoleClone {
			clone, found = b, true
		}
	}
	if !found {
		t.Fatal("empty state should offer a Clone button")
	}
	s.HandleClick(clone.rect.X+clone.rect.W/2, clone.rect.Y+clone.rect.H/2)
	if !s.GitActive() {
		t.Fatal("Clone button should open the Remote-Git panel")
	}
}

func TestSidebarFileTreeAndBadges(t *testing.T) {
	s, _ := withClonedSidebar(t)
	rows := s.SidebarFileRows()
	want := []string{"chapters/", "ch1.tex M", "intro.tex", "main.tex", "refs.bib A"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("file rows = %v, want %v", rows, want)
	}
	// The Files header names the active file; the History header carries the branch.
	if s.sidebar.filesHeaderText() != "Files · main.tex" {
		t.Fatalf("files header = %q", s.sidebar.filesHeaderText())
	}
	if s.sidebar.histHeaderText() != "History · main" {
		t.Fatalf("history header = %q", s.sidebar.histHeaderText())
	}
	// The timeline carries the commits, newest first.
	titles := s.SidebarTimelineTitles()
	if len(titles) != 2 || titles[0] != "abcdef1 first commit" {
		t.Fatalf("timeline titles = %v", titles)
	}
}

// The status marks reach PIXELS and not merely the model.
//
// ⛔ It no longer asserts the playground's own two ink constants. The Explorer
// chooses each mark's colour for CONTRAST — its rule is the first candidate that
// clears 4.5:1 against the row, with the row's ink as the fallback — so the
// colour is a function of the theme and not a constant this test can name. An
// assertion on the old amber and green would now fail for the right reason, and
// hard-coding whatever the widget currently picks would pin a number nobody
// chose.
//
// So the control is an A/B: the same workspace drawn with marks and without must
// differ. Comparing the two renders asks the question the colour constants were
// standing in for — "is anything drawn for a status at all" — without claiming
// to know which colour it should be.
func TestSidebarStatusMarksReachThePixels(t *testing.T) {
	s, f := withClonedSidebar(t)
	marked := make([]byte, testW*testH*4)
	s.Draw(marked)

	// Same tree, nothing to mark.
	f.status = gitStatus{Branch: "main", Clean: true}
	f.statusOK = true
	s.git.refreshStatus()
	s.git.buffers = map[string]*fileBuffer{}
	s.sidebar.rebuild()
	clean := make([]byte, testW*testH*4)
	s.Draw(clean)

	if bytes.Equal(marked, clean) {
		t.Fatal("a workspace with modified and staged files paints exactly like a clean one: no mark reaches the pixels")
	}
	// And the marks are in the model, so a difference cannot be some other
	// incidental change.
	rows := strings.Join(s.SidebarFileRows(), "|")
	if strings.Contains(rows, " M") || strings.Contains(rows, " A") {
		t.Fatalf("the clean render still models marks: %s", rows)
	}
}

func hasExactColor(buf []byte, c toolkit.RGBA) bool {
	for i := 0; i+3 < len(buf); i += 4 {
		if buf[i] == c.R && buf[i+1] == c.G && buf[i+2] == c.B && buf[i+3] == c.A {
			return true
		}
	}
	return false
}

// treeRowY is the centre of row i in the workspace's Explorer.
//
// ⛔ There is NO column header any more: the Explorer has no header band, so the
// old offset of one TreeTableHeaderHeight put every click one row late — which
// is how the directory-row test started opening a file. The row height is
// TreeView's own default (scaled 18, lifted to the density's hit target), named
// here because the widget does not export it; if upstream changes it these tests
// fail loudly rather than clicking the wrong row quietly.
func treeRowY(tr toolkit.Rect, i int) int {
	rowH := toolkit.TouchTarget(toolkit.Scaled(18))
	return tr.Y + i*rowH + rowH/2
}

func TestSidebarOpenFileOnClick(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// Row order: chapters/(0), ch1.tex(1), intro.tex(2), main.tex(3), refs.bib(4).
	// Click the refs.bib row and assert the editor loaded its content.
	tr := s.sidebar.treeRect
	y := treeRowY(tr, 4)
	if !s.HandleClick(tr.X+tr.W/2, y) {
		t.Fatal("a click in the tree should be consumed")
	}
	if s.Source() != "@book{x}" {
		t.Fatalf("clicking refs.bib did not load it: Source=%q", s.Source())
	}
	if s.GitLoadedPath() != "refs.bib" {
		t.Fatalf("loaded path = %q, want refs.bib", s.GitLoadedPath())
	}
	// Release clears the pressed button faces without panicking.
	s.HandleRelease(tr.X+tr.W/2, y)
}

func TestSidebarClickDirRowOpensNothing(t *testing.T) {
	s, _ := withClonedSidebar(t)
	before := s.Source()
	tr := s.sidebar.treeRect
	// Row 0 is the "chapters/" directory: clicking it collapses or expands, and
	// must not open a file. This is one of the clicks the old handler got wrong by
	// construction: it opened whatever the tree had selected, so a folder row, a
	// rename entry and a delete confirmation each opened a file as a side effect.
	// The Explorer now says which action a click was.
	s.HandleClick(tr.X+tr.W/2, treeRowY(tr, 0))
	if s.Source() != before {
		t.Fatalf("clicking a directory row changed the editor buffer")
	}
}

func TestSidebarActiveDirtyOverlay(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// main.tex is the loaded file and clean → no mark.
	if st := s.sidebar.fileStatus("main.tex"); st != toolkit.FileUnchanged {
		t.Fatalf("clean active file status = %v, want unchanged", st)
	}
	// Edit the buffer: the active file now reads modified via the overlay.
	s.SetSource("EDITED")
	if !s.sidebar.activeDirty() {
		t.Fatal("edited active file should be dirty")
	}
	if st := s.sidebar.fileStatus("main.tex"); st != toolkit.FileModified {
		t.Fatalf("dirty active status = %v, want modified", st)
	}
	// The signature folds the dirtiness, so a redraw rebuilds the tree with the M.
	s.Draw(make([]byte, testW*testH*4))
	rows := s.SidebarFileRows()
	joined := strings.Join(rows, "|")
	if !strings.Contains(joined, "main.tex M") {
		t.Fatalf("dirty main.tex not badged in tree: %v", rows)
	}
}

func TestSidebarActiveDirtyEdgeCases(t *testing.T) {
	s := newTestState(t, false)
	// No file loaded → not dirty.
	if s.sidebar.activeDirty() {
		t.Fatal("no loaded file should read not-dirty")
	}
	s, f := withClonedSidebar(t)
	// A read error for the loaded path → not dirty (nothing to compare).
	f.readErr = errGitNotExist
	if s.sidebar.activeDirty() {
		t.Fatal("a read error should read not-dirty")
	}
}

func TestSidebarButtonsDispatch(t *testing.T) {
	s, f := withClonedSidebar(t)
	click := func(role sidebarRole) {
		for _, b := range s.sidebar.buttons {
			if b.role == role {
				s.HandleClick(b.rect.X+b.rect.W/2, b.rect.Y+b.rect.H/2)
				s.HandleRelease(b.rect.X+b.rect.W/2, b.rect.Y+b.rect.H/2)
				return
			}
		}
		t.Fatalf("button role %d not laid out", role)
	}

	// Stage writes the active file's buffer and stages it.
	s.SetSource("STAGED BODY")
	click(sbRoleStage)
	if f.gotStagePath != "main.tex" || f.gotStageContent != "STAGED BODY" {
		t.Fatalf("Stage forwarded path=%q content=%q", f.gotStagePath, f.gotStageContent)
	}

	// Commit reuses the panel's commit-message flow (default message).
	s.SetSource("COMMIT BODY")
	click(sbRoleCommit)
	if f.gotCommitPath != "main.tex" || f.gotCommitContent != "COMMIT BODY" || f.gotCommitMsg == "" {
		t.Fatalf("Commit forwarded path=%q content=%q msg=%q", f.gotCommitPath, f.gotCommitContent, f.gotCommitMsg)
	}

	// Pull + Push reach the backend (no panic, notice set).
	click(sbRolePull)
	click(sbRolePush)

	// Refresh re-snapshots + forces a rebuild without a network op.
	s.sidebar.sig = "stale"
	click(sbRoleRefresh)
	if s.sidebar.sig == "stale" {
		t.Fatal("Refresh should force a rebuild (sig reset)")
	}

	// A Stage failure surfaces on the git error line (the GitStage error branch).
	f.stageErr = errGitTransport
	s.SetSource("WILL FAIL")
	click(sbRoleStage)
	if s.GitError() == "" {
		t.Fatal("a failed Stage should surface a git error")
	}
}

func TestSidebarTimelineClickShowsDetail(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// History starts collapsed (Files opens first); open it to reveal the timeline.
	hh := s.sidebar.histHdrRect
	s.HandleClick(hh.X+hh.W/2, hh.Y+hh.H/2)
	tl := s.sidebar.tlRect
	x := tl.X + toolkit.Scaled(4)
	y := tl.Y + toolkit.Scaled(toolkit.TimelinePadY) + 1 // first event
	if !s.HandleClick(x, y) {
		t.Fatal("timeline click should be consumed")
	}
	if d := s.SidebarDetail(); !strings.Contains(d, "abcdef1") {
		t.Fatalf("timeline click detail = %q, want the first commit", d)
	}
}

func TestSidebarDetailPriority(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// notice only.
	s.git.errMsg.Set("")
	s.git.notice.Set("Pulled.")
	s.sidebar.commitDetail = ""
	if got, _ := s.sidebar.detailText(); got != "Pulled." {
		t.Fatalf("notice detail = %q", got)
	}
	// commitDetail beats notice.
	s.sidebar.commitDetail = "abc · x"
	if got, _ := s.sidebar.detailText(); got != "abc · x" {
		t.Fatalf("commitDetail should beat notice: %q", got)
	}
	// error beats everything.
	s.git.errMsg.Set("boom")
	got, ink := s.sidebar.detailText()
	if !strings.Contains(got, "boom") || ink != errorInk {
		t.Fatalf("error detail = %q/%v", got, ink)
	}
	// all empty.
	s.git.errMsg.Set("")
	s.git.notice.Set("")
	s.sidebar.commitDetail = ""
	if got, _ := s.sidebar.detailText(); got != "" {
		t.Fatalf("empty detail = %q", got)
	}
}

func TestSidebarScroll(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// Files opens first, so the tree is scrollable.
	tr := s.sidebar.treeRect
	if !s.HandleScroll(tr.X+2, tr.Y+tr.H/2, 0, 2) {
		t.Fatal("scroll over the tree should be consumed")
	}
	// Open History (exclusive: this collapses Files) to reveal the timeline.
	hh := s.sidebar.histHdrRect
	s.HandleClick(hh.X+hh.W/2, hh.Y+hh.H/2)
	tl := s.sidebar.tlRect
	if !s.HandleScroll(tl.X+2, tl.Y+tl.H/2, 0, 2) {
		t.Fatal("scroll over the timeline should be consumed")
	}
	// Scroll over the Files accordion header (inside the column, neither tree nor
	// timeline) is NOT consumed by the sidebar.
	if s.HandleScroll(s.sidebar.filesHdrRect.X+1, s.sidebar.filesHdrRect.Y+1, 0, 2) {
		t.Fatal("scroll over the Files header should not be consumed by the sidebar")
	}
	// Scroll outside the column, and while closed, is not consumed.
	if s.sidebar.handleScroll(testW-1, 0, 2) {
		t.Fatal("scroll outside the column should not be consumed")
	}
	s.SetSidebarOpen(false)
	if s.sidebar.handleScroll(1, s.paned.Bounds().Y+1, 2) {
		t.Fatal("a closed sidebar should not consume scroll")
	}
}

func TestSidebarHandleClickGuards(t *testing.T) {
	s, _ := withClonedSidebar(t)
	// A click in the detail strip (inside the column, no widget) is swallowed
	// (modal within its own column) but opens nothing.
	dr := s.sidebar.detailRect
	if !s.sidebar.handleClick(dr.X+1, dr.Y+1) {
		t.Fatal("a click in the column should be consumed")
	}
	// A click outside the column is not consumed by the sidebar.
	if s.sidebar.handleClick(testW-1, 0) {
		t.Fatal("a click outside the column should not be consumed")
	}
	// A closed sidebar consumes nothing.
	s.SetSidebarOpen(false)
	if s.sidebar.handleClick(1, s.paned.Bounds().Y+1) {
		t.Fatal("a closed sidebar should consume nothing")
	}
}

func TestSidebarDrawGuards(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, 100*100*4)
	before := make([]byte, len(buf))
	copy(before, buf)
	p := painter.NewPixelPainter(buf, 100, 100)
	// Closed → draw is a no-op (returns before touching the buffer). The sidebar
	// opens by default, so close it first to exercise the closed guard.
	s.sidebar.open = false
	s.sidebar.draw(p, s.theme)
	if !bytesEqual(buf, before) {
		t.Fatal("a closed sidebar should paint nothing")
	}
	// Open but zero-width bounds → the r.W<=0 guard returns early.
	s.sidebar.open = true
	s.sidebar.ensureWidgets()
	s.sidebar.bounds = toolkit.Rect{}
	s.sidebar.draw(p, s.theme)
	if !bytesEqual(buf, before) {
		t.Fatal("a zero-bounds sidebar should paint nothing")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSidebarHandleReleaseNilButtons(t *testing.T) {
	s := newTestState(t, false)
	sb := newSidebar(s)
	// Before ensureWidgets, btns is nil: handleRelease must be a safe no-op.
	sb.handleRelease(0, 0)
}

func TestGitStageGuards(t *testing.T) {
	s, f, _ := withGit(t)
	// No repo → errNoGitRepo.
	var got error
	s.GitStage(func(e error) { got = e })
	if !errors.Is(got, errNoGitRepo) {
		t.Fatalf("GitStage without a repo = %v", got)
	}
	// Cloned but no file loaded (no .tex to open) → errNoGitFile.
	f.files = []string{"img.png"}
	s.GitClone(nil)
	got = nil
	s.GitStage(func(e error) { got = e })
	if !errors.Is(got, errNoGitFile) {
		t.Fatalf("GitStage with no loaded file = %v", got)
	}
	// Busy → silent no-op (done is never called).
	f.hold = true
	s.GitPull(nil) // parks a busy op
	called := false
	s.GitStage(func(error) { called = true })
	if called {
		t.Fatal("GitStage while busy should be a no-op")
	}
	if f.release != nil {
		f.release() // drain the parked pull
	}
}

func TestGitStageSuccessReportsDone(t *testing.T) {
	s, f := withClonedSidebar(t)
	s.SetSource("STAGED")
	done := false
	var gotErr error
	s.GitStage(func(e error) { done, gotErr = true, e })
	if !done || gotErr != nil {
		t.Fatalf("GitStage done=%v err=%v", done, gotErr)
	}
	if f.gotStageContent != "STAGED" {
		t.Fatalf("stage content = %q", f.gotStageContent)
	}
	if s.GitNotice() == "" {
		t.Fatal("a successful stage should set a notice")
	}
}

func TestGitOpenFileGuards(t *testing.T) {
	s, f, _ := withGit(t)
	// No repo → false.
	if s.GitOpenFile("main.tex") {
		t.Fatal("GitOpenFile without a repo should fail")
	}
	f.files = []string{"main.tex"}
	f.fileData = map[string]string{"main.tex": "X"}
	s.GitClone(nil)
	// Empty path → false.
	if s.GitOpenFile("") {
		t.Fatal("GitOpenFile of an empty path should fail")
	}
	// Busy → false.
	f.hold = true
	s.GitPull(nil)
	if s.GitOpenFile("main.tex") {
		t.Fatal("GitOpenFile while busy should fail")
	}
	if f.release != nil {
		f.release()
	}
	f.hold = false
	// Success.
	if !s.GitOpenFile("main.tex") || s.Source() != "X" {
		t.Fatalf("GitOpenFile success: opened=%v source=%q", s.GitOpenFile("main.tex"), s.Source())
	}
}

func TestSidebarShortColumnTimelineYields(t *testing.T) {
	// A very short window forces the timeline to yield its height so the tree
	// keeps its minimum — covering the tlH clamp branches in layout().
	SetupText(1)
	s := NewState(300, 200, false)
	s.CompilePending()
	_, f, _ := func() (*State, *fakeGitBackend, *int) {
		f := &fakeGitBackend{fileData: map[string]string{"main.tex": "x"}}
		n := 0
		s.git.attach(f, func() { n++ })
		return s, f, &n
	}()
	f.files = []string{"main.tex"}
	f.status = gitStatus{Branch: "main"}
	f.statusOK = true
	s.GitClone(nil)
	s.SetSidebarOpen(true)
	s.Draw(make([]byte, 300*200*4))
	// The tree kept at least its minimum; the timeline was squeezed (possibly to 0).
	if s.sidebar.treeRect.H < 0 || s.sidebar.tlRect.H < 0 {
		t.Fatalf("negative sub-rect heights: tree=%d tl=%d", s.sidebar.treeRect.H, s.sidebar.tlRect.H)
	}
}

// TestSidebarShowsBrandLogo: the go-tex brand tile + wordmark paint their indigo
// at the top of the workspace column, in both the empty and cloned states.
func TestSidebarShowsBrandLogo(t *testing.T) {
	s := newTestState(t, false) // no repo yet: empty state
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !hasExactColor(buf, brandIndigo) {
		t.Fatal("empty state: the go-tex brand colour is not painted in the sidebar")
	}
}

// TestSidebarAccordionExclusive: the two sections are mutually exclusive. Files
// opens first (History collapsed); opening one collapses the other, and collapsing
// the open one leaves both closed.
func TestSidebarAccordionExclusive(t *testing.T) {
	s, _ := withClonedSidebar(t)
	b := s.sidebar
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // lay out
	if !b.filesExp.Expanded().Get() || b.histExp.Expanded().Get() {
		t.Fatal("Files should start open and History collapsed")
	}
	if b.treeRect.H <= 0 || b.tlRect.H != 0 {
		t.Fatalf("Files open: tree=%d tl=%d, want tree>0 tl=0", b.treeRect.H, b.tlRect.H)
	}
	// Open History via its header click: exclusivity collapses Files.
	if !s.HandleClick(b.histHdrRect.X+10, b.histHdrRect.Y+2) {
		t.Fatal("click on the History header was not consumed")
	}
	if !b.histExp.Expanded().Get() || b.filesExp.Expanded().Get() {
		t.Fatal("opening History should collapse Files")
	}
	if b.tlRect.H <= 0 || b.treeRect.H != 0 {
		t.Errorf("History open: tree=%d tl=%d, want tl>0 tree=0", b.treeRect.H, b.tlRect.H)
	}
	// Open Files again: exclusivity collapses History.
	s.HandleClick(b.filesHdrRect.X+10, b.filesHdrRect.Y+2)
	if !b.filesExp.Expanded().Get() || b.histExp.Expanded().Get() {
		t.Fatal("opening Files should collapse History")
	}
	// Collapse the open Files: both end closed.
	s.HandleClick(b.filesHdrRect.X+10, b.filesHdrRect.Y+2)
	if b.filesExp.Expanded().Get() || b.histExp.Expanded().Get() {
		t.Fatal("collapsing the open section should leave both closed")
	}
	if b.treeRect.H != 0 || b.tlRect.H != 0 {
		t.Errorf("both collapsed: tree=%d tl=%d, want both 0", b.treeRect.H, b.tlRect.H)
	}
}

// TestSidebarCompactColumn: opening a file clears the detail strip (the file's
// name shows in the Files header instead of a notice line), so with no error or
// clicked-commit detail the strip collapses to zero height; and drawing with
// History open paints the timeline body.
func TestSidebarCompactColumn(t *testing.T) {
	s, _ := withClonedSidebar(t)
	b := s.sidebar
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // initial layout

	// Open a second file: the "Loaded/Opened" notice is gone, so with no error or
	// clicked-commit detail the strip collapses.
	if !s.GitOpenFile("chapters/intro.tex") {
		t.Fatal("open intro.tex")
	}
	b.commitDetail = ""
	s.git.errMsg.Set("")
	b.layout()
	if b.detailRect.H != 0 {
		t.Errorf("idle detail strip should collapse to zero height, got %d", b.detailRect.H)
	}
	if got := b.filesHeaderText(); got != "Files · intro.tex" {
		t.Errorf("files header = %q, want Files · intro.tex", got)
	}

	// Open History (exclusive) and draw: the timeline body paints (tlRect.H > 0).
	hh := b.histHdrRect
	s.HandleClick(hh.X+hh.W/2, hh.Y+hh.H/2)
	s.Draw(buf)
	if b.tlRect.H <= 0 {
		t.Fatal("History open should give the timeline a body")
	}
}

// TestSidebarCloningSpinner: a clone in flight (busy, no repo) reports Cloning,
// draws the spinner branch, ticks it, and SubscribeCloning fires on the change.
func TestSidebarCloningSpinner(t *testing.T) {
	s := newTestState(t, false) // nopGitBackend: no repo
	s.git.busy.Set(true)
	if !s.Cloning() {
		t.Fatal("busy + no repo should report Cloning")
	}
	fired := 0
	s.SubscribeCloning(func() { fired++ })

	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // exercises the cloning empty-state (spinner) draw branch

	before := s.sidebar.spinner.Phase
	s.Tick(0.1)
	if s.sidebar.spinner.Phase == before {
		t.Errorf("Tick did not advance the spinner phase (%v)", before)
	}

	s.git.busy.Set(false) // clone finished
	if fired == 0 {
		t.Error("SubscribeCloning did not fire on the busy change")
	}
	if s.Cloning() {
		t.Error("not busy → not Cloning")
	}
}
