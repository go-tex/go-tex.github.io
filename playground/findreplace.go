// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// findReplace wires the toolkit's UI-only FindReplace bar (toolkit v0.252.0) to
// the playground's Source CodeEditor. The bar itself holds the query, the
// replacement, the search toggles and the count readout and fires callbacks; it
// never runs a regexp. This wrapper is the host that owns the buffer: it runs
// toolkit.FindMatches over the editor lines on every query/toggle change, pushes
// the count back with SetMatches/SetInvalid and drives the editor's match
// highlight API (SetMatchHighlights / SetCurrentMatch / ScrollToMatch), and
// implements Prev/Next (with wrap) and Replace / Replace-all over the buffer.
//
// It is deliberately self-contained (the same shape as collab/git/sidebar): the
// only app.go hooks are the toolbar toggle button, a ⌘F/Ctrl+F keybind and the
// draw/click/char/key routing while the bar is visible.
//
// WYSIWYG scope: only the Source CodeEditor is wired. The WYSIWYG RichEditor has
// no match-highlight API (its model is block/rich-document based, not the
// line/(line,col) model FindMatches and SetMatchHighlights speak), so WYSIWYG
// find is deferred rather than balloon this change with a new toolkit surface.
type findReplace struct {
	s  *State
	fr *toolkit.FindReplace

	// matches is the current query's occurrence set (each a single-line
	// Selection in rune coordinates, as FindMatches returns); current is the
	// 0-based index of the active match within it, or -1 when there is none.
	matches []toolkit.Selection
	current int
}

// newFindReplace builds the hidden find bar and wires its callbacks to this
// host. The bar starts closed (Open is what shows it); regex mode is on by
// default (the toolkit's default), matching the "enter a regexp" brief.
func newFindReplace(s *State) *findReplace {
	f := &findReplace{s: s, fr: toolkit.NewFindReplace(), current: -1}
	// The query text and every toggle re-run the search from the first match;
	// Prev/Next step it; Replace / Replace-all mutate the buffer and re-search;
	// Close (the ✕ button or Escape) clears the highlights.
	f.fr.OnQueryChange = func() { f.search(false) }
	f.fr.OnNext = func() { f.step(1) }
	f.fr.OnPrev = func() { f.step(-1) }
	f.fr.OnReplace = f.replace
	f.fr.OnReplaceAll = f.replaceAll
	f.fr.OnClose = f.clearSearch
	return f
}

// visible reports whether the bar is currently shown.
func (f *findReplace) visible() bool { return f.fr.Visible().Get() }

// layout anchors the bar over the Source editor pane, so its top-right panel
// floats above the code the highlighted matches sit in.
func (f *findReplace) layout() {
	pr := f.s.paned.Bounds()
	leftW := f.s.paned.Position().Get()
	f.fr.SetBounds(toolkit.Rect{X: pr.X, Y: pr.Y, W: leftW, H: pr.H})
}

// toggle opens the bar (and re-runs the current query) or closes it (clearing
// the highlights) — the toolbar button and ⌘F/Ctrl+F path.
func (f *findReplace) toggle() {
	if f.visible() {
		f.close()
	} else {
		f.open()
	}
}

// open shows the bar, moves focus to the query field (Open does both) and runs
// the current query so any highlights are restored immediately.
func (f *findReplace) open() {
	f.fr.Open()
	f.search(false)
}

// close hides the bar and clears its highlights. Unlike the ✕/Escape path
// (which fires OnClose → clearSearch itself), a programmatic Close does not fire
// OnClose, so clearSearch is called here explicitly.
func (f *findReplace) close() {
	f.fr.Close()
	f.clearSearch()
}

// clearSearch drops the match set and wipes the editor's highlight overlay — the
// "search closed / dismissed" reset. It is also the OnClose callback.
func (f *findReplace) clearSearch() {
	f.matches = nil
	f.current = -1
	f.s.editor.ClearMatchHighlights()
	f.s.dirty = true
}

// search runs FindMatches over the current editor buffer for the current query
// and pushes the result to both the bar (count/invalid) and the editor
// (highlights + current match). keepIndex preserves the active index (clamped)
// across a re-search after a replace; a fresh query passes false so the current
// match resets to the first.
func (f *findReplace) search(keepIndex bool) {
	lines := strings.Split(f.s.editor.Text().Get(), "\n")
	matches, err := toolkit.FindMatches(lines, f.fr.Query().Get(), f.fr.Options())
	if err != nil {
		// An invalid pattern: mark the bar, drop the highlights, keep no matches.
		f.matches = nil
		f.current = -1
		f.fr.SetInvalid(true)
		f.s.editor.ClearMatchHighlights()
		f.s.dirty = true
		return
	}
	f.matches = matches
	f.s.editor.SetMatchHighlights(matches)
	f.applyCurrent(keepIndex)
	f.s.dirty = true
}

// applyCurrent selects the active match after a search: none when the set is
// empty, else index 0 (a fresh query) or the clamped previous index (a
// re-search that keeps its place). It updates the bar's count and the editor's
// current-match emphasis + scroll.
func (f *findReplace) applyCurrent(keepIndex bool) {
	if len(f.matches) == 0 {
		f.current = -1
		f.fr.SetMatches(0, -1)
		f.s.editor.SetCurrentMatch(toolkit.Selection{})
		return
	}
	cur := 0
	if keepIndex {
		cur = f.current
		if cur < 0 {
			cur = 0
		}
		if cur >= len(f.matches) {
			cur = len(f.matches) - 1
		}
	}
	f.current = cur
	f.fr.SetMatches(len(f.matches), cur)
	f.s.editor.SetCurrentMatch(f.matches[cur])
	f.s.editor.ScrollToMatch(f.matches[cur])
}

// step advances (delta +1) or rewinds (delta -1) the current match with wrap
// around the ends, updating the bar count and the editor emphasis + scroll.
func (f *findReplace) step(delta int) {
	n := len(f.matches)
	if n == 0 {
		return
	}
	f.current = ((f.current+delta)%n + n) % n
	f.fr.SetMatches(n, f.current)
	f.s.editor.SetCurrentMatch(f.matches[f.current])
	f.s.editor.ScrollToMatch(f.matches[f.current])
	f.s.dirty = true
}

// replace substitutes the current match with the replacement text, then
// re-searches keeping the index so the bar lands on the following occurrence.
//
// Replacement is LITERAL: the matched span is swapped for the replacement string
// verbatim, with no regexp group ($1) expansion, whether or not regex mode is on
// (v1 scope — group expansion is deliberately left out to keep single-match and
// replace-all behaviour identical and predictable).
func (f *findReplace) replace() {
	if f.current < 0 || f.current >= len(f.matches) {
		return
	}
	newText := replaceSpans(f.s.editor.Text().Get(), f.matches[f.current:f.current+1], f.fr.Replace().Get())
	f.s.editor.SetText(newText)
	f.search(true)
}

// replaceAll substitutes every match with the replacement text (literal, as in
// replace) in one edit, then re-searches.
func (f *findReplace) replaceAll() {
	if len(f.matches) == 0 {
		return
	}
	newText := replaceSpans(f.s.editor.Text().Get(), f.matches, f.fr.Replace().Get())
	f.s.editor.SetText(newText)
	f.search(true)
}

// draw paints the bar (a no-op while hidden).
func (f *findReplace) draw(p painter.Painter, theme *toolkit.Theme) {
	f.fr.Draw(p, theme)
}

// handleClick routes a pointer press to the bar when it lands on one of the
// bar's controls, returning whether it was consumed. A click on the editor
// beneath the (docked, non-modal) bar falls through so the editor still works.
func (f *findReplace) handleClick(x, y int) bool {
	if !f.visible() {
		return false
	}
	b := f.fr.Bounds()
	for _, w := range f.fr.Children() {
		if w.Bounds().Contains(x, y) {
			f.fr.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.X, Y: y - b.Y})
			f.s.dirty = true
			return true
		}
	}
	return false
}

// handleChar routes a typed character to the bar's focused field while visible.
func (f *findReplace) handleChar(code string) bool {
	if !f.visible() {
		return false
	}
	f.fr.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: code})
	f.s.dirty = true
	return true
}

// handleKey routes a named key to the bar while visible: the bar itself acts on
// Escape (close), Enter (next) and Shift+Enter (previous) and forwards the rest
// (Backspace, arrows) to the focused field. A "Shift+"-prefixed code is split
// back into a bare code + the Shift modifier the bar reads.
func (f *findReplace) handleKey(code string) bool {
	if !f.visible() {
		return false
	}
	shift := false
	if strings.HasPrefix(code, "Shift+") {
		shift = true
		code = code[len("Shift+"):]
	}
	f.fr.OnEvent(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code, Shift: shift})
	f.s.dirty = true
	return true
}

// replaceSpans returns text with each of spans replaced by repl. Every span is a
// single-line rune range (as FindMatches yields); spans are applied in reverse
// document order so replacing one never shifts the columns of an earlier one on
// the same line. Out-of-range or inverted spans are skipped.
func replaceSpans(text string, spans []toolkit.Selection, repl string) string {
	if len(spans) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		if sp.StartLine < 0 || sp.StartLine >= len(lines) {
			continue
		}
		runes := []rune(lines[sp.StartLine])
		start, end := sp.StartCol, sp.EndCol
		if start < 0 {
			start = 0
		}
		if end > len(runes) {
			end = len(runes)
		}
		if start > end {
			continue
		}
		lines[sp.StartLine] = string(runes[:start]) + repl + string(runes[end:])
	}
	return strings.Join(lines, "\n")
}
