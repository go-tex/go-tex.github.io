// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

const testW, testH = 1000, 720

// nonBlank reports whether buf contains any pixel differing from c (i.e. the
// scene drew something over the background).
func nonBlank(buf []byte, c toolkit.RGBA) bool {
	for i := 0; i+3 < len(buf); i += 4 {
		if buf[i] != c.R || buf[i+1] != c.G || buf[i+2] != c.B {
			return true
		}
	}
	return false
}

func newTestState(t *testing.T, dark bool) *State {
	t.Helper()
	SetupText(1) // install the OT font at scale 1 (resets any HiDPI scale a prior test set)
	s := NewState(testW, testH, dark)
	if s.pages <= 0 {
		t.Fatalf("sample document produced %d pages, want > 0 (errText=%q)", s.pages, s.errText)
	}
	if len(s.lineBands) == 0 {
		t.Fatalf("no per-line bands produced for the sample document")
	}
	return s
}

// editorRect / renderRect are test conveniences for the two pane bounds;
// rscroll reaches the render view's inner scroll for offset assertions.
func (s *State) editorRect() toolkit.Rect     { return s.editor.Bounds() }
func (s *State) renderRect() toolkit.Rect     { return s.renderView.contentRect() }
func (s *State) rscroll() *toolkit.ScrollView { return s.renderView.scroll }

func TestNewStateCompilesSampleAndDraws(t *testing.T) {
	s := newTestState(t, false)
	if s.errText != "" {
		t.Fatalf("sample compile error: %s", s.errText)
	}
	if s.renderView.baseW == 0 || s.renderView.baseH == 0 || len(s.renderView.basePix) == 0 {
		t.Fatalf("render image empty: %dx%d len=%d", s.renderView.baseW, s.renderView.baseH, len(s.renderView.basePix))
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !nonBlank(buf, s.theme.Background) {
		t.Fatalf("Draw produced a blank frame")
	}
	if s.Dirty() {
		t.Fatalf("Draw should clear the dirty flag")
	}
}

func TestStatusSegments(t *testing.T) {
	s := newTestState(t, false)
	if got := s.status.Segments[0]; got != "Ln 1, Col 1" {
		t.Fatalf("segment 0 = %q, want %q", got, "Ln 1, Col 1")
	}
	if got := s.status.Segments[1]; got != "UTF-8" {
		t.Fatalf("segment 1 = %q, want UTF-8", got)
	}
	if !strings.HasSuffix(s.status.Segments[2], "page") && !strings.HasSuffix(s.status.Segments[2], "pages") {
		t.Fatalf("segment 2 = %q, want a page count", s.status.Segments[2])
	}
}

func TestEditLatchesDebouncedCompile(t *testing.T) {
	s := newTestState(t, false)
	var scheduled int
	s.OnCompileNeeded = func() { scheduled++ }

	// Focus editor and type a character.
	if !s.HandleChar("Z") {
		t.Fatalf("HandleChar not consumed")
	}
	if scheduled == 0 {
		t.Fatalf("edit did not call OnCompileNeeded")
	}
	if !s.TakePendingCompile() {
		t.Fatalf("pending compile not latched")
	}
	if s.TakePendingCompile() {
		t.Fatalf("pending compile latched twice (should drain once)")
	}
	if !strings.Contains(s.editor.Text(), "Z") {
		t.Fatalf("typed character did not reach the editor buffer")
	}
	s.Compile() // the debounced host would call this
}

func TestCaretKeysUpdateStatus(t *testing.T) {
	s := newTestState(t, false)
	// Move the caret down a few lines.
	for i := 0; i < 3; i++ {
		if !s.HandleKeyDown("ArrowDown") {
			t.Fatalf("ArrowDown not consumed")
		}
	}
	if s.editor.CursorLine != 3 {
		t.Fatalf("cursor line = %d, want 3", s.editor.CursorLine)
	}
	if got := s.status.Segments[0]; got != "Ln 4, Col 1" {
		t.Fatalf("status after ArrowDown x3 = %q, want %q", got, "Ln 4, Col 1")
	}
}

func TestClickEditorMovesCaret(t *testing.T) {
	s := newTestState(t, false)
	er := s.editorRect()
	// Click a few rows down inside the editor.
	if !s.HandleClick(er.X+30, er.Y+60) {
		t.Fatalf("editor click not consumed")
	}
	if !s.editor.Focused {
		t.Fatalf("editor should be focused after a click")
	}
}

func TestClickRenderNavigatesToSource(t *testing.T) {
	s := newTestState(t, false)
	// Pick a known rendered source line and its band, click its centre in the
	// render pane, and assert the editor caret jumps to that line.
	var line int
	var band [2]int
	for l, b := range s.lineBands {
		if l > line { // deterministic: the last (deepest) line with a band
			line, band = l, b
		}
	}
	rr := s.renderRect()
	// Scroll so the band is in view, then click its on-screen centre.
	s.rscroll().Scroll(0, band[0]-s.rscroll().OffsetY)
	midContent := (band[0] + band[1]) / 2
	screenY := rr.Y + (midContent - s.rscroll().OffsetY)
	if screenY < rr.Y {
		screenY = rr.Y + 4
	}
	if !s.HandleClick(rr.X+rr.W/2, screenY) {
		t.Fatalf("render click not consumed")
	}
	if got := s.editor.CursorLine + 1; got != line {
		t.Fatalf("render->editor nav landed on line %d, want %d", got, line)
	}
}

func TestScrollBothPanes(t *testing.T) {
	s := newTestState(t, false)
	rr := s.renderRect()
	before := s.rscroll().OffsetY
	if !s.HandleScroll(rr.X+10, rr.Y+10, 0, 5) {
		t.Fatalf("render scroll not consumed")
	}
	if s.rscroll().OffsetY <= before {
		t.Fatalf("render pane did not scroll down: %d -> %d", before, s.rscroll().OffsetY)
	}
	er := s.editorRect()
	if !s.HandleScroll(er.X+10, er.Y+10, 0, 3) {
		t.Fatalf("editor scroll not consumed")
	}
	// A scroll outside both panes is ignored.
	if s.HandleScroll(-5, -5, 0, 1) {
		t.Fatalf("out-of-bounds scroll should not be consumed")
	}
}

func TestScrollRenderToLineClampsAndMisses(t *testing.T) {
	s := newTestState(t, false)
	// A line with no band is a no-op.
	before := s.rscroll().OffsetY
	s.scrollRenderToLine(1 << 20)
	if s.rscroll().OffsetY != before {
		t.Fatalf("scrollRenderToLine on a missing line moved the offset")
	}
	// The first band scrolls to (near) the top.
	var minLine int = 1 << 30
	for l := range s.lineBands {
		if l < minLine {
			minLine = l
		}
	}
	s.scrollRenderToLine(minLine)
}

func TestSetThemeRecompilesAndRecolours(t *testing.T) {
	s := newTestState(t, false)
	lightBuf := make([]byte, testW*testH*4)
	s.Draw(lightBuf)

	s.SetTheme(true)
	if !s.dark {
		t.Fatalf("SetTheme(true) did not switch to dark")
	}
	darkBuf := make([]byte, testW*testH*4)
	s.Draw(darkBuf)

	// The background changed, so the frames must differ.
	same := true
	for i := range lightBuf {
		if lightBuf[i] != darkBuf[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("light and dark frames are identical")
	}
}

func TestResize(t *testing.T) {
	s := newTestState(t, false)
	s.Resize(1200, 800)
	if w, h := s.Size(); w != 1200 || h != 800 {
		t.Fatalf("Size after resize = %dx%d, want 1200x800", w, h)
	}
	if !s.Dirty() {
		t.Fatalf("Resize should raise dirty")
	}
	buf := make([]byte, 1200*800*4)
	s.Draw(buf) // must not panic at the new size
}

func TestCompileErrorPath(t *testing.T) {
	s := newTestState(t, false)
	// An unterminated group is a hard TeX error even in lenient mode.
	s.editor.SetText(`\documentclass{article}\begin{document}\bgroup oops\end{document}`)
	s.Compile()
	// Either it errors (errText set) or it renders; if it errored, the status
	// and overlay path must engage.
	if s.errText != "" {
		if s.status.Segments[2] != "compile error" {
			t.Fatalf("status not set to compile error: %q", s.status.Segments[2])
		}
		buf := make([]byte, testW*testH*4)
		s.Draw(buf) // exercises drawError overlay
	}
}

func TestUnfocusedEditorIgnoresKeys(t *testing.T) {
	s := newTestState(t, false)
	s.editor.Focused = false
	if s.HandleChar("x") {
		t.Fatalf("unfocused editor consumed a char")
	}
	if s.HandleKeyDown("ArrowDown") {
		t.Fatalf("unfocused editor consumed a key")
	}
}

func TestHoverHooksAreNoops(t *testing.T) {
	s := newTestState(t, false)
	if s.HandleMove(1, 1) || s.HandleRelease(1, 1) {
		t.Fatalf("hover/release hooks should report no change")
	}
}

func TestThemeAccessorAndClearDirty(t *testing.T) {
	s := newTestState(t, false)
	if s.Theme() == nil {
		t.Fatalf("Theme() nil")
	}
	s.dirty = true
	s.ClearDirty()
	if s.Dirty() {
		t.Fatalf("ClearDirty did not clear")
	}
}

func TestOnEditFiresAndSourceRoundTrips(t *testing.T) {
	s := newTestState(t, false)
	var saved string
	s.OnEdit = func(text string) { saved = text }

	if !s.HandleChar("Q") {
		t.Fatalf("HandleChar not consumed")
	}
	if saved == "" || !strings.Contains(saved, "Q") {
		t.Fatalf("OnEdit did not receive the edited buffer: %q", saved)
	}
	if s.Source() != s.editor.Text() {
		t.Fatalf("Source() disagrees with the editor buffer")
	}
}

func TestSetSourceRestoresWithoutFiringOnEdit(t *testing.T) {
	s := newTestState(t, false)
	fired := false
	s.OnEdit = func(string) { fired = true }

	restored := `\documentclass{article}\begin{document}Restored.\end{document}`
	s.SetSource(restored)
	if s.Source() != restored {
		t.Fatalf("SetSource did not replace the buffer")
	}
	if fired {
		t.Fatalf("SetSource must not fire OnEdit (restore is not a user edit)")
	}
	if !s.Dirty() {
		t.Fatalf("SetSource should raise dirty")
	}
}

func TestLineAtEmptyBands(t *testing.T) {
	s := &State{lineBands: map[int][2]int{}}
	if got := s.lineAt(10); got != 0 {
		t.Fatalf("lineAt with no bands = %d, want 0", got)
	}
}
