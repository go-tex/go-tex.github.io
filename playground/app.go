// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package playground is the go-tex WebAssembly LaTeX playground rendered as a
// single go-widgets/toolkit canvas application: a toolbar (syntax colour-scheme
// picker, minimap toggle, Rendered/Log toggle), a horizontal split of a
// CodeEditor (line numbers, LaTeX syntax colours, current-line highlight) with
// an optional minimap on the left and a live, scrollable pane of the pure-Go
// gotex render (or a diagnostics Log) on the right, and a status bar (caret
// position, encoding, page count, issue count). The caret scrolls the render to
// its source line and clicking the render moves the caret there.
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
	"strconv"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	"github.com/go-tex/go-tex.github.io/playground/latexhl"
)

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

// pressKind records what the current pointer press captured, so a following
// move/release is routed to the same target (the fix for a divider/scrollbar
// that could not be dragged because move/up were discarded).
const (
	pressNone = iota
	pressToolbar
	pressDivider
	pressEditor
	pressMinimap
	pressRight // the render ScrollView or the Log view (paned.Second)
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

	editor       *toolkit.CodeEditor
	hl           *latexhl.Highlighter
	renderImg    *toolkit.Image
	renderScroll *toolkit.ScrollView
	logView      *logView
	minimap      *minimap
	paned        *toolkit.Paned
	status       *toolkit.Statusbar

	schemePicker *toolkit.DropDown
	minimapBtn   *toolkit.Button
	logBtn       *toolkit.Button

	showMinimap bool
	showLog     bool

	// last compile output.
	lineBands map[int][2]int
	errText   string
	pages     int
	diag      engine.Diagnostics

	// chrome heights (device pixels), recomputed each layout at the active scale.
	toolbarH, statusH int

	// pointer drag capture.
	pressKind int

	dirty          bool
	pendingCompile bool

	// OnCompileNeeded schedules a debounced Compile after an edit; OnEdit
	// persists the buffer independent of the compile path. Nil in tests.
	OnCompileNeeded func()
	OnEdit          func(text string)
}

// NewState builds the playground at w×h DEVICE pixels, dark or light, compiles
// the sample document once and returns the ready scene. The caller installs the
// text/scale first via SetupText.
func NewState(w, h int, dark bool) *State {
	s := &State{w: w, h: h, dark: dark, lineBands: map[int][2]int{}, showMinimap: true}
	s.setTheme(dark)

	s.hl = latexhl.New()
	s.editor = toolkit.NewCodeEditor(SampleLaTeX)
	s.editor.Language = "latex"
	s.editor.Syntax = s.hl
	s.editor.Focused = true
	s.editor.OnChange = func() {
		if s.OnEdit != nil {
			s.OnEdit(s.editor.Text())
		}
		s.pendingCompile = true
		s.dirty = true
		if s.OnCompileNeeded != nil {
			s.OnCompileNeeded()
		}
	}

	s.renderImg = toolkit.NewImage(nil, 0, 0)
	s.renderScroll = toolkit.NewScrollView(s.renderImg)
	s.logView = &logView{}
	s.minimap = &minimap{}
	s.paned = toolkit.NewHPaned(s.editor, s.renderScroll)

	s.schemePicker = toolkit.NewDropDown(latexhl.ThemeNames(), 0)
	s.schemePicker.OnSelect = func(idx int) { s.applyScheme(idx) }
	s.minimapBtn = toolkit.NewButton("Minimap", func() {
		s.showMinimap = !s.showMinimap
		s.layout()
		s.dirty = true
	})
	s.logBtn = toolkit.NewButton("Log", func() { s.toggleLog() })

	s.status = toolkit.NewStatusbar([]string{"Ln 1, Col 1", "UTF-8", "0 pages", ""})

	s.layout()
	s.Compile()
	return s
}

// applyScheme switches the syntax colour theme. A fresh highlighter instance is
// installed so the CodeEditor's span cache (keyed by highlighter identity)
// invalidates and re-lexes with the new palette.
func (s *State) applyScheme(idx int) {
	names := latexhl.ThemeNames()
	if idx < 0 || idx >= len(names) {
		return
	}
	hl := latexhl.New()
	hl.SetTheme(names[idx])
	s.hl = hl
	s.editor.Syntax = hl
	s.dirty = true
}

// toggleLog swaps the right pane between the render and the diagnostics Log.
func (s *State) toggleLog() {
	s.showLog = !s.showLog
	if s.showLog {
		s.paned.Second = s.logView
	} else {
		s.paned.Second = s.renderScroll
	}
	s.layout()
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
	s.layout()
	s.dirty = true
}

// Size returns the current surface size.
func (s *State) Size() (int, int) { return s.w, s.h }

// Source returns the current editor buffer.
func (s *State) Source() string { return s.editor.Text() }

// SetSource replaces the editor buffer (restoring a persisted document), resets
// the caret and recompiles. It does NOT fire OnEdit.
func (s *State) SetSource(text string) {
	s.editor.SetText(text)
	s.Compile()
	s.dirty = true
}

// Dirty/ClearDirty/Theme accessors.
func (s *State) Dirty() bool           { return s.dirty }
func (s *State) ClearDirty()           { s.dirty = false }
func (s *State) Theme() *toolkit.Theme { return s.theme }

// Read-only introspection for the host/verification harness: the editor pane
// width, the render pane's vertical scroll offset, and whether the Log is shown.
// They let a headless test assert a real width/scroll change after a drag.
func (s *State) EditorWidth() int   { return s.editor.Bounds().W }
func (s *State) RenderOffsetY() int { return s.renderScroll.OffsetY }
func (s *State) ShowLog() bool      { return s.showLog }

// DividerX is the surface x of the resize grip's leading edge.
func (s *State) DividerX() int { return s.paned.Bounds().X + s.paned.Position }

// TakePendingCompile drains the edit latch (once).
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
	bodyH := s.h - s.toolbarH - s.statusH
	if bodyH < 0 {
		bodyH = 0
	}
	s.paned.SetBounds(toolkit.Rect{X: 0, Y: s.toolbarH, W: s.w, H: bodyH})
	s.status.SetBounds(toolkit.Rect{X: 0, Y: s.toolbarH + bodyH, W: s.w, H: s.statusH})
	s.layoutToolbar()
	s.applyLeftSplit()
}

// layoutToolbar places the three controls left-to-right in the toolbar row.
func (s *State) layoutToolbar() {
	pad := toolkit.Scaled(6)
	gap := toolkit.Scaled(8)
	h := s.toolbarH - 2*toolkit.Scaled(4)
	yy := toolkit.Scaled(4)
	x := pad
	pw := toolkit.Scaled(150)
	s.schemePicker.SetBounds(toolkit.Rect{X: x, Y: yy, W: pw, H: h})
	x += pw + gap
	bw := toolkit.Scaled(84)
	s.minimapBtn.SetBounds(toolkit.Rect{X: x, Y: yy, W: bw, H: h})
	x += bw + gap
	s.logBtn.SetBounds(toolkit.Rect{X: x, Y: yy, W: bw, H: h})
}

// applyLeftSplit reserves a strip of the left pane for the minimap (when shown),
// shrinking the editor to fit. Recomputed after every layout AND after a divider
// drag, because Paned re-lays its First child to the full left width.
func (s *State) applyLeftSplit() {
	pr := s.paned.Bounds()
	leftW := s.paned.Position
	mmW := toolkit.Scaled(84)
	if s.showMinimap && leftW > 3*mmW {
		editorW := leftW - mmW
		s.editor.SetBounds(toolkit.Rect{X: pr.X, Y: pr.Y, W: editorW, H: pr.H})
		s.minimap.SetBounds(toolkit.Rect{X: pr.X + editorW, Y: pr.Y, W: mmW, H: pr.H})
	} else {
		s.editor.SetBounds(toolkit.Rect{X: pr.X, Y: pr.Y, W: leftW, H: pr.H})
		s.minimap.SetBounds(toolkit.Rect{})
	}
}

// Compile runs the engine, updates the render image, the per-line bands, the
// diagnostics Log and the status bar.
func (s *State) Compile() {
	res := compileLaTeX(s.editor.Text(), s.theme)
	s.errText = res.errText
	s.pages = res.pages
	s.diag = res.diag
	s.lineBands = res.lineBands
	s.logView.set(res.diag, res.errText)

	if res.pixels != nil {
		s.renderImg.Pixels = res.pixels
		s.renderImg.W, s.renderImg.H = res.w, res.h
		s.renderImg.SetBounds(toolkit.Rect{X: 0, Y: 0, W: res.w, H: res.h})
		s.renderScroll.SetContentSize(res.w, res.h)
	} else {
		s.renderImg.Pixels = nil
		s.renderImg.W, s.renderImg.H = 0, 0
		s.renderScroll.SetContentSize(0, 0)
	}
	s.updateStatus()
	s.dirty = true
}

// updateStatus refreshes the status-bar segments from the caret + last compile.
func (s *State) updateStatus() {
	ln := s.editor.CursorLine + 1
	col := s.editor.CursorCol + 1
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
	if n := s.logView.alarmCount(); n > 0 {
		s.status.SetSegment(3, strconv.Itoa(n)+" issue(s)")
	} else {
		s.status.SetSegment(3, "")
	}
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

	// Toolbar.
	p.FillRect(toolkit.Rect{X: 0, Y: 0, W: s.w, H: s.toolbarH}, s.theme.Surface)
	p.FillRect(toolkit.Rect{X: 0, Y: s.toolbarH - toolkit.Scaled(1), W: s.w, H: toolkit.Scaled(1)}, s.theme.Border)
	s.schemePicker.Draw(p, s.theme)
	s.minimapBtn.Draw(p, s.theme)
	s.logBtn.Draw(p, s.theme)

	// Body: editor + (render|log) + handle, then the minimap overlay.
	s.paned.Draw(p, s.theme)
	if s.showMinimap && s.minimap.Bounds().W > 0 {
		s.minimap.update(s.editor.Lines, s.editor.ScrollLine, s.visibleEditorLines())
		s.minimap.Draw(p, s.theme)
	}
	if s.errText != "" && !s.showLog {
		s.drawError(p)
	}
	s.status.Draw(p, s.theme)

	// Popover floats above everything.
	if s.schemePicker.Open {
		s.schemePicker.DrawPopover(p, s.theme)
	}
	s.dirty = false
}

// drawError overlays the compile error at the top of the render pane.
func (s *State) drawError(p painter.Painter) {
	r := s.paned.Second.Bounds()
	band := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: toolkit.Scaled(22)}
	p.FillRect(band, s.theme.SurfaceAlt)
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
	d0 := pr.X + s.paned.Position - tol
	d1 := pr.X + s.paned.Position + toolkit.Scaled(toolkit.PanedHandleW) + tol
	return x >= d0 && x < d1
}

// onRenderScrollbar reports whether x falls in the render pane's vertical
// scrollbar gutter (so a press there scrolls rather than navigates).
func (s *State) onRenderScrollbar(x int) bool {
	r := s.paned.Second.Bounds()
	return x >= r.X+r.W-toolkit.Scaled(16)
}

// HandleClick routes a pointer press and captures it for a following drag.
func (s *State) HandleClick(x, y int) bool {
	s.pressKind = pressNone

	// An open colour-scheme popover intercepts the next click.
	if s.schemePicker.Open {
		s.schemePicker.PopoverClick(x, y)
		s.dirty = true
		return true
	}

	// Toolbar controls.
	if y < s.toolbarH {
		for _, w := range []toolkit.Widget{s.schemePicker, s.minimapBtn, s.logBtn} {
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
		s.editor.Focused = true
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - er.X, Y: y - er.Y})
		s.scrollRenderToLine(s.editor.CursorLine + 1)
		s.updateStatus()
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

	// Right pane (render ScrollView or Log view).
	rr := s.paned.Second.Bounds()
	if rr.Contains(x, y) {
		s.paned.Second.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - rr.X, Y: y - rr.Y})
		if !s.showLog && !s.onRenderScrollbar(x) {
			s.navRenderToEditor(x, y)
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
	switch s.pressKind {
	case pressDivider:
		pr := s.paned.Bounds()
		s.paned.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - pr.X, Y: y - pr.Y})
		s.applyLeftSplit() // Paned re-laid First to full width; re-reserve the minimap
		s.dirty = true
		return true
	case pressEditor:
		er := s.editor.Bounds()
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - er.X, Y: y - er.Y})
		s.dirty = true
		return true
	case pressMinimap:
		s.minimapScrollTo(y)
		s.dirty = true
		return true
	case pressRight:
		rr := s.paned.Second.Bounds()
		s.paned.Second.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - rr.X, Y: y - rr.Y})
		s.dirty = true
		return true
	}
	return false
}

// HandleRelease ends a captured drag, delivering EventMouseUp to the target.
func (s *State) HandleRelease(x, y int) bool {
	kind := s.pressKind
	switch kind {
	case pressDivider:
		pr := s.paned.Bounds()
		s.paned.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - pr.X, Y: y - pr.Y})
	case pressEditor:
		er := s.editor.Bounds()
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - er.X, Y: y - er.Y})
	case pressRight:
		rr := s.paned.Second.Bounds()
		s.paned.Second.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - rr.X, Y: y - rr.Y})
	}
	s.pressKind = pressNone
	if kind != pressNone {
		s.dirty = true
		return true
	}
	return false
}

// HandleScroll forwards a wheel scroll (delta in rows) to the pane under the
// pointer.
func (s *State) HandleScroll(x, y, delta int) bool {
	rr := s.paned.Second.Bounds()
	if rr.Contains(x, y) {
		if s.showLog {
			s.logView.offset += delta * rowStep
			if s.logView.offset < 0 {
				s.logView.offset = 0
			}
		} else {
			s.renderScroll.Scroll(0, delta*rowStep)
		}
		s.dirty = true
		return true
	}
	er := s.editor.Bounds()
	if er.Contains(x, y) {
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, X: x - er.X, Y: y - er.Y, Delta: delta})
		s.dirty = true
		return true
	}
	if s.showMinimap && s.minimap.Bounds().Contains(x, y) {
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, Delta: delta})
		s.dirty = true
		return true
	}
	return false
}

// rowStep is the pixel distance one wheel row scrolls.
const rowStep = 28

// HandleChar routes a printable character to the editor.
func (s *State) HandleChar(code string) bool {
	if !s.editor.Focused {
		return false
	}
	s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: code})
	s.updateStatus()
	s.dirty = true
	return true
}

// HandleKeyDown routes a named key to the editor.
func (s *State) HandleKeyDown(code string) bool {
	if !s.editor.Focused {
		return false
	}
	s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code})
	s.scrollRenderToLine(s.editor.CursorLine + 1)
	s.updateStatus()
	s.dirty = true
	return true
}

// minimapScrollTo scrolls the editor so the buffer line under minimap-y sits at
// the top of the viewport.
func (s *State) minimapScrollTo(y int) {
	// Refresh the overview's line cache so a click maps correctly even before the
	// first paint (Draw also refreshes it every frame).
	s.minimap.update(s.editor.Lines, s.editor.ScrollLine, s.visibleEditorLines())
	s.editor.ScrollLine = s.minimap.lineAtY(y) // lineAtY already clamps to [0, n-1]
}

// scrollRenderToLine scrolls the render pane so the band for a 1-based source
// line is visible. No-op when the render pane is hidden or the line has no band.
func (s *State) scrollRenderToLine(line int) {
	if s.showLog {
		return
	}
	band, ok := s.lineBands[line]
	if !ok {
		return
	}
	rr := s.paned.Second.Bounds()
	target := band[0] - rr.H/3
	if target < 0 {
		target = 0
	}
	s.renderScroll.Scroll(0, target-s.renderScroll.OffsetY)
}

// navRenderToEditor maps a click in the render pane to the nearest source line
// and moves the editor caret there.
func (s *State) navRenderToEditor(x, y int) {
	rr := s.paned.Second.Bounds()
	contentY := (y - rr.Y) + s.renderScroll.OffsetY
	line := s.lineAt(contentY)
	if line <= 0 {
		return
	}
	target := line - 1
	if target >= len(s.editor.Lines) {
		target = len(s.editor.Lines) - 1
	}
	s.editor.CursorLine = target
	s.editor.CursorCol = 0
	s.editor.Focused = true
	s.updateStatus()
}

// lineAt returns the 1-based source line whose band contains contentY, or the
// nearest band's line. Returns 0 when there are no bands.
func (s *State) lineAt(contentY int) int {
	best, bestDist := 0, 1<<62
	for line, b := range s.lineBands {
		if contentY >= b[0] && contentY < b[1] {
			return line
		}
		d := b[0] - contentY
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			bestDist, best = d, line
		}
	}
	return best
}
