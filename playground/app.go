// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package playground is the go-tex WebAssembly LaTeX playground rendered as a
// single go-widgets/toolkit canvas application: a toolbar (syntax colour-scheme
// picker, minimap toggle), a horizontal split of a CodeEditor (line numbers,
// LaTeX syntax colours, current-line highlight) with an optional code minimap on
// the left and, on the right, a small Rendered│Log tab strip over either the
// shared toolkit.PagedView showing the pure-Go gotex render (continuous /
// paginated modes, zoom, and full wheel + keyboard page navigation, all owned by
// the widget) or a diagnostics Log, and a status bar (caret position, encoding,
// page count, issue count).
//
// The whole View + logic lives here as a plain, tagless package so a native
// `go test` exercises construction, layout, compile, rasterize, event dispatch
// (including pointer drag: divider resize + scrollbar thumbs) and draw against an
// RGBA buffer via go-widgets/painter — no browser needed. The js/wasm canvas
// driver (cmd/playground-wasm) is the only build-tagged file; it forwards DOM
// input (mousedown/move/up, wheel, keys) into these handlers, applies the HiDPI
// device-pixel scale, and blits Draw's buffer.
package playground

import (
	"image"
	"strconv"
	"strings"

	"github.com/go-opentype/fonts/jetbrainsmono"
	engine "github.com/go-tex/engine"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/toolkit/rougelex"
)

// editorFontTTF is the monospace face the CodeEditor renders with. A code editor
// MUST be monospace: the toolkit's caret placement and click-to-column mapping
// both step by ONE glyph advance, so the toolkit's default proportional face
// (Atkinson Hyperlegible) makes the caret and clicks drift along wide glyphs
// (a click over column 6 could land on column 13). It is a package var so a test
// can swap a bad blob to exercise the font-load error branch.
var editorFontTTF = jetbrainsmono.TTF

// SampleLaTeX is the document the playground opens with — the same article the
// legacy textarea playground shipped, so the two render identically.
const SampleLaTeX = `\documentclass{article}

\title{Live \LaTeX{} in WebAssembly}
\author{the go-tex authors}
\date{2026}

\begin{document}
\maketitle

\section{Introduction}
This page is typeset by the genuine \texttt{article.cls}, executed as
WebAssembly directly in your browser --- no TeX Live, no server.

\subsection{What runs here}
The pure-Go TeX engine performs Knuth--Plass line breaking and lays out
the document exactly as a real \LaTeX{} run would.

\section{Features on display}
\begin{itemize}
  \item The real title block from \texttt{maketitle}.
  \item Numbered section and subsection headings.
  \item An inline equation: the quadratic root
        $x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}$.
\end{itemize}

\end{document}`

// baseFontPx is the logical text size; the HiDPI driver multiplies it by the
// device-pixel ratio via SetupText so glyphs render crisp on a Retina panel.
const baseFontPx = 16

// buildInfoSegment is the index of the status-bar segment that carries the build
// stamp (SHA + UTC time). It is the LAST segment, so the toolkit Statusbar
// expands it to fill the bar's right-hand remainder; updateStatus owns segments
// 0..buildInfoSegment-1 and never overwrites it.
const buildInfoSegment = 4

// defaultBuildInfo is the build stamp shown when no ldflags value was injected —
// i.e. a native `go test`, a `go run`, or a wasm build that skipped the deploy
// -X flags. It keeps the segment honest ("this is an un-stamped/dev binary")
// rather than blank or falsely current.
//
// The separator is parentheses, not a middot: the toolkit's 5×7 bitmap font is
// ASCII-only (byte-indexed font5x7), so a "·" renders as a blank gap. Parens
// read cleanly against the hyphenated timestamp with characters the font has.
const defaultBuildInfo = "dev (unknown)"

// formatBuildInfo renders the compact status-bar stamp "<version> (<buildTime>)"
// (e.g. "4d63d59 (2026-08-25 13:20 UTC)"). An empty field falls back to its dev
// placeholder so a half-injected build (SHA but no time, or vice-versa) still
// reads sensibly. The separator is parentheses rather than a middot because the
// toolkit's ASCII-only 5×7 bitmap font renders a "·" as blank (see defaultBuildInfo).
func formatBuildInfo(version, buildTime string) string {
	if version == "" {
		version = "dev"
	}
	if buildTime == "" {
		buildTime = "unknown"
	}
	return version + " (" + buildTime + ")"
}

// pressKind records what the current pointer press captured, so a following
// move/release is routed to the same target (the fix for a divider/scrollbar
// that could not be dragged because move/up were discarded).
const (
	pressNone = iota
	pressToolbar
	pressDivider
	pressEditor
	pressMinimap
	pressRight   // the render ScrollView or the Log view (paned.Second)
	pressCollab  // the modal Collaborate panel captured the press
	pressGit     // the modal Remote-Git panel captured the press
	pressSidebar // the workspace sidebar (left column) captured the press
	pressFind    // the find-and-replace modal captured the press
)

// SetupText installs the toolkit's anti-aliased text and metric scale for a
// device-pixel ratio: text renders at baseFontPx*scale and every box metric is
// scaled to match, so the UI is crisp on a HiDPI panel and byte-identical at
// scale 1. The driver calls it (with window.devicePixelRatio) before building
// the State; tests call SetupText(1).
func SetupText(scale float64) {
	if scale <= 0 {
		scale = 1
	}
	toolkit.SetMetricScale(scale)
	_ = toolkit.UseOpenTypeTextSize(int(float64(baseFontPx)*scale + 0.5))
}

// State is the whole playground: the widget tree, the current theme, the last
// compile result and host bookkeeping. Created with NewState, driven through its
// Handle* / Draw methods.
type State struct {
	w, h  int
	theme *toolkit.Theme
	dark  bool

	editor     *toolkit.CodeEditor
	hl         *rougelex.Highlighter
	renderView *toolkit.PagedView
	logView    *toolkit.LogView
	rightPane  *rightPane
	minimap    *toolkit.CodeMinimap
	paned      *toolkit.Paned
	status     *toolkit.Statusbar

	schemePicker   *toolkit.DropDown
	iconPackPicker *toolkit.DropDown
	minimapBtn     *toolkit.Button

	// buildInfo is the immutable "which build is this" string shown in the last
	// status-bar segment: a git short SHA + a UTC build timestamp, baked into the
	// wasm at deploy time via -ldflags and pushed in once at startup by the
	// wasm shell (SetBuildInfo). A native/dev build leaves it at defaultBuildInfo
	// ("dev · unknown"), so the segment is honest about an un-stamped binary.
	buildInfo string

	// Chrome grounds that were once hand-filled rectangles, now persistent
	// Backdrop widgets (so they carry theming/HiDPI like every other element and
	// pass bricolint): the toolbar surface, its bottom hairline, and the
	// compile-error band drawn by drawError.
	toolbarBg   *toolkit.Backdrop
	toolbarRule *toolkit.Backdrop
	errorBand   *toolkit.Backdrop

	// collab is the live collaborative-editing affordance (Collaborate launcher +
	// modal panel + WebRTC session). Self-contained in collab.go / collab_js.go;
	// the fields below are the only app.go hooks it needs.
	collab *collabView

	// git is the remote-git affordance (Git launcher + modal panel + browsergit
	// clone/commit/push). Self-contained in git.go / git_js.go, the same shape as
	// collab; the hooks below (init, draw, click, char, key) are all app.go needs.
	git *gitView

	// sidebar is the Git workspace sidebar: a toggleable left column with the file
	// tree, git command buttons and a commit timeline (sidebar.go). sidebarBtn is
	// its toolbar toggle. When open, layout() reserves the column's width on the
	// left and the editor+render body fills the rest.
	sidebar    *sidebar
	sidebarBtn *toolkit.Button

	// fr is the regex find-and-replace affordance: a MODAL window (toolkit
	// v0.254.0 NewSearchModal, v0.253.0 RichEditor highlights) whose top input bar
	// is the regex query, driven over WHICHEVER editor is active — the Source
	// CodeEditor or the WYSIWYG RichEditor. Self-contained in findreplace.go;
	// findBtn is its toolbar toggle (⌘F/Ctrl+F is the keyboard peer).
	fr      *findReplace
	findBtn *toolkit.Button

	// clip is the toolkit-wide clipboard the editor's copy/cut/paste go through;
	// installed process-wide in NewState. Its onWrite hook lets the wasm host push
	// copies to the real OS clipboard (navigator.clipboard).
	clip *appClipboard

	// topZone / bottomZone are the two chrome bands that used to be HTML around the
	// canvas (the ".pg-bar" status line and the ".pg-note" + "<footer>"), now part
	// of the toolkit scene so the app fills the whole viewport below the host page's
	// blue header. See zones.go. layout() reserves their heights, shrinking the
	// editor+render body between them.
	topZone    *topZone
	bottomZone *bottomZone

	// navigate is the host hook a bottomZone link click drives (the wasm driver
	// wires it to window.location.href = url); nil on a native build, so a link
	// click is a no-op there. siteRoot is the "Back to go-tex" target, defaulting
	// to defaultSiteRoot and overridden by the js layer with location.origin + "/".
	navigate func(url string)
	siteRoot string

	showMinimap bool

	// last compile output.
	errText    string
	pages      int // engine (logical) page count
	drawnPages int // pages rasterized + fed to the render pane
	diag       engine.Diagnostics

	// chrome heights (device pixels), recomputed each layout at the active scale.
	// assetLoading names the wasm the app is fetching in the background, or is
	// empty. The playground is interactive long before every asset has arrived —
	// the git client is a separate 4.2 MB (gzip) binary that only downloads when
	// a repository is opened — and a reader should be told what is still coming
	// rather than left to wonder why a panel is not ready yet.
	assetLoading string

	// topZoneH / bottomZoneH are the two moved-in HTML bands' reserved heights (the
	// bottom one grows with its wrapped prose); the toolbar + body + status sit
	// between them.
	toolbarH, statusH     int
	topZoneH, bottomZoneH int

	// pointer drag capture.
	pressKind int

	// monoScale is the metric scale the editor's monospace font was last built
	// for, so applyEditorFont rebuilds it only on a real HiDPI scale change.
	monoScale float64

	// keyboard selection anchor: selecting is true while a Shift+navigation
	// selection is in progress; the anchor is where it began.
	selecting                   bool
	selAnchorLine, selAnchorCol int

	// source↔render linking. lineMaps is the per-rendered-page source-line
	// Y-band table (parallel to the PagedView's pages), rebuilt every Compile.
	// lastCaretLine remembers the caret's line so a caret MOVE (not every edit)
	// triggers a render scroll. syncing guards the linking against feedback: it is
	// raised while a render-click drives the caret, so the caret-move that results
	// does NOT re-scroll the render underneath the click.
	lineMaps []pageLineMap
	// svgs is the per-rendered-page SVG the host draws, parallel to the render
	// pane's pages. The pane paints each page's paper, shadow and border; this is
	// what goes on top — searchable, selectable and crisp at any zoom, because
	// the browser renders it rather than a bitmap standing in for it.
	svgs []string
	// pageSizes is each page's natural (un-zoomed) size in device pixels, the
	// space the host reports its line-band measurements in.
	pageSizes     []image.Point
	lastCaretLine int
	syncing       bool

	dirty          bool
	pendingCompile bool

	// loadingSource is raised while SetSource programmatically replaces the
	// buffer (SetText), so the Text() edit subscriber skips that push — a restore
	// is not a user edit and drives its own compile.
	loadingSource bool

	// OnCompileNeeded schedules a debounced Compile after an edit; OnEdit
	// persists the buffer independent of the compile path. Nil in tests.
	OnCompileNeeded func()
	OnEdit          func(text string)

	// now returns the host-formatted timestamp stamped on each compile's Log
	// entries. The toolkit LogView never reads the clock itself, so the host
	// formats the time (the wasm driver wires this to the browser's
	// new Date().toLocaleTimeString()); defaults to a Go wall-clock format so a
	// native build/test still gets non-empty timestamps.
	now func() string

	// wys is the WYSIWYG multi-format mode (format registry + a Source│WYSIWYG tab
	// strip atop the editor pane + a RichEditor shown on the WYSIWYG tab). Lazily
	// built via s.wysiwyg(); its whole implementation, and every hook this file
	// calls into it, lives in wysiwyg.go so this mode stays an isolated, additive
	// feature.
	wys *wysiwyg
}

// NewState builds the playground at w×h DEVICE pixels, dark or light, compiles
// the sample document once and returns the ready scene. The caller installs the
// text/scale first via SetupText.
func NewState(w, h int, dark bool) *State {
	s := &State{w: w, h: h, dark: dark, showMinimap: true}
	s.now = defaultTimestamp // host overrides via SetTimeProvider (browser locale time)
	s.setTheme(dark)

	s.hl = rougelex.New()
	s.editor = toolkit.NewCodeEditor(SampleLaTeX)
	s.editor.Language = "latex"
	s.editor.Syntax = s.hl
	s.editor.Focused().Set(true)
	s.installCompletion() // LaTeX autocompletion (see latexcomplete.go)
	s.applyEditorFont()   // monospace so the caret + clicks land on exact columns
	// TextView is MVVM in v0.214: an edit publishes onto the Text() Observable
	// (the OnChange successor) rather than firing a callback, so react by
	// subscribing to it. Subscribe fires on every buffer change — including the
	// programmatic SetText behind SetSource, which must NOT count as a user edit
	// (SetSource restores a persisted document and drives its own compile), so
	// loadingSource gates that one path out. The editor lives for the whole app,
	// so the unsubscribe handle is intentionally dropped.
	s.editor.Text().Subscribe(func(string) {
		if s.loadingSource {
			return
		}
		if s.OnEdit != nil {
			s.OnEdit(s.editor.Text().Get())
		}
		s.pendingCompile = true
		s.dirty = true
		if s.OnCompileNeeded != nil {
			s.OnCompileNeeded()
		}
	})

	s.renderView = toolkit.NewPagedView(nil)
	// Fill the pane by default: a page at the fixed 100% left most of the render
	// pane's width empty. Sticky fit-to-width scales each page to the pane and
	// re-fits on resize / a new document, until the reader zooms manually.
	s.renderView.SetFitWidth(true)
	s.logView = toolkit.NewLogView()
	s.logView.MaxEntries = 500
	s.rightPane = newRightPane(s.renderView, s.logView)
	s.minimap = toolkit.NewCodeMinimap()
	// The overview is a scroll thumbnail: a click/drag maps to a buffer line and
	// scrolls the editor there (mirrors the old minimapScrollTo).
	s.minimap.OnScrollToLine = func(line int) {
		s.editor.ScrollLine().Set(line)
		s.dirty = true
	}
	s.paned = toolkit.NewHPaned(s.editor, s.rightPane)

	s.schemePicker = toolkit.NewDropDown(rougelex.ThemeNames(), 0)
	// DropDown is MVVM in v0.196: react to a selection change by subscribing to
	// its Selected observable (Subscribe does not fire on registration, so the
	// initial scheme is applied by the first compile, not here). The picker lives
	// for the whole app, so the unsubscribe handle is intentionally dropped.
	s.schemePicker.Selected().Subscribe(func(idx int) { s.applyScheme(idx) })
	// The file-type icon pack the workspace tree draws from (Seti UI / Material),
	// chosen like the syntax theme. A change rebuilds the tree so every row's icon
	// re-renders from the new pack.
	s.iconPackPicker = toolkit.NewDropDown(iconPackNames(), 0)
	s.iconPackPicker.Selected().Subscribe(func(int) {
		s.sidebar.forceRebuild()
		s.dirty = true
	})
	s.minimapBtn = toolkit.NewButton("Minimap", func() {
		s.showMinimap = !s.showMinimap
		s.layout()
		s.dirty = true
	})

	// Persistent chrome grounds (see the struct fields): built once, positioned
	// and drawn each frame, replacing hand-filled rectangles.
	s.toolbarBg = &toolkit.Backdrop{}
	s.toolbarRule = &toolkit.Backdrop{}
	s.errorBand = &toolkit.Backdrop{}

	// Segments 0..3 are the live editor read-out (caret, encoding, page count,
	// issue count — all refreshed by updateStatus). Segment buildInfoSegment is the
	// immutable build stamp: it is the LAST segment, set once here to the dev
	// default and overwritten once by SetBuildInfo when the wasm shell hands over
	// the ldflags-injected SHA + timestamp. updateStatus never touches it.
	s.buildInfo = defaultBuildInfo
	s.status = toolkit.NewStatusbar([]string{"Ln 1, Col 1", "UTF-8", "0 pages", "", s.buildInfo})
	s.collab = newCollabView(s) // Collaborate affordance (see collab.go)
	s.git = newGitView(s)       // Remote-git affordance (see git.go)
	s.sidebar = newSidebar(s)   // Git workspace sidebar, left column (see sidebar.go)
	// The toolbar toggle for the workspace sidebar. OPEN by default (see
	// newSidebar) so the workspace is present on load; this toggle lets the user
	// close it to reclaim the full canvas width.
	s.sidebarBtn = toolkit.NewButton("Workspace", func() {
		s.sidebar.toggle()
		s.layout()
		s.dirty = true
	})

	// Regex find-and-replace over the active editor (findreplace.go). The toolbar
	// button toggles the modal; ⌘F/Ctrl+F is its keyboard peer (wired in the wasm
	// driver to ToggleFindReplace).
	s.fr = newFindReplace(s)
	s.findBtn = toolkit.NewButton("Find", func() {
		s.fr.toggle()
		s.layout()
		s.dirty = true
	})

	// The two chrome bands moved in from the host HTML (see zones.go). siteRoot is
	// seeded to the canonical default before the first layout (bottomZone.measure
	// reads it); the js layer overrides it with the live origin at startup.
	s.siteRoot = defaultSiteRoot
	s.topZone = newTopZone(s)
	s.bottomZone = newBottomZone(s)

	// Route the toolkit-wide clipboard through this State so the editor's
	// copy/cut/paste reach the host (and, on wasm, the OS clipboard).
	s.clip = &appClipboard{}
	toolkit.SetClipboard(s.clip)

	s.layout()
	// No initial Compile() here: it would stamp the boot compile's Log entries
	// before the host installs its clock (SetTimeProvider), giving the first
	// entry a different timestamp format from the rest. Mark it pending instead;
	// the host fires the first compile via CompilePending() once its time
	// provider (and everything else) is wired.
	s.pendingCompile = true
	s.lastCaretLine = s.editor.CursorLine().Get() // seed the caret-move tracker
	return s
}

// appClipboard is the process-wide toolkit Clipboard for the playground: it
// holds the last-copied text in memory (so the synchronous ClipboardText the
// toolkit calls always has a value) and, when set, mirrors every write to the
// host via onWrite — the wasm driver points that at navigator.clipboard.writeText
// so a copy reaches the real OS clipboard.
type appClipboard struct {
	text    string
	onWrite func(string)
}

func (c *appClipboard) ClipboardText() string { return c.text }

func (c *appClipboard) SetClipboardText(s string) {
	c.text = s
	if c.onWrite != nil {
		c.onWrite(s)
	}
}

// SetClipboardWriter installs the host hook that mirrors every copy/cut to the OS
// clipboard (the wasm driver wires it to navigator.clipboard.writeText).
func (s *State) SetClipboardWriter(w func(string)) { s.clip.onWrite = w }

// SetNavigate installs the host navigation hook a bottomZone link click drives.
// The wasm driver wires it to window.location.href = url; a native build leaves
// it nil, so a link click is a no-op there (the whole affordance stays testable
// off a browser).
func (s *State) SetNavigate(f func(url string)) { s.navigate = f }

// SetSiteRoot sets the "Back to go-tex" link's target — the deployment's own site
// root. The js layer passes location.origin + "/" at startup so the link points
// at the exact site the app is served from; an empty string is ignored, keeping
// the built-in default ([defaultSiteRoot]).
func (s *State) SetSiteRoot(base string) {
	if base != "" {
		s.siteRoot = base
		s.layout() // the Back link's rect + target are recomputed for the new root
		s.dirty = true
	}
}

// doNavigate drives the host navigation hook for a link click (no-op on a native
// build, where navigate is nil) and marks the scene dirty.
func (s *State) doNavigate(url string) {
	if s.navigate != nil {
		s.navigate(url)
	}
	s.dirty = true
}

// bodyTop is the surface Y where the editor+render body begins: below the topZone
// band and the toolbar. Every overlay that anchors to the body (the Collaborate /
// Git panels + scrims) reads it so it follows the topZone shift.
func (s *State) bodyTop() int { return s.topZoneH + s.toolbarH }

// SetTimeProvider installs the host clock the compile Log stamps its entries
// with. The wasm driver wires it to the browser's
// new Date().toLocaleTimeString() so entries carry the viewer's local time; a
// nil hook is ignored (the Go wall-clock default stays in place).
func (s *State) SetTimeProvider(now func() string) {
	if now != nil {
		s.now = now
	}
}

// HandleCopy copies the editor selection to the clipboard. No-op (returns false)
// when the editor is unfocused.
func (s *State) HandleCopy() bool {
	if !s.editor.Focused().Get() {
		return false
	}
	s.editor.CopySelection()
	s.dirty = true
	return true
}

// HandleCut cuts the editor selection to the clipboard (an edit, so it schedules
// a recompile via the editor's OnChange). No-op when unfocused.
func (s *State) HandleCut() bool {
	if !s.editor.Focused().Get() {
		return false
	}
	s.editor.CutSelection()
	s.updateStatus()
	s.dirty = true
	return true
}

// HandlePaste inserts text at the caret (replacing any selection). The host reads
// the OS clipboard asynchronously and passes the text here. A paste into a focused
// Collaborate panel field (the signalling-blob paste field, the ICE or the name
// field) goes there instead, so ⌘V/Ctrl+V drops the invitation/reply into the
// visible field — the immediate proof it was taken. No-op when nothing is focused.
func (s *State) HandlePaste(text string) bool {
	if s.collab.handlePaste(text) { // paste into the focused Collaborate field
		s.dirty = true
		return true
	}
	if !s.editor.Focused().Get() {
		return false
	}
	s.editor.Paste(text)
	s.updateStatus()
	s.dirty = true
	return true
}

// HandleSelectAll selects the whole buffer. No-op when unfocused.
func (s *State) HandleSelectAll() bool {
	if !s.editor.Focused().Get() {
		return false
	}
	s.editor.SelectAll()
	s.dirty = true
	return true
}

// applyScheme switches the syntax colour theme. A fresh highlighter instance is
// installed so the CodeEditor's span cache (keyed by highlighter identity)
// invalidates and re-lexes with the new palette. The named schemes return a
// fixed Palette from rougelex.PaletteByName; the "Default" scheme returns the
// zero Palette, which keeps the highlighter theme-derived.
func (s *State) applyScheme(idx int) {
	names := rougelex.ThemeNames()
	if idx < 0 || idx >= len(names) {
		return
	}
	pal, _ := rougelex.PaletteByName(names[idx])
	hl := &rougelex.Highlighter{Palette: pal}
	s.hl = hl
	s.editor.Syntax = hl
	s.dirty = true
}

// showLog reports whether the diagnostics Log tab is the active right-pane tab.
func (s *State) showLog() bool { return s.rightPane.isLog() }

// toggleLog swaps the active right-pane tab between the render and the
// diagnostics Log.
func (s *State) toggleLog() {
	if s.rightPane.isLog() {
		s.rightPane.setActive(tabRender)
	} else {
		s.rightPane.setActive(tabLog)
	}
	s.dirty = true
}

func (s *State) setTheme(dark bool) {
	s.dark = dark
	if dark {
		s.theme = toolkit.DefaultDark()
	} else {
		s.theme = toolkit.DefaultLight()
	}
}

// SetTheme switches the palette and recompiles so the render pane's paper/ink
// follow the new theme.
func (s *State) SetTheme(dark bool) {
	s.setTheme(dark)
	s.Compile()
	s.dirty = true
}

// Resize re-lays out to a new surface size and repaints.
func (s *State) Resize(w, h int) {
	s.w, s.h = w, h
	s.applyEditorFont() // rebuild the editor's mono font if the HiDPI scale changed
	s.layout()
	s.dirty = true
}

// applyEditorFont installs a monospace face on the CodeEditor sized to the active
// metric scale (rebuilding only when the scale changed). A load failure leaves
// the editor on the inherited proportional font rather than crashing.
func (s *State) applyEditorFont() {
	scale := toolkit.MetricScale()
	if s.monoScale == scale && s.editor.Font != nil {
		return
	}
	size := int(float64(baseFontPx)*scale + 0.5)
	f, err := toolkit.NewTrueTypeFont(editorFontTTF, size)
	if err != nil {
		return
	}
	s.editor.SetFont(f)
	s.monoScale = scale
}

// Size returns the current surface size.
func (s *State) Size() (int, int) { return s.w, s.h }

// Source returns the current editor buffer.
func (s *State) Source() string { return s.editor.Text().Get() }

// SetSource replaces the editor buffer (restoring a persisted document), resets
// the caret and recompiles. It does NOT fire OnEdit.
func (s *State) SetSource(text string) {
	// SetText publishes onto Text(), which would otherwise wake the edit
	// subscriber (OnEdit + a debounced compile); loadingSource gates that so a
	// restore drives only the explicit Compile below.
	s.loadingSource = true
	s.editor.SetText(text)
	s.loadingSource = false
	s.Compile()
	s.dirty = true
}

// SetSourceCursor replaces the editor buffer AND restores a saved caret + scroll
// position, without firing OnEdit. It is how switching back to a file re-opens
// its independent edit buffer (git.go's openFile) so the unsaved edits AND the
// place the user left off both come back. Like SetSource it recompiles.
func (s *State) SetSourceCursor(text string, line, col, scroll int) {
	s.loadingSource = true
	s.editor.SetText(text) // parks the caret at (0,0); restore it below
	s.editor.CursorLine().Set(line)
	s.editor.CursorCol().Set(col)
	s.editor.ScrollLine().Set(scroll)
	s.loadingSource = false
	s.Compile()
	s.dirty = true
}

// ToggleFindReplace shows or hides the regex find-and-replace modal over the
// active editor and relays out. It is the host hook the wasm driver wires to the
// ⌘F/Ctrl+F keydown (the toolbar Find button drives the same fr.toggle). It
// always reports true so the driver re-renders after the toggle.
func (s *State) ToggleFindReplace() bool {
	s.fr.toggle()
	s.layout()
	s.dirty = true
	return true
}

// FindVisible reports whether the find modal is open — the wasm driver reads it to
// decide whether a Shift+Enter should reach the modal (previous match) rather than
// the editor.
func (s *State) FindVisible() bool { return s.fr.visible() }

// Dirty/ClearDirty/Theme accessors.
func (s *State) Dirty() bool           { return s.dirty }
func (s *State) ClearDirty()           { s.dirty = false }
func (s *State) Theme() *toolkit.Theme { return s.theme }

// Read-only introspection for the host/verification harness: the editor pane
// width, whether the Log is shown, the active tab index, the render zoom
// percentage (read from the PagedView's Zoom observable) and the selected colour
// scheme index. They let a headless test assert real state changes after a
// pointer interaction.
func (s *State) EditorWidth() int    { return s.editor.Bounds().W }
func (s *State) ShowLog() bool       { return s.showLog() }
func (s *State) ActiveTab() int      { return s.rightPane.activeTab() }
func (s *State) ZoomPercent() int    { return s.renderView.Zoom().Get() }
func (s *State) SelectedScheme() int { return s.schemePicker.Selected().Get() }

// iconPackIdx is the index into iconPacks of the workspace tree's chosen icon
// pack (clamped defensively, since the picker owns the value).
func (s *State) iconPackIdx() int {
	i := s.iconPackPicker.Selected().Get()
	if i < 0 || i >= len(iconPacks) {
		return 0
	}
	return i
}

// PageCount is the engine's logical page count; DrawnPages the number actually
// rasterized and fed to the render pane. They let a headless test assert a
// multi-page document reached the viewer.
func (s *State) PageCount() int  { return s.pages }
func (s *State) DrawnPages() int { return s.drawnPages }

// RenderMode / RenderCurrentPage / RenderVisiblePages report the render pane's
// continuous-vs-paginated mode, the current 1-based page and how many pages are
// shown at once (1 paginated, all continuous) — all read from the PagedView's
// MVVM observables. CursorLine/CursorCol expose the editor caret for
// click-accuracy assertions. HasSelection / SelectionText expose the current
// text selection.
func (s *State) RenderMode() int        { return int(s.renderView.Mode().Get()) }
func (s *State) RenderCurrentPage() int { return s.renderView.CurrentPage().Get() }
func (s *State) RenderVisiblePages() int {
	if s.renderView.Mode().Get() == toolkit.PagedPaginated {
		if s.renderView.PageCount() == 0 {
			return 0
		}
		return 1
	}
	return s.renderView.PageCount()
}
func (s *State) CursorLine() int       { return s.editor.CursorLine().Get() }
func (s *State) CursorCol() int        { return s.editor.CursorCol().Get() }
func (s *State) HasSelection() bool    { return s.editor.HasSelection() }
func (s *State) SelectionText() string { return s.editor.SelectionText() }

// RenderFocused reports whether the render pane holds keyboard focus (so nav keys
// drive the page viewer, not the editor). Host introspection.
func (s *State) RenderFocused() bool { return s.renderFocused() }

// CaretPixel returns the DEVICE-pixel point just inside the cell of the given
// 0-based (line, col) caret position — so a headless harness can click there and
// assert the caret round-trips exactly (proving the click→column mapping is
// accurate). It DELEGATES to the toolkit's [toolkit.TextView.CaretPixel]
// (promoted through the embedded *TextView) so the gutter/advance/line-pitch
// geometry lives in exactly one place: duplicating it here drifted the moment the
// gutter padding widened. CaretPixel returns the cell's top-left, so a couple of
// in-cell pixels (one across, half a glyph down) land the click squarely inside
// the cell for caretAt to map back.
func (s *State) CaretPixel(line, col int) (int, int) {
	x, y := s.editor.CaretPixel(line, col)
	return x + 1, y + s.editor.EffectiveFont().Height()/2
}

// Find-and-replace host introspection for the headless proof: the modal's count
// state and a device-pixel point inside each match's highlight band, so the
// browser harness can read the count AND sample the canvas to prove the
// highlights land on the matches. They read the host's own match state (and the
// ACTIVE editor's highlight geometry), so they report on whichever editor —
// Source or WYSIWYG — the search is targeting.
func (s *State) FindTotal() int        { return len(s.fr.matches) }
func (s *State) FindCurrent() int      { return s.fr.current }
func (s *State) FindCountText() string { return s.fr.countText() }
func (s *State) FindInvalid() bool     { return s.fr.invalid }

// FindMatchPoints returns a device-pixel point just inside each current match's
// highlight band (its start cell), in the active editor's match order.
func (s *State) FindMatchPoints() [][2]int { return s.fr.target().matchPoints() }

// LogEntryCount returns how many entries the diagnostics Log has accumulated
// (proving the history builds up across compiles, newest at the bottom, rather
// than being cleared each compile). Host introspection for the headless harness.
func (s *State) LogEntryCount() int { return s.logView.Len() }

// DividerX is the surface x of the resize grip's leading edge.
func (s *State) DividerX() int { return s.paned.Bounds().X + s.paned.Position().Get() }

// DebugRects returns the DEVICE-pixel [x,y,w,h] rectangles of the interactive
// targets a headless verification harness needs to click precisely: the colour
// scheme picker and its open popover, the two right-pane tabs, the whole render
// pane (the PagedView, which owns its own toolbar + scroll — the harness drives
// paging with real wheel/key events over this rect) and the render content area
// below the PagedView's toolbar strip. It is host-facing introspection only.
func (s *State) DebugRects() map[string][4]int {
	rect := func(r toolkit.Rect) [4]int { return [4]int{r.X, r.Y, r.W, r.H} }
	return map[string][4]int{
		"picker":        rect(s.schemePicker.Bounds()),
		"popover":       rect(s.schemePicker.PopoverBounds()),
		"renderTab":     rect(s.rightPane.tabRect(tabRender)),
		"logTab":        rect(s.rightPane.tabRect(tabLog)),
		"renderPane":    rect(s.renderView.Bounds()),
		"renderContent": rect(s.renderContentRect()),
		"sourceTab":     s.EditorTabRect(tabSource),
		"wysiwygTab":    s.EditorTabRect(tabWysiwyg),
		// The find-and-replace panel and the title strip a drag must be started
		// on. Zero rectangles while the modal is closed.
		"findPanel": rect(s.findPanelRect()),
		"findTitle": rect(s.findTitleRect()),
	}
}

// findPanelRect is the find-and-replace panel's device rectangle, or the zero
// rectangle when the modal is closed.
func (s *State) findPanelRect() toolkit.Rect {
	if s.fr == nil || !s.fr.shown {
		return toolkit.Rect{}
	}
	return s.fr.modal.Panel.Bounds()
}

// findTitleRect is the panel's title strip: the band a drag must start on, left
// of the close button. Zero while the modal is closed.
func (s *State) findTitleRect() toolkit.Rect {
	p := s.findPanelRect()
	if p.W == 0 {
		return toolkit.Rect{}
	}
	h := toolkit.Scaled(toolkit.DialogTitleH)
	if h > p.H {
		h = p.H
	}
	return toolkit.Rect{X: p.X, Y: p.Y, W: p.W - h, H: h}
}

// renderContentRect is the render pane area BELOW the PagedView's toolbar strip:
// where the compile-error overlay is painted. The PagedView reserves a
// fixed-height toolbar (toolkit.Scaled(30)) at the top of its bounds.
func (s *State) renderContentRect() toolkit.Rect {
	r := s.renderView.Bounds()
	tb := toolkit.Scaled(30)
	if tb > r.H {
		tb = r.H
	}
	return toolkit.Rect{X: r.X, Y: r.Y + tb, W: r.W, H: r.H - tb}
}

// renderFocused reports whether the render pane owns the keyboard: it is the
// active tab and the PagedView holds focus. When true, nav keys flip pages
// instead of moving the editor caret.
func (s *State) renderFocused() bool {
	return !s.showLog() && s.renderView.Focused()
}

// TakePendingCompile drains the edit latch (once).
// CompilePending compiles once iff a compile is pending, and reports whether it
// did. The host calls it after wiring the time provider so the boot compile's
// Log entries carry the same clock format as every later one.
func (s *State) CompilePending() bool {
	if s.TakePendingCompile() {
		s.Compile()
		return true
	}
	return false
}

func (s *State) TakePendingCompile() bool {
	if s.pendingCompile {
		s.pendingCompile = false
		return true
	}
	return false
}

// layout places the toolbar, the split and the status bar, then subdivides the
// left pane into editor + optional minimap. Chrome heights are scaled to the
// active HiDPI metric scale.
func (s *State) layout() {
	s.toolbarH = toolkit.Scaled(30)
	s.statusH = toolkit.Scaled(20)
	// The two moved-in HTML bands bracket the whole app: topZone above the toolbar,
	// bottomZone below the status bar. The topZone is a fixed line; the bottomZone
	// grows with its wrapped prose (measure returns its height at the current width).
	s.topZoneH = s.topZone.height()
	s.bottomZoneH = s.bottomZone.measure(s.w)
	bodyH := s.h - s.topZoneH - s.toolbarH - s.statusH - s.bottomZoneH
	if bodyH < 0 {
		bodyH = 0
	}
	top := s.bodyTop() // topZoneH + toolbarH: where the body begins
	// The workspace sidebar (when open) is a left column spanning the body band;
	// the editor+render body shrinks to the right of it. The status bar and the
	// two chrome bands stay full width.
	sbW := s.sidebar.width()
	s.sidebar.setBounds(toolkit.Rect{X: 0, Y: top, W: sbW, H: bodyH})
	s.paned.SetBounds(toolkit.Rect{X: sbW, Y: top, W: s.w - sbW, H: bodyH})
	s.status.SetBounds(toolkit.Rect{X: 0, Y: top + bodyH, W: s.w, H: s.statusH})
	s.topZone.setBounds(toolkit.Rect{X: 0, Y: 0, W: s.w, H: s.topZoneH})
	s.bottomZone.place(toolkit.Rect{X: 0, Y: top + bodyH + s.statusH, W: s.w, H: s.bottomZoneH})
	s.layoutToolbar()
	s.applyLeftSplit()
	s.wysiwygLayout() // editor-pane Source│WYSIWYG tab strip + RichEditor bounds (wysiwyg.go)
	s.fr.layout()     // find-and-replace modal: scrim + panel over the editor pane (findreplace.go)
}

// layoutToolbar places the colour-scheme picker and the minimap toggle
// left-to-right in the toolbar row (the former standalone "Log" toggle is now a
// tab in the right pane).
func (s *State) layoutToolbar() {
	pad := toolkit.Scaled(6)
	gap := toolkit.Scaled(8)
	h := s.toolbarH - 2*toolkit.Scaled(4)
	yy := s.topZoneH + toolkit.Scaled(4) // the toolbar row now sits below the topZone band
	x := pad
	pw := toolkit.Scaled(150)
	s.schemePicker.SetBounds(toolkit.Rect{X: x, Y: yy, W: pw, H: h})
	x += pw + gap
	ipw := toolkit.Scaled(110)
	s.iconPackPicker.SetBounds(toolkit.Rect{X: x, Y: yy, W: ipw, H: h})
	x += ipw + gap
	bw := toolkit.Scaled(84)
	s.minimapBtn.SetBounds(toolkit.Rect{X: x, Y: yy, W: bw, H: h})
	x += bw + gap
	sbw := toolkit.Scaled(sidebarBtnW)
	s.sidebarBtn.SetBounds(toolkit.Rect{X: x, Y: yy, W: sbw, H: h})
	x += sbw + gap
	fbw := toolkit.Scaled(56)
	s.findBtn.SetBounds(toolkit.Rect{X: x, Y: yy, W: fbw, H: h})
}

// applyLeftSplit reserves the top strip of the left pane for the editor's
// Source│WYSIWYG tabs (wysiwyg.go) and a strip on its right for the minimap (when
// shown), shrinking the CodeEditor to the remaining area below the tabs.
// Recomputed after every layout AND after a divider drag, because Paned re-lays
// its First child to the full left region.
func (s *State) applyLeftSplit() {
	pr := s.paned.Bounds()
	stripH := s.wysiwyg().stripHeight() // Source│WYSIWYG tabs sit above the editor
	ey := pr.Y + stripH
	eh := pr.H - stripH
	if eh < 0 {
		eh = 0
	}
	leftW := s.paned.Position().Get()
	mmW := toolkit.Scaled(84)
	if s.showMinimap && leftW > 3*mmW {
		editorW := leftW - mmW
		s.editor.SetBounds(toolkit.Rect{X: pr.X, Y: ey, W: editorW, H: eh})
		s.minimap.SetBounds(toolkit.Rect{X: pr.X + editorW, Y: ey, W: mmW, H: eh})
	} else {
		s.editor.SetBounds(toolkit.Rect{X: pr.X, Y: ey, W: leftW, H: eh})
		s.minimap.SetBounds(toolkit.Rect{})
	}
}

// Compile runs the engine, feeds the freshly rasterized page bitmaps to the
// render pane's PagedView, updates the diagnostics Log and the status bar. A
// hard error (or an empty document) yields no bitmaps, which clears the viewer.
func (s *State) Compile() {
	res := compileLaTeX(s.git.compileSource(), s.theme, s.git.resolveWorkspace())
	s.errText = res.errText
	s.pages = res.pages
	s.drawnPages = res.drawnPages
	s.diag = res.diag
	s.svgs = res.svgs
	s.pageSizes = res.sizes
	// The source-line bands are MEASURED FROM THE DOM once the host has placed
	// the pages (see SetLineBands): the browser lays the text out, so it is the
	// one that knows where each line ended up. They are cleared here so a click
	// between a compile and that measurement resolves to nothing rather than to
	// the previous document's lines.
	s.lineMaps = nil
	s.logCompile(res) // append a timestamped block to the accumulating Log

	// The pane lays the pages out and paints their paper; the host draws the
	// content over them, reading where from PagedView.PageRect.
	s.renderView.SetPageSizes(res.sizes)
	s.updateStatus()
	s.dirty = true
}

// updateStatus refreshes the status-bar segments from the caret + last compile.
func (s *State) updateStatus() {
	ln := s.editor.CursorLine().Get() + 1
	col := s.editor.CursorCol().Get() + 1
	s.status.SetSegment(0, "Ln "+strconv.Itoa(ln)+", Col "+strconv.Itoa(col))
	s.status.SetSegment(1, "UTF-8")
	pageWord := " pages"
	if s.pages == 1 {
		pageWord = " page"
	}
	seg := strconv.Itoa(s.pages) + pageWord
	if s.errText != "" {
		seg = "compile error"
	}
	s.status.SetSegment(2, seg)
	if n := diagIssueCount(s.diag, s.errText); n > 0 {
		s.status.SetSegment(3, strconv.Itoa(n)+" issue(s)")
	} else {
		s.status.SetSegment(3, "")
	}
}

// SetBuildInfo stamps the build identity into the last status-bar segment: a git
// short SHA (version) and a UTC build timestamp, both baked into the wasm at
// deploy time via `-ldflags -X`. The wasm shell (cmd/playground-wasm) calls it
// ONCE at startup with those injected values; a native/dev build never calls it
// and keeps the defaultBuildInfo placeholder. Because it targets the segment
// updateStatus leaves alone, the stamp survives every later compile — so a SHA
// visible on screen corresponds exactly to the wasm actually running (a new SHA
// after a deploy proves the fresh binary loaded past any Pages/CDN cache).
//
// It is an immutable set-once value, not a per-frame cross-boundary copy, so it
// respects the MVVM boundary (no reactive Observable is warranted for a constant
// stamped in at init).
func (s *State) SetBuildInfo(version, buildTime string) {
	s.buildInfo = formatBuildInfo(version, buildTime)
	s.status.SetSegment(buildInfoSegment, s.buildInfo)
	s.dirty = true
}

// editorLines is the editor buffer as a slice of lines. TextView's line buffer
// went private in the v0.214 MVVM migration (Text() is the sole public surface),
// so the playground reconstructs the lines from the committed Text() Observable —
// the exact inverse of TextView's own strings.Join(lines, "\n"). It feeds the
// minimap thumbnail, the LaTeX highlighter and the caret-line clamping.
func (s *State) editorLines() []string {
	return strings.Split(s.editor.Text().Get(), "\n")
}

// visibleEditorLines estimates how many buffer lines fit in the editor viewport,
// for the minimap's viewport indicator.
func (s *State) visibleEditorLines() int {
	lineH := toolkit.Scaled(baseFontPx + 4) // always > 0 at any positive scale
	n := s.editor.Bounds().H / lineH
	if n < 1 {
		n = 1
	}
	return n
}

// Draw paints the whole scene onto buf (RGBA, row-major, w*h*4).
func (s *State) Draw(buf []byte) {
	fillRGBA(buf, s.theme.Background)
	p := painter.NewPixelPainter(buf, s.w, s.h)

	// The topZone status band sits above the toolbar (moved in from the host HTML).
	s.topZone.draw(p, s.theme)

	// Toolbar ground + bottom hairline, each a Backdrop rather than a hand-filled
	// rect. The row sits below the topZone band, so its ground starts at bodyTop's
	// toolbar origin (topZoneH).
	tbY := s.topZoneH
	s.toolbarBg.Fill = s.theme.Surface
	s.toolbarBg.SetBounds(toolkit.Rect{X: 0, Y: tbY, W: s.w, H: s.toolbarH})
	s.toolbarBg.Draw(p, s.theme)
	s.toolbarRule.Fill = s.theme.Border
	s.toolbarRule.SetBounds(toolkit.Rect{X: 0, Y: tbY + s.toolbarH - toolkit.Scaled(1), W: s.w, H: toolkit.Scaled(1)})
	s.toolbarRule.Draw(p, s.theme)
	s.schemePicker.Draw(p, s.theme)
	s.iconPackPicker.Draw(p, s.theme)
	s.minimapBtn.Draw(p, s.theme)
	s.sidebarBtn.Selected().Set(s.sidebar.open)
	s.sidebarBtn.Draw(p, s.theme)
	s.findBtn.Selected().Set(s.fr.visible())
	s.findBtn.Draw(p, s.theme)

	// The workspace sidebar (left column of the body band), when open.
	s.sidebar.draw(p, s.theme)

	// Body: editor + right pane (tabs over render|log) + handle, then the
	// minimap overlay.
	s.paned.Draw(p, s.theme)
	if s.showMinimap && s.minimap.Bounds().W > 0 {
		lines := s.editorLines()
		spans := s.hl.Highlight("latex", lines, s.theme)
		s.minimap.Update(lines, spans, s.editor.ScrollLine().Get(), s.visibleEditorLines())
		s.minimap.Draw(p, s.theme)
	}
	if s.errText != "" && !s.showLog() {
		s.drawError(p)
	}
	s.status.Draw(p, s.theme)

	// The bottomZone footer band sits below the status bar (moved in from the host
	// HTML): the description prose + footer line, with clickable links.
	s.bottomZone.draw(p, s.theme)

	// WYSIWYG toolbar controls + (while active) the RichEditor overlay over the
	// editor pane and its own format popover (wysiwyg.go).
	s.wysiwygDraw(p)

	// Popover floats above everything.
	if s.schemePicker.Open().Get() {
		s.schemePicker.DrawPopover(p, s.theme)
	}
	if s.iconPackPicker.Open().Get() {
		s.iconPackPicker.DrawPopover(p, s.theme)
	}
	// The Collaborate + Git launchers and their modal panels float above the whole
	// scene.
	s.collab.draw(p, s.theme)
	s.git.draw(p, s.theme)

	// The find-and-replace modal (scrim + panel) is topmost of all, above the
	// popover and the other launchers (a no-op while hidden).
	s.fr.draw(p, s.theme)
	s.dirty = false
}

// drawError overlays the compile error at the top of the render content area
// (below the tab strip and the render's own zoom toolbar).
func (s *State) drawError(p painter.Painter) {
	r := s.renderContentRect()
	band := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: toolkit.Scaled(22)}
	s.errorBand.Fill = s.theme.SurfaceAlt
	s.errorBand.SetBounds(band)
	s.errorBand.Draw(p, s.theme)
	toolkit.DrawText(p, r.X+toolkit.Scaled(8), r.Y+toolkit.Scaled(6), "Error: "+s.errText, s.theme.Accent)
}

// --- input --------------------------------------------------------------

// onDivider reports whether (x,y) falls on the Paned divider hit-zone.
func (s *State) onDivider(x, y int) bool {
	pr := s.paned.Bounds()
	if y < pr.Y || y >= pr.Y+pr.H {
		return false
	}
	tol := toolkit.Scaled(3)
	pos := s.paned.Position().Get()
	d0 := pr.X + pos - tol
	d1 := pr.X + pos + toolkit.Scaled(toolkit.PanedHandleW) + tol
	return x >= d0 && x < d1
}

// HandleClick routes a pointer press and captures it for a following drag.
func (s *State) HandleClick(x, y int) bool {
	// The find-and-replace modal, when open, is modal: it captures EVERY click (a
	// panel control, or the scrim outside it, which dismisses). It gets first
	// refusal so nothing beneath the scrim reacts. handleClick returns false while
	// the modal is closed, so this is inert then.
	if s.fr.handleClick(x, y) {
		s.pressKind = pressFind
		return true
	}
	// The Collaborate / Git launchers + panels get next refusal; an open panel is
	// modal. Record the capture so the following move/release reach the same panel
	// (that mouseup is what clears a button's momentary pressed face).
	if s.collab.handleClick(x, y) {
		s.pressKind = pressCollab
		return true
	}
	if s.git.handleClick(x, y) {
		s.pressKind = pressGit
		return true
	}
	s.pressKind = pressNone

	// A click on a bottomZone link navigates (the footer band is below the status
	// bar, disjoint from every body widget, so this is checked up front).
	if s.bottomZone.handleClick(x, y) {
		s.dirty = true
		return true
	}

	// An open colour-scheme popover intercepts the next click.
	if s.schemePicker.Open().Get() {
		s.schemePicker.PopoverClick(x, y)
		s.dirty = true
		return true
	}
	// So does an open icon-pack popover.
	if s.iconPackPicker.Open().Get() {
		s.iconPackPicker.PopoverClick(x, y)
		s.dirty = true
		return true
	}

	// WYSIWYG controls / RichEditor claim the press first (wysiwyg.go).
	if s.wysiwygClick(x, y) {
		return true
	}

	// Toolbar controls (the row between the topZone band and the body).
	if y >= s.topZoneH && y < s.bodyTop() {
		for _, w := range []toolkit.Widget{s.schemePicker, s.iconPackPicker, s.minimapBtn, s.sidebarBtn, s.findBtn} {
			r := w.Bounds()
			if r.Contains(x, y) {
				w.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - r.X, Y: y - r.Y})
				s.pressKind = pressToolbar
				s.dirty = true
				return true
			}
		}
		return false
	}

	// Workspace sidebar (the left column of the body band, when open). It is a
	// distinct region that never overlaps the editor/render body, so it is tested
	// before the divider + body widgets.
	if s.sidebar.handleClick(x, y) {
		s.pressKind = pressSidebar
		s.dirty = true
		return true
	}

	// Divider (resize grip).
	if s.onDivider(x, y) {
		pr := s.paned.Bounds()
		s.paned.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - pr.X, Y: y - pr.Y})
		s.pressKind = pressDivider
		s.dirty = true
		return true
	}

	// Editor.
	er := s.editor.Bounds()
	if er.Contains(x, y) {
		s.editor.Focused().Set(true)
		s.renderView.SetFocused(false) // the editor now owns the keyboard
		s.selecting = false            // a click starts a fresh selection anchor
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - er.X, Y: y - er.Y})
		s.updateStatus()
		s.caretMaybeChanged() // caret → scroll the render to the clicked source line
		s.pressKind = pressEditor
		s.dirty = true
		return true
	}

	// Minimap.
	if s.showMinimap && s.minimap.Bounds().Contains(x, y) {
		s.minimapScrollTo(y)
		s.pressKind = pressMinimap
		s.dirty = true
		return true
	}

	// Right pane (tab strip over the render view or the Log view).
	rr := s.rightPane.Bounds()
	if rr.Contains(x, y) {
		s.rightPane.click(x, y)
		// A click into the render content either lands on a rendered source line —
		// jumping the editor caret there (click-to-source) and giving the editor
		// focus — or, on bare page margin / gutter / toolbar, gives the PagedView
		// keyboard focus so nav keys flip pages. A tab-strip or Log click leaves the
		// editor's focus alone.
		if s.rightPane.press == rpPressRender && !s.tryClickToSource(x, y) {
			s.editor.Focused().Set(false)
			s.renderView.SetFocused(true)
		}
		s.pressKind = pressRight
		s.dirty = true
		return true
	}
	return false
}

// HandleMove routes a captured drag to its target. This is what makes the Paned
// divider and the scrollbar thumbs draggable (previously a no-op, so move/up
// were discarded and no widget ever received EventMouseDrag).
func (s *State) HandleMove(x, y int) bool {
	// The find-and-replace modal is modal: while it is open it swallows every
	// move, and while its title bar is held it IS the drag. This has to come
	// FIRST — the hover and drag routes below would otherwise take the move and
	// the panel would never follow the pointer.
	if s.fr.shown {
		if s.pressKind == pressFind {
			s.fr.handleDrag(x, y)
		}
		return true
	}
	// An open Collaborate / Git panel is modal: route the move to it for button
	// hover feedback regardless of whether a button is pressed.
	if s.collab.open {
		s.collab.handleMove(x, y)
		return true
	}
	if s.git.open {
		s.git.handleMove(x, y)
		return true
	}
	// Footer link hover: raise the underline on the bottomZone link under the
	// pointer (clear the others). handleMove reports true only when the hovered
	// link actually changed, in which case the frame must repaint — so return
	// consumed to make the host render(). A hover change can only happen over the
	// footer band, disjoint from every body widget, so this never steals a drag
	// (which keeps routing through the switch below on the no-change path).
	if s.bottomZone.handleMove(x, y) {
		s.dirty = true
		return true
	}
	if s.wysiwygMove(x, y) { // WYSIWYG RichEditor drag (wysiwyg.go)
		return true
	}
	switch s.pressKind {
	case pressDivider:
		pr := s.paned.Bounds()
		s.paned.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - pr.X, Y: y - pr.Y})
		s.applyLeftSplit() // Paned re-laid First to full width; re-reserve the minimap
		s.wysiwygLayout()  // and re-size the editor tab strip to the new left width
		s.dirty = true
		return true
	case pressEditor:
		er := s.editor.Bounds()
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - er.X, Y: y - er.Y})
		s.caretMaybeChanged() // a drag that moves the caret line scrolls the render
		s.dirty = true
		return true
	case pressMinimap:
		s.minimapScrollTo(y)
		s.dirty = true
		return true
	case pressRight:
		s.rightPane.drag(x, y)
		s.dirty = true
		return true
	}
	return false
}

// HandleRelease ends a captured drag, delivering EventMouseUp to the target.
func (s *State) HandleRelease(x, y int) bool {
	// The find-and-replace modal gets its release: a title-bar drag ends on it,
	// and without one the Dialog stays armed and the panel follows the pointer
	// after the button is up.
	if s.pressKind == pressFind {
		s.fr.handleRelease(x, y)
		s.pressKind = pressNone
		return true
	}
	// A Collaborate / Git panel that captured the press gets the release even if
	// the action closed it — the mouseup clears the pressed button faces.
	if s.pressKind == pressCollab {
		s.collab.handleRelease(x, y)
		s.pressKind = pressNone
		return true
	}
	if s.pressKind == pressGit {
		s.git.handleRelease(x, y)
		s.pressKind = pressNone
		return true
	}
	if s.pressKind == pressSidebar {
		s.sidebar.handleRelease(x, y)
		s.pressKind = pressNone
		return true
	}
	if s.wysiwygRelease(x, y) { // end a WYSIWYG RichEditor drag (wysiwyg.go)
		return true
	}
	kind := s.pressKind
	switch kind {
	case pressDivider:
		pr := s.paned.Bounds()
		s.paned.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - pr.X, Y: y - pr.Y})
	case pressEditor:
		er := s.editor.Bounds()
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - er.X, Y: y - er.Y})
	case pressRight:
		s.rightPane.release(x, y)
	}
	s.pressKind = pressNone
	if kind != pressNone {
		s.dirty = true
		return true
	}
	return false
}

// HandleScroll forwards a wheel/two-finger scroll to the pane under the pointer.
// dx/dy are in ROWS (a horizontal swipe carries dx); the render pane routes both
// axes so a horizontal swipe moves its horizontal scrollbar, while the editor and
// minimap scroll vertically only.
func (s *State) HandleScroll(x, y, dx, dy int) bool {
	if s.wysiwygScroll(x, y, dy) { // WYSIWYG RichEditor wheel scroll (wysiwyg.go)
		return true
	}
	// The workspace sidebar's file tree + timeline scroll under the pointer.
	if s.sidebar.handleScroll(x, y, dy) {
		s.dirty = true
		return true
	}
	rr := s.rightPane.Bounds()
	if rr.Contains(x, y) {
		s.rightPane.scrollWheel(x, y, dx, dy)
		s.dirty = true
		return true
	}
	er := s.editor.Bounds()
	if er.Contains(x, y) {
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, X: x - er.X, Y: y - er.Y, Delta: dy})
		s.dirty = true
		return true
	}
	if s.showMinimap && s.minimap.Bounds().Contains(x, y) {
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, Delta: dy})
		s.dirty = true
		return true
	}
	return false
}

// HandleChar routes a printable character to the editor. Typing ends any
// keyboard selection in progress (the inserted text replaces it if present).
// When the render pane holds focus instead, a space bar pages the viewer and no
// character reaches the (unfocused) editor.
func (s *State) HandleChar(code string) bool {
	if s.fr.handleChar(code) { // typing goes to the find bar's focused field while open
		return true
	}
	if s.collab.handleChar(code) { // typing edits the collab display name when focused
		return true
	}
	if s.git.handleChar(code) { // typing edits the focused git panel field
		return true
	}
	if s.wysiwygChar(code) { // type into the WYSIWYG RichEditor (wysiwyg.go)
		return true
	}
	if s.renderFocused() {
		if code == " " || code == "Space" {
			return s.rightPane.keyDown("Space")
		}
		return false
	}
	if !s.editor.Focused().Get() {
		return false
	}
	if s.editor.HasSelection() {
		s.editor.DeleteSelection() // typing over a selection replaces it
	}
	s.selecting = false
	s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: code})
	s.updateStatus()
	s.dirty = true
	return true
}

// navBase maps a navigation key that can be Shift-extended to its bare form, or
// "" when the key is not a Shift-selectable navigation key.
func navBase(code string) string {
	switch code {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End":
		return code
	}
	return ""
}

// HandleKeyDown routes a named key. When the render pane holds keyboard focus it
// receives the key (PageDown / ArrowUp / Home / End flip pages) and the editor is
// left untouched. Otherwise it goes to the editor: a "Shift+"-prefixed navigation
// key extends the selection from an anchor; a plain navigation key moves the
// caret and collapses any selection; every other key is forwarded as-is.
func (s *State) HandleKeyDown(code string) bool {
	if s.fr.handleKey(code) { // Enter=next, Shift+Enter=prev, Escape=close, else the field
		return true
	}
	if s.collab.handleKey(code) { // Backspace/Enter/Escape in the collab name field
		return true
	}
	if s.git.handleKey(code) { // Backspace/Enter/Tab/Escape in the git panel fields
		return true
	}
	if s.wysiwygKey(code) { // navigate/edit the WYSIWYG RichEditor (wysiwyg.go)
		return true
	}
	if s.renderFocused() {
		return s.rightPane.keyDown(code)
	}
	if !s.editor.Focused().Get() {
		return false
	}
	// nav records whether this key moved the caret (as opposed to editing text), so
	// only a genuine caret MOVE scrolls the render to the new source line — typing
	// leaves the render alone until the debounced recompile catches up.
	nav := false
	switch {
	case len(code) > 6 && code[:6] == "Shift+" && navBase(code[6:]) != "":
		s.shiftSelect(navBase(code[6:]))
		nav = true
	case navBase(code) != "" && s.editor.HasSelection():
		// A plain navigation key collapses the selection as the caret moves.
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code})
		s.editor.ClearSelection()
		s.selecting = false
		nav = true
	default:
		s.selecting = false
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code})
		nav = navBase(code) != ""
	}
	s.afterKey()
	if nav {
		s.caretMaybeChanged() // caret → scroll the render to follow the moved caret
	}
	return true
}

// shiftSelect anchors a keyboard selection on its first Shift+navigation key,
// moves the caret with the bare key, then extends Selection from the anchor to
// the new caret.
func (s *State) shiftSelect(base string) {
	if !s.selecting {
		s.selAnchorLine, s.selAnchorCol = s.editor.CursorLine().Get(), s.editor.CursorCol().Get()
		s.selecting = true
	}
	s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: base})
	s.editor.SetSelection(toolkit.SelectionRange(s.selAnchorLine, s.selAnchorCol, s.editor.CursorLine().Get(), s.editor.CursorCol().Get()))
}

// afterKey runs the shared post-key bookkeeping (status + dirty).
func (s *State) afterKey() {
	s.updateStatus()
	s.dirty = true
}

// minimapScrollTo scrolls the editor so the buffer line under the surface-y
// under the pointer sits at the top of the viewport. It refreshes the overview's
// line cache first so a click maps correctly even before the first paint (Draw
// also refreshes it every frame; the pointer mapping needs only the line count,
// so the per-line spans are left nil), then hands the widget a click at that y —
// CodeMinimap re-anchors the local y to surface space, maps it to a buffer line
// and fires OnScrollToLine, which sets the editor's ScrollLine.
func (s *State) minimapScrollTo(y int) {
	s.minimap.Update(s.editorLines(), nil, s.editor.ScrollLine().Get(), s.visibleEditorLines())
	s.minimap.OnEvent(toolkit.Event{Kind: toolkit.EventClick, Y: y - s.minimap.Bounds().Y})
}

// --- source ↔ render linking --------------------------------------------

// sourceClickTolY is how far (in page-natural pixels) a render-pane click may
// fall from the nearest source-line band and still resolve to that line, so a
// click in the small leading between two glyph rows of a paragraph snaps to the
// closest line while a click far out in a page margin does not (it leaves the
// render owning the keyboard for paging instead).
const sourceClickTolY = 40

// renderSourceLineAt maps a SURFACE point over the render pane to the source line
// rendered under it. It translates the point into the PagedView's widget-local
// frame and asks the widget which page + natural point sits there (PageAt is false
// over the toolbar, a gap or the gutter — i.e. exactly when a click WAS consumed
// as a control), then looks that natural-Y up against the page's source-line
// bands. ok is false when the point is not over a linked page line.
func (s *State) renderSourceLineAt(x, y int) (int, bool) {
	b := s.renderView.Bounds()
	page, _, localY, ok := s.renderView.PageAt(x-b.X, y-b.Y)
	if !ok {
		return 0, false
	}
	return s.lineAtPageY(page, localY)
}

// tryClickToSource turns a click on the render pane into an editor caret jump,
// returning true (and moving the caret) only when the click resolved to a source
// line.
func (s *State) tryClickToSource(x, y int) bool {
	line, ok := s.renderSourceLineAt(x, y)
	if !ok {
		return false
	}
	s.jumpCaretToLine(line)
	return true
}

// lineAtPageY resolves a page-natural Y on a 1-based rendered page to the source
// line drawn there: the band whose [yTop, yBot) contains y, else the nearest band
// within sourceClickTolY. It reports ok=false for an out-of-range page, a page
// with no recorded lines, or a Y too far from every band.
func (s *State) lineAtPageY(page, y int) (int, bool) {
	idx := page - 1
	if idx < 0 || idx >= len(s.lineMaps) {
		return 0, false
	}
	bands := s.lineMaps[idx].bands
	best, bestDist := -1, 0
	for i, bd := range bands {
		if y >= bd.yTop && y < bd.yBot {
			return bd.line, true // inside the band: an exact hit
		}
		d := bd.yTop - y
		if y >= bd.yBot {
			d = y - bd.yBot + 1
		}
		if best < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	if best < 0 || bestDist > sourceClickTolY {
		return 0, false
	}
	return bands[best].line, true
}

// pageYForLine finds where a 1-based source line was rendered: the first page
// carrying a band for that exact line, at the band's top (natural pixels). Lines
// that drew nothing (blank or purely structural, e.g. \begin{document}) carry no
// band, so it falls back to the present line nearest in number — the caret always
// scrolls the render to the closest rendered output rather than silently doing
// nothing. ok is false only when NO page has any band at all.
func (s *State) pageYForLine(srcLine int) (page, y int, ok bool) {
	for i, lm := range s.lineMaps {
		for _, bd := range lm.bands {
			if bd.line == srcLine {
				return i + 1, bd.yTop, true
			}
		}
	}
	bestPage, bestY, bestLine, found := 0, 0, 0, false
	for i, lm := range s.lineMaps {
		for _, bd := range lm.bands {
			if !found || absInt(bd.line-srcLine) < absInt(bestLine-srcLine) {
				bestPage, bestY, bestLine, found = i+1, bd.yTop, bd.line, true
			}
		}
	}
	if !found {
		return 0, 0, false
	}
	return bestPage, bestY, true
}

// jumpCaretToLine places the editor caret at column 0 of a 1-based source line,
// clears any selection, focuses the editor and scrolls it so the line shows. The
// syncing guard is raised for the whole move so the caret change it produces does
// NOT bounce back into a render scroll (the click already picked the page) —
// lastCaretLine is updated directly instead. This is the anti-feedback seam.
func (s *State) jumpCaretToLine(line int) {
	li := line - 1
	if li < 0 {
		li = 0
	}
	// The buffer always has at least one line (TextView keeps a lone "" line for
	// an "empty" document), so editorLines() is never zero-length and li lands in
	// [0, n-1] after this clamp — no empty-buffer guard is reachable.
	if n := len(s.editorLines()); li >= n {
		li = n - 1
	}
	s.syncing = true
	s.editor.CursorLine().Set(li)
	s.editor.CursorCol().Set(0)
	s.editor.ClearSelection()
	s.selecting = false
	s.editor.Focused().Set(true)
	s.renderView.SetFocused(false)
	s.scrollEditorToLine(li)
	s.lastCaretLine = li
	s.updateStatus()
	s.syncing = false
}

// scrollEditorToLine nudges the editor's vertical scroll so 0-based line li is
// visible, centring it when it currently sits off-screen. A line already in view
// is left where it is (no jump).
func (s *State) scrollEditorToLine(li int) {
	vis := s.visibleEditorLines()
	if sl := s.editor.ScrollLine().Get(); li >= sl && li < sl+vis {
		return
	}
	top := li - vis/2
	if top < 0 {
		top = 0
	}
	s.editor.ScrollLine().Set(top)
}

// caretMaybeChanged scrolls the render to follow the editor caret, but only when
// the caret's LINE actually moved and the move was not itself driven by a render
// click (syncing). It is the caret → render half of the linking; because it only
// ever scrolls the render (never moves the caret) it cannot form a feedback loop
// with the click → caret half.
func (s *State) caretMaybeChanged() {
	cl := s.editor.CursorLine().Get()
	if s.syncing || cl == s.lastCaretLine {
		return
	}
	s.lastCaretLine = cl
	if page, y, ok := s.pageYForLine(cl + 1); ok {
		s.renderView.ScrollToPage(page, y)
	}
}

// Read-only introspection for the headless verification harness of the
// source↔render linking. RenderLineAt maps a device-pixel render-pane point to
// the source line drawn there (0 = none), LineToRenderPage the inverse page
// lookup (0 = nothing rendered for the line), RenderScrollY the render pane's
// vertical scroll offset. SetRenderPaginated flips the viewer's mode so a
// caret-driven scroll shows up crisply as a current-page change.
func (s *State) RenderLineAt(x, y int) int {
	if line, ok := s.renderSourceLineAt(x, y); ok {
		return line
	}
	return 0
}

func (s *State) LineToRenderPage(line int) int {
	if page, _, ok := s.pageYForLine(line); ok {
		return page
	}
	return 0
}

func (s *State) RenderScrollY() int {
	_, y := s.renderView.ScrollOffset()
	return y
}

func (s *State) SetRenderPaginated(on bool) {
	if on {
		s.renderView.Mode().Set(toolkit.PagedPaginated)
	} else {
		s.renderView.Mode().Set(toolkit.PagedContinuous)
	}
	s.dirty = true
}

// absInt is the integer absolute value.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// SetAssetLoading names the background asset currently downloading ("" clears
// it). The topZone shows it in place of the ready line, so the reader can see
// which wasm is still on its way.
func (s *State) SetAssetLoading(name string) {
	if s.assetLoading == name {
		return
	}
	s.assetLoading = name
	s.dirty = true
}

// AssetLoading is what the topZone is currently announcing (host introspection).
func (s *State) AssetLoading() string { return s.assetLoading }
