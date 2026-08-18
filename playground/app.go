// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package playground is the go-tex WebAssembly LaTeX playground rendered as a
// single go-widgets/toolkit canvas application: a toolbar (syntax colour-scheme
// picker, minimap toggle), a horizontal split of a CodeEditor (line numbers,
// LaTeX syntax colours, current-line highlight) with an optional code minimap on
// the left and, on the right, a small Rendered│Log tab strip over either a
// zoomable live pane of the pure-Go gotex render or a diagnostics Log, and a
// status bar (caret position, encoding, page count, issue count). The caret
// scrolls the render to its source line and clicking the render moves the caret
// there.
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

	editor     *toolkit.CodeEditor
	hl         *latexhl.Highlighter
	renderView *renderView
	logView    *logView
	rightPane  *rightPane
	minimap    *minimap
	paned      *toolkit.Paned
	status     *toolkit.Statusbar

	schemePicker *toolkit.DropDown
	minimapBtn   *toolkit.Button

	showMinimap bool

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

	s.renderView = newRenderView()
	s.logView = &logView{}
	s.rightPane = newRightPane(s.renderView, s.logView)
	s.minimap = &minimap{}
	s.paned = toolkit.NewHPaned(s.editor, s.rightPane)

	s.schemePicker = toolkit.NewDropDown(latexhl.ThemeNames(), 0)
	s.schemePicker.OnSelect = func(idx int) { s.applyScheme(idx) }
	s.minimapBtn = toolkit.NewButton("Minimap", func() {
		s.showMinimap = !s.showMinimap
		s.layout()
		s.dirty = true
	})

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
// width, the render pane's vertical scroll offset, whether the Log is shown, the
// active tab index, the render zoom percentage, the selected colour scheme index
// and the minimap's token-segment count. They let a headless test assert real
// state changes after a pointer interaction.
func (s *State) EditorWidth() int    { return s.editor.Bounds().W }
func (s *State) RenderOffsetY() int  { return s.renderView.offsetY() }
func (s *State) ShowLog() bool       { return s.showLog() }
func (s *State) ActiveTab() int      { return s.rightPane.active }
func (s *State) ZoomPercent() int    { return s.renderView.ZoomPercent() }
func (s *State) SelectedScheme() int { return s.schemePicker.Selected }

// MinimapSegments returns how many coloured token segments the minimap paints
// for the current buffer (proving it renders per-token rows, not one solid bar
// per line). It refreshes the overview inputs first, so it is valid after layout.
func (s *State) MinimapSegments() int {
	spans := s.hl.Highlight("latex", s.editor.Lines, s.theme)
	s.minimap.update(s.editor.Lines, spans, s.editor.ScrollLine, s.visibleEditorLines())
	return s.minimap.segmentCount(s.theme.OnSurface)
}

// DividerX is the surface x of the resize grip's leading edge.
func (s *State) DividerX() int { return s.paned.Bounds().X + s.paned.Position }

// DebugRects returns the DEVICE-pixel [x,y,w,h] rectangles of the interactive
// targets a headless verification harness needs to click precisely (the colour
// scheme picker and its open popover, the two right-pane tabs, the render zoom
// buttons and the render content area). It is host-facing introspection only.
func (s *State) DebugRects() map[string][4]int {
	rect := func(r toolkit.Rect) [4]int { return [4]int{r.X, r.Y, r.W, r.H} }
	tb := s.rightPane.tabs.Bounds()
	half := tb.W / 2
	rc := s.renderView.contentRect()
	return map[string][4]int{
		"picker":        rect(s.schemePicker.Bounds()),
		"popover":       rect(s.schemePicker.PopoverBounds()),
		"renderTab":     {tb.X, tb.Y, half, tb.H},
		"logTab":        {tb.X + half, tb.Y, tb.W - half, tb.H},
		"zoomOut":       rect(s.renderView.zoomOut.Bounds()),
		"zoomIn":        rect(s.renderView.zoomIn.Bounds()),
		"fit":           rect(s.renderView.fitBtn.Bounds()),
		"renderContent": rect(rc),
	}
}

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

// layoutToolbar places the colour-scheme picker and the minimap toggle
// left-to-right in the toolbar row (the former standalone "Log" toggle is now a
// tab in the right pane).
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
		s.renderView.SetContent(res.pixels, res.w, res.h)
	} else {
		s.renderView.SetContent(nil, 0, 0)
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

	// Body: editor + right pane (tabs over render|log) + handle, then the
	// minimap overlay.
	s.paned.Draw(p, s.theme)
	if s.showMinimap && s.minimap.Bounds().W > 0 {
		spans := s.hl.Highlight("latex", s.editor.Lines, s.theme)
		s.minimap.update(s.editor.Lines, spans, s.editor.ScrollLine, s.visibleEditorLines())
		s.minimap.Draw(p, s.theme)
	}
	if s.errText != "" && !s.showLog() {
		s.drawError(p)
	}
	s.status.Draw(p, s.theme)

	// Popover floats above everything.
	if s.schemePicker.Open {
		s.schemePicker.DrawPopover(p, s.theme)
	}
	s.dirty = false
}

// drawError overlays the compile error at the top of the render content area
// (below the tab strip and the render's own zoom toolbar).
func (s *State) drawError(p painter.Painter) {
	r := s.renderView.contentRect()
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
		for _, w := range []toolkit.Widget{s.schemePicker, s.minimapBtn} {
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

	// Right pane (tab strip over the render view or the Log view).
	rr := s.rightPane.Bounds()
	if rr.Contains(x, y) {
		s.rightPane.click(x, y)
		// App-level link: a click on the render content jumps the caret to the
		// nearest source line (ignored on the tab strip, the zoom toolbar or the
		// scrollbar gutter, where baseContentYAt reports not-ok).
		if !s.showLog() {
			if baseY, ok := s.renderView.baseContentYAt(x, y); ok {
				s.navRenderToEditorAt(baseY)
			}
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
		s.rightPane.drag(x, y)
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
		s.rightPane.release(x, y)
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
	rr := s.rightPane.Bounds()
	if rr.Contains(x, y) {
		s.rightPane.scrollWheel(x, y, delta)
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
	// first paint (Draw also refreshes it every frame). The pointer mapping needs
	// only the line count, so the per-line spans are left nil here.
	s.minimap.update(s.editor.Lines, nil, s.editor.ScrollLine, s.visibleEditorLines())
	s.editor.ScrollLine = s.minimap.lineAtY(y) // lineAtY already clamps to [0, n-1]
}

// scrollRenderToLine scrolls the render pane so the band for a 1-based source
// line is visible. No-op when the Log tab is active or the line has no band.
func (s *State) scrollRenderToLine(line int) {
	if s.showLog() {
		return
	}
	band, ok := s.lineBands[line]
	if !ok {
		return
	}
	s.renderView.scrollToBaseY(band[0])
}

// navRenderToEditorAt maps a BASE-render content Y (from a render click) to the
// nearest source line and moves the editor caret there.
func (s *State) navRenderToEditorAt(baseY int) {
	line := s.lineAt(baseY)
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
