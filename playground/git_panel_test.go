// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// The Git panel has no launcher of its own any more: the workspace sidebar is
// the way in, and its Clone row opens the panel. This drives that path — the
// sidebar's own dispatch — and checks the panel came up.
func TestGitPanelOpensFromTheWorkspace(t *testing.T) {
	s := newTestState(t, false)
	if s.GitActive() {
		t.Fatal("the panel must start closed")
	}
	s.sidebar.dispatch(sbRoleClone)
	if !s.GitActive() {
		t.Fatal("the workspace Clone action did not open the Git panel")
	}
}

// And nothing in the toolbar band opens it any more: a click where the launcher
// used to sit is not consumed by the Git view.
func TestNoGitLauncherInTheToolbar(t *testing.T) {
	s := newTestState(t, false)
	s.git.layout()
	for x := 0; x < s.w; x += 8 {
		y := s.topZoneH + s.toolbarH/2
		if s.git.handleClick(x, y) {
			t.Fatalf("the Git view still consumes a toolbar click at x=%d", x)
		}
	}
	if s.GitActive() {
		t.Fatal("a toolbar click opened the Git panel")
	}
}

func TestGitLayoutEdges(t *testing.T) {
	s := newTestState(t, false)
	v := s.git
	// A surface narrower than the panel clamps the panel to fit.
	s.w = 120
	v.open = true
	v.layout()
	if v.panel.W > s.w {
		t.Fatalf("panel width %d exceeds surface %d", v.panel.W, s.w)
	}
}

func TestGitClickRouting(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex", "chapters/a.tex", "chapters/b.tex", "d.tex"}
	f.fileData["main.tex"] = "x"
	f.fileData["chapters/a.tex"] = "A"
	s.GitClone(nil) // loads main.tex, and sets up a >1 tex picker
	v.open = true
	v.layout()

	// Closed branch: with no launcher of its own, the view consumes nothing while
	// closed, wherever the click lands.
	v.open = false
	v.layout()
	if v.handleClick(-100, -100) {
		t.Fatal("a click was consumed while the panel was closed")
	}
	v.openPanel() // what the workspace sidebar's Clone row does
	if !v.open {
		t.Fatal("openPanel did not open the panel")
	}

	// Focus a text field.
	v.layout()
	var urlBox = v.boxes[0]
	if !v.handleClick(urlBox.rect.X+2, urlBox.rect.Y+2) || v.focus != urlBox.field {
		t.Fatalf("field click did not focus it (focus=%d)", v.focus)
	}

	// Click the file-pick button for chapters/a.tex (index 1 in texFiles) → loads
	// that file (defocuses fields).
	v.layout()
	loaded := false
	for _, b := range v.buttons {
		if b.role == gitRolePickFile && b.arg == 1 {
			loaded = v.handleClick(b.rect.X+1, b.rect.Y+1)
		}
	}
	if !loaded {
		t.Fatal("file-pick button not clicked")
	}
	if v.focus != gitFieldNone {
		t.Fatal("clicking a button should defocus the field")
	}
	if s.GitLoadedPath() != "chapters/a.tex" {
		t.Fatalf("pick did not load chapters/a.tex: %q", s.GitLoadedPath())
	}

	// Click Close.
	v.layout()
	for _, b := range v.buttons {
		if b.role == gitRoleClose {
			v.handleClick(b.rect.X+1, b.rect.Y+1)
		}
	}
	if v.open {
		t.Fatal("Close did not close the panel")
	}

	// A click on empty panel space is still swallowed (modal).
	v.open = true
	v.layout()
	if !v.handleClick(v.panel.X+1, v.panel.Y+v.panel.H-2) {
		t.Fatal("modal panel should swallow a background click")
	}
}

func TestGitPickFileBusyAndBounds(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex", "a.tex"}
	f.fileData["main.tex"] = "x"
	f.fileData["a.tex"] = "A"
	s.GitClone(nil)
	// Out-of-range index is a no-op (defensive).
	v.dispatch(gitRolePickFile, 99)
	// Busy blocks a pick.
	v.busy.Set(true)
	v.dispatch(gitRolePickFile, 1)
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("busy pick changed the file to %q", s.GitLoadedPath())
	}
	v.busy.Set(false)
	// roleNone is a no-op branch.
	v.dispatch(gitRoleNone, 0)
}

func TestGitDispatchActionRoles(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = "x"
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Clean: true}

	// Each action role routes to the matching State method.
	v.dispatch(gitRoleClone, 0)
	if s.GitLoadedPath() != "main.tex" {
		t.Fatalf("dispatch Clone did not load: %q", s.GitLoadedPath())
	}
	v.dispatch(gitRolePull, 0)
	v.dispatch(gitRoleCommit, 0)
	if f.gotCommitPath != "main.tex" {
		t.Fatalf("dispatch Commit did not reach the backend: %q", f.gotCommitPath)
	}
	v.dispatch(gitRolePush, 0)
	if s.GitNotice() == "" {
		t.Fatal("dispatch Push produced no notice")
	}
}

func TestGitCharEditsFocusedField(t *testing.T) {
	s, _, _ := withGit(t)
	v := s.git
	// Unfocused / closed: not consumed.
	if v.handleChar("x") {
		t.Fatal("closed panel consumed a char")
	}
	v.open = true
	if v.handleChar("x") {
		t.Fatal("unfocused field consumed a char")
	}
	// Focused URL field takes characters.
	v.focus = gitFieldURL
	v.url.Set("")
	if !s.HandleChar("h") || !s.HandleChar("i") {
		t.Fatal("focused field did not consume chars via HandleChar")
	}
	if v.url.Get() != "hi" {
		t.Fatalf("url = %q, want hi", v.url.Get())
	}
	// A multi-rune key name is swallowed but not inserted.
	before := v.url.Get()
	if !v.handleChar("Shift") || v.url.Get() != before {
		t.Fatal("a key-name should be swallowed but not inserted")
	}
	// Focus with no observable is impossible via fieldObs, but guard the branch:
	v.focus = gitField(999)
	if v.handleChar("z") {
		t.Fatal("an unknown focus field should not consume a char")
	}
}

func TestGitKeyEditing(t *testing.T) {
	s, _, _ := withGit(t)
	v := s.git
	// Closed: not consumed.
	if v.handleKey("Backspace") {
		t.Fatal("closed panel consumed a key")
	}
	v.open = true
	// Escape while open-but-unfocused closes the panel.
	if !s.HandleKeyDown("Escape") || v.open {
		t.Fatal("Escape should close an unfocused panel")
	}

	// Backspace on a focused field.
	v.open = true
	v.focus = gitFieldBranch
	v.branch.Set("main")
	if !v.handleKey("Backspace") || v.branch.Get() != "mai" {
		t.Fatalf("backspace: branch=%q", v.branch.Get())
	}
	// Backspace on an empty field is a no-op.
	v.branch.Set("")
	v.handleKey("Backspace")
	if v.branch.Get() != "" {
		t.Fatalf("backspace on empty = %q", v.branch.Get())
	}
	// Tab advances focus.
	v.focus = gitFieldURL
	if !v.handleKey("Tab") || v.focus != gitFieldBranch {
		t.Fatalf("Tab did not advance focus (focus=%d)", v.focus)
	}
	// Enter also advances (Return synonym too).
	v.handleKey("Enter")
	v.handleKey("Return")
	// Escape while focused defocuses but keeps the panel open.
	v.focus = gitFieldURL
	if !v.handleKey("Escape") || v.focus != gitFieldNone || !v.open {
		t.Fatal("Escape while focused should defocus but keep the panel open")
	}
	// A non-editing key while focused is still swallowed by the modal field.
	v.focus = gitFieldURL
	if !v.handleKey("ArrowLeft") {
		t.Fatal("a focused field should swallow other keys")
	}
	// Open + unfocused + a non-Escape key: not consumed by the field.
	v.focus = gitFieldNone
	if v.handleKey("Backspace") {
		t.Fatal("unfocused field should not consume an editing key")
	}
}

func TestGitDrawEveryPhase(t *testing.T) {
	s, f, _ := withGit(t)
	v := s.git
	buf := make([]byte, testW*testH*4)

	f.files = []string{"main.tex", "a.tex", "b.tex"}
	f.fileData["main.tex"] = "x"
	f.statusOK = true
	f.status = gitStatus{Branch: "main", Ahead: 1, Clean: false, DirtyFile: 1}
	f.log = []GitCommitInfo{{Hash: "abcdef1234", Subject: "seed", Author: "seed"}}
	s.GitClone(nil) // repo open, main.tex loaded, >1 tex → picker, log present

	phases := []struct {
		name  string
		setup func()
	}{
		{"launcher-closed", func() { v.open = false }},
		{"open-with-picker-log", func() { v.open = true; v.focus = gitFieldNone }},
		{"token-focused", func() { v.focus = gitFieldToken; v.token.Set("secret") }},
		{"busy", func() { v.focus = gitFieldNone; v.busy.Set(true) }},
		{"error", func() { v.busy.Set(false); v.errMsg.Set("boom") }},
		{"notice", func() { v.errMsg.Set(""); v.notice.Set("Pushed to origin.") }},
	}
	for _, ph := range phases {
		ph.setup()
		s.Draw(buf) // must not panic; draws the launcher + (when open) the panel
	}
}

// TestGitDrawNarrowClamped exercises the panel-clamp draw path on a small window.
func TestGitDrawNarrowClamped(t *testing.T) {
	s, _, _ := withGit(t)
	s.Resize(360, 400)
	s.git.open = true
	buf := make([]byte, 360*400*4)
	s.Draw(buf)
}

// BootClone opens the sample repository in the workspace as the app starts, and
// shows the sidebar once it lands.
func TestBootCloneOpensTheWorkspace(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"article.tex", "gotex-demo.sty"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}cloned\end{document}`
	// The workspace is present from load (open by default); a reader may still
	// have closed it while the clone was in flight, so BootClone reveals it again.
	if !s.SidebarOpen() {
		t.Fatal("the workspace must start open by default")
	}
	s.SetSidebarOpen(false)

	var gotErr error
	s.BootClone(func(err error) { gotErr = err })
	if gotErr != nil {
		t.Fatalf("boot clone: %v", gotErr)
	}
	if !s.SidebarOpen() {
		t.Error("the workspace must open once the repository lands")
	}
	if got := s.Source(); !strings.Contains(got, "cloned") {
		t.Errorf("the repository's primary .tex was not loaded: %q", got)
	}
	if s.git.config().URL != DefaultRemoteURL {
		t.Errorf("boot clone used %q, want the default remote", s.git.config().URL)
	}
}

// A reader who starts typing while the clone is in flight keeps what they wrote.
// The workspace still fills; only the editor is left alone.
func TestBootCloneKeepsWhatYouWereWriting(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}cloned\end{document}`

	const typed = `\documentclass{article}\begin{document}mine\end{document}`
	// The fake backend completes the clone inside the call, so the "meanwhile"
	// is staged by changing the buffer after BootClone recorded it — which is
	// exactly the state the guard reads.
	s.git.bootBuffer = s.Source()
	s.SetSource(typed)
	s.GitClone(nil)

	if got := s.Source(); got != typed {
		t.Errorf("the reader's text was overwritten:\n got %q\nwant %q", got, typed)
	}
	if len(s.git.files) == 0 {
		t.Error("the workspace must still fill with the repository")
	}
}

// An explicit Clone always loads — the guard is for the boot path only.
func TestExplicitCloneStillLoads(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}cloned\end{document}`
	s.SetSource(`\documentclass{article}\begin{document}mine\end{document}`)
	s.GitClone(nil)
	if got := s.Source(); !strings.Contains(got, "cloned") {
		t.Errorf("an explicit clone must load the repository's file, got %q", got)
	}
}

// BootClone is inert where there is nothing to clone into, or a repository is
// already open.
func TestBootCloneIsInertWhenItShouldBe(t *testing.T) {
	bare := newTestState(t, false) // no backend attached
	bare.SetSidebarOpen(false)     // reader closed it; BootClone must not touch it
	bare.BootClone(nil)
	if bare.SidebarOpen() {
		t.Error("no backend: BootClone must not force the workspace open")
	}

	s, f, _ := withGit(t)
	f.files = []string{"a.tex"}
	f.fileData["a.tex"] = "x"
	s.GitClone(nil) // a repository is now open
	s.sidebar.open = false
	s.BootClone(nil)
	if s.SidebarOpen() {
		t.Error("a repository is already open: boot clone must not run again")
	}
}

// A boot clone must never keep the reader from opening the repository they
// actually asked for: an explicit Clone supersedes one in flight. This is the
// failure that showed up as two browser proofs timing out — their own clone was
// refused while the boot clone held the busy flag.
func TestExplicitCloneSupersedesTheBootClone(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"boot.tex"}
	f.fileData["boot.tex"] = "boot"

	// Stage a boot clone that has NOT completed.
	s.git.busy.Set(true)
	s.git.bootInFlight = true
	before := f.cloneCalls

	s.git.url.Set("https://example.invalid/other.git")
	s.GitClone(nil)
	if f.cloneCalls != before+1 {
		t.Fatalf("an explicit clone must supersede a boot clone in flight: calls %d -> %d", before, f.cloneCalls)
	}
}

// Two explicit clones are a different matter — a double-click on Clone — and the
// second is still refused. That guard predates the boot clone and stays.
func TestSecondExplicitCloneStillRefused(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"a.tex"}
	f.fileData["a.tex"] = "x"

	s.git.busy.Set(true)
	s.git.bootInFlight = false
	before := f.cloneCalls
	s.GitClone(nil)
	if f.cloneCalls != before {
		t.Fatalf("a second explicit clone must be refused: calls %d -> %d", before, f.cloneCalls)
	}
}

// A boot clone that lands after an explicit one must not overwrite what the
// explicit one opened.
func TestLateBootCloneResultIsDropped(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"explicit.tex"}
	f.fileData["explicit.tex"] = `\documentclass{article}\begin{document}explicit\end{document}`

	// A boot clone is in flight; capture its generation the way its callback did.
	s.git.cloneGen++
	staleGen := s.git.cloneGen
	s.git.busy.Set(true)
	s.git.bootInFlight = true

	s.GitClone(nil) // supersedes, and completes inside the fake backend
	if got := s.Source(); !strings.Contains(got, "explicit") {
		t.Fatalf("the explicit clone did not load: %q", got)
	}
	if staleGen == s.git.cloneGen {
		t.Fatal("the explicit clone must have taken a new generation")
	}
}

// While the git client is on its way the topZone names it, so a reader watching
// a workspace that has not filled yet knows what is still coming. The playground
// is interactive and typesetting throughout — only the git panel waits.
func TestTopZoneNamesTheAssetItIsFetching(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"a.tex"}
	f.fileData["a.tex"] = "x"

	if got := s.TopZoneStatusText(); got != topZoneStatus {
		t.Fatalf("at rest the band reads %q", got)
	}

	// A backend that is a prewarmer (the real worker one) announces its wasm for
	// the duration of the clone; the fake completes inside the call, so the
	// announcement is observed from within the backend.
	var during string
	f.onClone = func() { during = s.TopZoneStatusText() }
	s.GitClone(nil)

	if !strings.Contains(during, "git-worker.wasm") {
		t.Errorf("the band did not name the asset while fetching it: %q", during)
	}
	if got := s.TopZoneStatusText(); got != topZoneStatus {
		t.Errorf("the announcement must clear when the clone lands: %q", got)
	}
	if s.AssetLoading() != "" {
		t.Errorf("AssetLoading not cleared: %q", s.AssetLoading())
	}
}

// A document can load a .sty that sits beside it in the repository. Without the
// workspace resolver the engine sees only its embedded set, \usepackage silently
// does nothing, and every command that package defined becomes undefined — which
// swallows its argument, so the text those commands wrapped disappears from the
// page. The sample article lost 112 characters that way.
func TestWorkspaceResolvesItsOwnPackage(t *testing.T) {
	s, f, _ := withGit(t)
	f.files = []string{"paper.tex", "local-demo.sty"}
	f.fileData["local-demo.sty"] = `\ProvidesPackage{local-demo}` + "\n" +
		`\newcommand{\keyword}[1]{\textbf{#1}}`
	f.fileData["paper.tex"] = `\documentclass{article}\usepackage{local-demo}` +
		`\begin{document}a \keyword{resolved} word\end{document}`
	s.GitClone(nil)

	res := compileLaTeX(s.git.compileSource(), s.theme, s.git.resolveWorkspace())
	if n := len(res.diag.Skipped); n != 0 {
		t.Errorf("the package did not resolve: %v", res.diag.Skipped)
	}
	// The wrapped word must survive: an undefined \keyword eats its argument.
	var text strings.Builder
	for _, svg := range res.svgs {
		text.WriteString(svg)
	}
	if !strings.Contains(text.String(), "<text") {
		t.Fatal("no text layer to read")
	}
	if got := textOf(res); !strings.Contains(got, "resolved") {
		t.Errorf("the argument of a resolved command was swallowed: %q", got)
	}
}

// With no repository open there is no resolver, which is the engine's own
// "disk and embedded set only" default.
func TestNoWorkspaceNoResolver(t *testing.T) {
	s := newTestState(t, false)
	if s.git.resolveWorkspace() != nil {
		t.Error("no repository is open: there is nothing to resolve from")
	}
}

// textOf reads what a compiled result's pages say, out of their text layers.
func textOf(res compileResult) string {
	var out strings.Builder
	for _, svg := range res.svgs {
		rest := svg
		for {
			i := strings.Index(rest, "<text")
			if i < 0 {
				break
			}
			rest = rest[i:]
			j := strings.Index(rest, "</text>")
			if j < 0 {
				break
			}
			out.WriteString(dropTags(rest[:j]))
			rest = rest[j+len("</text>"):]
		}
	}
	return out.String()
}

// dropTags leaves the character data between an SVG fragment's tags.
func dropTags(s string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			return out.String()
		}
		s = s[i+j+1:]
	}
}
