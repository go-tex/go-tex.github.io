// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package playground is the go-tex WebAssembly LaTeX playground rendered as a
// single go-widgets/toolkit canvas application: a horizontal split of a
// CodeEditor (line numbers, LaTeX syntax colours, current-line highlight) on
// the left and a live, scrollable pane of the pure-Go gotex render on the
// right, with a status bar (caret position, encoding, page count) and
// click-to-source navigation in both directions.
//
// The whole View + logic lives here as a plain, tagless package so a native
// `go test` exercises construction, layout, compile, rasterize, event dispatch
// and draw against an RGBA buffer via go-widgets/painter — no browser needed.
// The js/wasm canvas driver (cmd/playground-wasm) is the only build-tagged
// file; it forwards DOM input into these handlers and blits Draw's buffer.
package playground

import (
	"strconv"

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

// statusBarH is the height of the bottom status bar.
const statusBarH = toolkit.StatusbarH

// State is the whole playground: the widget tree, the current theme, the last
// compile result and the small amount of host bookkeeping (dirty flag, a
// pending-compile latch the debounced driver drains). It is created with
// NewState and driven through its Handle* / Draw methods.
type State struct {
	w, h  int
	theme *toolkit.Theme
	dark  bool

	editor       *toolkit.CodeEditor
	renderImg    *toolkit.Image
	renderScroll *toolkit.ScrollView
	paned        *toolkit.Paned
	status       *toolkit.Statusbar

	// last compile output, kept so Draw + click navigation can consult the
	// per-line bands without recompiling.
	lineBands map[int][2]int
	errText   string
	pages     int

	// dirty is raised whenever a repaint is needed; the driver reads and
	// clears it. pendingCompile is latched by an editor edit and drained by
	// the driver's debounce (TakePendingCompile) so a burst of keystrokes
	// compiles once.
	dirty          bool
	pendingCompile bool

	// OnCompileNeeded, when set, is called after an edit so the host can
	// schedule a debounced Compile. Nil in tests, which call Compile directly.
	OnCompileNeeded func()

	// OnEdit, when set, is called synchronously on every editor change with the
	// full buffer text — BEFORE (and independent of) any compile scheduling — so
	// the host can persist the document (e.g. to localStorage) even if the next
	// compile errors. Nil in tests.
	OnEdit func(text string)
}

// NewState builds the playground at w×h logical pixels, dark or light, compiles
// the sample document once and returns the ready scene.
func NewState(w, h int, dark bool) *State {
	_ = toolkit.UseOpenTypeText() // crisp shaped text; harmless if it fails

	s := &State{w: w, h: h, dark: dark, lineBands: map[int][2]int{}}
	s.setTheme(dark)

	s.editor = toolkit.NewCodeEditor(SampleLaTeX)
	s.editor.Language = "latex"
	s.editor.Syntax = latexhl.New()
	s.editor.Focused = true
	s.editor.OnChange = func() {
		// Persist first, independent of the compile path, so an edit survives a
		// reload even when the following compile errors.
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
	s.paned = toolkit.NewHPaned(s.editor, s.renderScroll)

	s.status = toolkit.NewStatusbar([]string{"Ln 1, Col 1", "UTF-8", "0 pages"})

	s.layout()
	s.Compile()
	return s
}

// setTheme picks the light or dark toolkit theme.
func (s *State) setTheme(dark bool) {
	s.dark = dark
	if dark {
		s.theme = toolkit.DefaultDark()
	} else {
		s.theme = toolkit.DefaultLight()
	}
}

// SetTheme switches the palette and recompiles so the render pane's paper/ink
// follow the new theme. Raises dirty.
func (s *State) SetTheme(dark bool) {
	s.setTheme(dark)
	s.Compile()
	s.dirty = true
}

// Resize re-lays out the widgets to a new surface size and repaints.
func (s *State) Resize(w, h int) {
	s.w, s.h = w, h
	s.layout()
	s.dirty = true
}

// Size returns the current surface size.
func (s *State) Size() (int, int) { return s.w, s.h }

// Source returns the current editor buffer.
func (s *State) Source() string { return s.editor.Text() }

// SetSource replaces the editor buffer (e.g. restoring a persisted document at
// startup), resets the caret and recompiles. It does NOT fire OnEdit, so a
// restore is not mistaken for a user edit.
func (s *State) SetSource(text string) {
	s.editor.SetText(text)
	s.Compile()
	s.dirty = true
}

// Dirty reports whether a repaint is pending; ClearDirty resets it.
func (s *State) Dirty() bool           { return s.dirty }
func (s *State) ClearDirty()           { s.dirty = false }
func (s *State) markClean()            { s.dirty = false }
func (s *State) Theme() *toolkit.Theme { return s.theme }

// TakePendingCompile returns true (once) when an edit has requested a compile,
// clearing the latch. The debounced driver calls it when its timer fires.
func (s *State) TakePendingCompile() bool {
	if s.pendingCompile {
		s.pendingCompile = false
		return true
	}
	return false
}

// layout places the split (editor | render) above the status bar.
func (s *State) layout() {
	bodyH := s.h - statusBarH
	if bodyH < 0 {
		bodyH = 0
	}
	s.paned.SetBounds(toolkit.Rect{X: 0, Y: 0, W: s.w, H: bodyH})
	s.status.SetBounds(toolkit.Rect{X: 0, Y: bodyH, W: s.w, H: statusBarH})
}

// Compile runs the engine on the editor buffer, updates the render pane image,
// the per-line bands and the status bar, and raises dirty.
func (s *State) Compile() {
	res := compileLaTeX(s.editor.Text(), s.theme)
	s.errText = res.errText
	s.pages = res.pages
	s.lineBands = res.lineBands // compileLaTeX always returns a non-nil map

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

// updateStatus refreshes the three status-bar segments from the caret and the
// last compile.
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
}

// editorRect / renderRect are the current bounds of the two panes.
func (s *State) editorRect() toolkit.Rect { return s.paned.First.Bounds() }
func (s *State) renderRect() toolkit.Rect { return s.paned.Second.Bounds() }

// Draw paints the whole scene onto buf (RGBA, row-major, w*h*4).
func (s *State) Draw(buf []byte) {
	fillRGBA(buf, s.theme.Background)
	p := painter.NewPixelPainter(buf, s.w, s.h)
	s.paned.Draw(p, s.theme)
	if s.errText != "" {
		s.drawError(p)
	}
	s.status.Draw(p, s.theme)
	s.markClean()
}

// drawError overlays the compile error message at the top of the render pane.
func (s *State) drawError(p painter.Painter) {
	r := s.renderRect()
	band := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: 22}
	p.FillRect(band, s.theme.SurfaceAlt)
	p.Text(r.X+8, r.Y+7, "Error: "+s.errText, s.theme.Accent)
}

// --- input --------------------------------------------------------------

// HandleClick routes a click. In the editor it moves the caret (and scrolls the
// render pane to the caret's source line); in the render pane it maps the click
// to a source line and moves the editor caret there.
func (s *State) HandleClick(x, y int) bool {
	er := s.editorRect()
	rr := s.renderRect()
	switch {
	case er.Contains(x, y):
		s.editor.Focused = true
		s.editor.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - er.X, Y: y - er.Y})
		s.scrollRenderToLine(s.editor.CursorLine + 1)
		s.updateStatus()
		s.dirty = true
		return true
	case rr.Contains(x, y):
		s.navRenderToEditor(x, y)
		s.dirty = true
		return true
	}
	return false
}

// HandleScroll forwards a wheel scroll (delta in rows) to whichever pane is
// under the pointer.
func (s *State) HandleScroll(x, y, delta int) bool {
	rr := s.renderRect()
	if rr.Contains(x, y) {
		s.renderScroll.Scroll(0, delta*rowStep)
		s.dirty = true
		return true
	}
	er := s.editorRect()
	if er.Contains(x, y) {
		e := toolkit.Event{Kind: toolkit.EventScroll, X: x - er.X, Y: y - er.Y, Delta: delta}
		s.editor.OnEvent(e)
		s.dirty = true
		return true
	}
	return false
}

// rowStep is the pixel distance one wheel row scrolls the render pane.
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

// HandleKeyDown routes a named key (arrows, Backspace, Enter, …) to the editor.
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

// HandleMove / HandleRelease are hover/drag hooks kept for a uniform driver.
func (s *State) HandleMove(x, y int) bool    { _, _ = x, y; return false }
func (s *State) HandleRelease(x, y int) bool { _, _ = x, y; return false }

// scrollRenderToLine scrolls the render pane so the band for the given 1-based
// source line is visible near the top of the viewport. No-op when the line has
// no rendered band.
func (s *State) scrollRenderToLine(line int) {
	band, ok := s.lineBands[line]
	if !ok {
		return
	}
	rr := s.renderRect()
	target := band[0] - rr.H/3 // keep some context above
	if target < 0 {
		target = 0
	}
	s.renderScroll.Scroll(0, target-s.renderScroll.OffsetY)
}

// navRenderToEditor maps a click at surface (x, y) in the render pane to the
// nearest source line (via the per-line bands) and moves the editor caret to
// its start.
func (s *State) navRenderToEditor(x, y int) {
	rr := s.renderRect()
	contentY := (y - rr.Y) + s.renderScroll.OffsetY
	line := s.lineAt(contentY)
	if line <= 0 {
		return
	}
	target := line - 1 // line >= 1 here (lineAt returned a band key)
	if target >= len(s.editor.Lines) {
		target = len(s.editor.Lines) - 1
	}
	s.editor.CursorLine = target
	s.editor.CursorCol = 0
	s.editor.Focused = true
	s.updateStatus()
}

// lineAt returns the 1-based source line whose band contains contentY, or the
// nearest band's line when contentY falls in a gap. Returns 0 when there are no
// bands.
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
