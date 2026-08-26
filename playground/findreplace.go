// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"fmt"
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// This file is the playground's regex find-and-replace affordance, housed in a
// MODAL window (toolkit v0.254.0 [toolkit.ModalWindow] / [toolkit.NewSearchModal])
// that works over WHICHEVER editor is active — the Source [toolkit.CodeEditor] or
// the WYSIWYG [toolkit.RichEditor].
//
// # The modal
//
// [toolkit.NewSearchModal] builds a dimming, click-catching scrim over the whole
// surface with a centred [toolkit.Dialog] panel on top. The panel's TOP INPUT BAR
// is the regex query (a focused [toolkit.SearchEntry], returned as `query`); its
// BODY carries the occurrence-count read-out and the replacement field; its ACTION
// STRIP carries Previous / Next / Replace / Replace all; and its × close button —
// like Escape and a click on the scrim — dismisses it (clearing the highlights)
// through the modal's single OnClose hook. The panel is positioned over the LOWER
// portion of the editor pane so the current match, scrolled into the top of the
// viewport, reads ABOVE the card rather than under it.
//
// # The active-editor abstraction
//
// A find/replace host is editor-agnostic: it runs [toolkit.FindMatches] over the
// active editor's plain-text projection and pushes the ranges back through the
// [toolkit.MatchHighlighter] surface both editors share. [searchTarget] captures
// exactly that seam. Every match is a [toolkit.Selection] (FindMatches' native
// coordinate): for the Source editor a (line, col) range over the line buffer, for
// the WYSIWYG editor a (block, off) range over [toolkit.RichEditor.BlockTexts] that
// [toolkit.DocSelectionsFromMatches] folds into the [toolkit.DocSelection] ranges
// the RichEditor highlights. [findReplace.target] picks the implementation from the
// active editor tab, so the SAME search / next / prev / replace logic drives both.
//
// # Replace semantics
//
// Replacement is LITERAL on both editors: the matched span is swapped for the
// replacement string verbatim, with no regexp group ($1) expansion, whether or not
// the pattern is a regex (v1 scope — group expansion is left out to keep Replace
// and Replace-all identical and predictable, exactly as #53 did for the Source
// editor). On the Source editor the spans are substituted in the line buffer
// (reverse document order, so an earlier span's columns stay valid); on the WYSIWYG
// editor each match's [toolkit.DocSelection] is deleted and the replacement typed
// in its place, again in reverse order per block.
//
// # WYSIWYG search scope (v1 limitation)
//
// The WYSIWYG search runs over [toolkit.RichEditor.BlockTexts], the editable
// plain-text projection of the document's blocks. A non-editable / atomic corner of
// the rich model — a table cell, a math or raw block, an inline atom (image, hard
// break) — projects to an empty string or a single object-replacement position, so
// its inner text is NOT searchable in v1 (an accepted limitation per the v0.253.0
// notes); paragraphs, headings and code blocks, which is the bulk of a LaTeX
// document, search and replace in full.

// findPanelW / findPanelH are the modal panel's preferred size in LOGICAL pixels
// (metric-scaled by the toolkit). The width is chosen so the four action buttons
// (Previous / Next / Replace / Replace all) fit the [toolkit.Dialog] bottom strip
// without crowding; the height leaves the title bar, the query input strip, the
// count + replace body rows and the button strip each a comfortable band.
const (
	findPanelW = 460
	findPanelH = 210
)

// findBottomMargin is the gap in LOGICAL pixels between the modal panel's bottom
// edge and the bottom of the editor pane, so the panel sits low in the pane and
// the current match (scrolled toward the top) reads above it.
const findBottomMargin = 14

// findSearchOptions is how the query bar's pattern is interpreted: a Go regular
// expression (the "enter a regexp" brief), case-insensitively. Regex mode is what
// makes an unbalanced "(" an INVALID pattern (the panel's Bad-pattern state) rather
// than a literal to match.
var findSearchOptions = toolkit.SearchOptions{Regex: true}

// searchTarget is the active editor as a find/replace subject, in FindMatches'
// native [toolkit.Selection] coordinate. It abstracts over the Source CodeEditor
// (line/col) and the WYSIWYG RichEditor (block/off, via DocSelection), so the
// find logic is written once and [findReplace.target] chooses the implementation.
type searchTarget interface {
	// lines is the plain-text projection FindMatches runs over: the Source editor's
	// line buffer, or the RichEditor's per-block text. A match's StartLine is a line
	// index on the Source editor and a block index on the WYSIWYG editor.
	lines() []string
	// setMatches pushes the soft-highlight occurrence set (converting coordinates).
	setMatches(ms []toolkit.Selection)
	// setCurrent emphasises one match; the zero [toolkit.Selection] clears it.
	setCurrent(m toolkit.Selection)
	// scrollTo reveals m, centring it only when it is out of view.
	scrollTo(m toolkit.Selection)
	// clear drops the soft highlights AND the current-match emphasis.
	clear()
	// replaceMatches substitutes each of ms with repl, literally, in the editor's
	// own model (ms is one match for Replace, all matches for Replace all).
	replaceMatches(ms []toolkit.Selection, repl string)
	// matchPoints is a device-pixel point inside each soft-highlight band, in order,
	// so the headless proof can sample the canvas and prove the highlights paint.
	matchPoints() [][2]int
}

// findReplace is the modal find-and-replace host. It owns the modal + its fields
// and buttons, runs the regexp over the active editor and drives the highlight /
// count / next-prev / replace state; app.go carries only the toolbar toggle, the
// ⌘F/Ctrl+F keybind and the draw/click/char/key routing while the modal is open.
type findReplace struct {
	s *State

	modal   *toolkit.ModalWindow
	query   *toolkit.SearchEntry // the top input bar (the regex field), pre-focused
	replace *toolkit.Entry       // the replacement field, in the panel body
	count   *toolkit.Label       // the occurrence-count read-out, in the panel body

	prevBtn, nextBtn          *toolkit.Button
	replaceBtn, replaceAllBtn *toolkit.Button

	// shown is whether the modal is currently displayed; focusRepl is whether the
	// keyboard focus is on the replacement field (else the query field).
	shown     bool
	focusRepl bool

	// invalid is whether the last query was an invalid regular expression, so the
	// count reads "Bad pattern" and the highlights are cleared.
	invalid bool

	// matches is the current query's occurrence set over the ACTIVE editor (each a
	// FindMatches [toolkit.Selection]); current is the 0-based active index, or -1.
	matches []toolkit.Selection
	current int
}

// newFindReplace builds the hidden modal and wires it to this host. The modal
// starts closed (open shows it); the query field is the modal's top input bar and
// drives the search live via its Text observable.
func newFindReplace(s *State) *findReplace {
	f := &findReplace{s: s, current: -1}

	// Panel body: the count read-out over the replacement field.
	f.count = toolkit.NewLabel("")
	f.replace = toolkit.NewEntry("")
	f.replace.Placeholder = "Replace with…"
	body := toolkit.NewVBox()
	body.AddFixed(f.count, toolkit.Scaled(22))
	body.AddFixed(f.replace, toolkit.Scaled(28))

	// Action strip: step + replace verbs.
	f.prevBtn = toolkit.NewButton("Previous", func() { f.step(-1) })
	f.nextBtn = toolkit.NewButton("Next", func() { f.step(1) })
	f.replaceBtn = toolkit.NewButton("Replace", f.replaceCurrent)
	f.replaceAllBtn = toolkit.NewButton("Replace all", f.replaceAll)

	m, se := toolkit.NewSearchModal("Find and replace", body, f.prevBtn, f.nextBtn, f.replaceBtn, f.replaceAllBtn)
	m.PanelW, m.PanelH = findPanelW, findPanelH
	m.OnClose = f.onModalClose // × / Escape / scrim → dismiss + clear highlights
	f.modal = m
	f.query = se

	// The query text (typing / Backspace / the clear affordance all Set it) re-runs
	// the search from the first match. The modal lives for the whole app, so the
	// unsubscribe handle is intentionally dropped.
	se.Text().Subscribe(func(string) { f.search(false) })
	return f
}

// target is the active editor as a searchTarget: the WYSIWYG RichEditor when the
// WYSIWYG tab is selected, else the Source CodeEditor. Every search / highlight /
// replace call routes through it, so a tab switch re-targets the whole feature.
func (f *findReplace) target() searchTarget {
	if f.s.wysiwyg().active() {
		return richTarget{f.s.wysiwyg().editor, f.s}
	}
	return sourceTarget{f.s.editor, f.s}
}

// visible reports whether the modal is currently shown.
func (f *findReplace) visible() bool { return f.shown }

// toggle opens the modal (restoring the current query's highlights) or closes it
// (clearing them) — the toolbar Find button and the ⌘F/Ctrl+F path.
func (f *findReplace) toggle() {
	if f.shown {
		f.close()
	} else {
		f.open()
	}
}

// open shows the modal, focuses the query field and runs the current query so any
// highlights are restored immediately.
func (f *findReplace) open() {
	f.shown = true
	f.focusQuery()
	f.search(false)
}

// close hides the modal and clears the highlights. A programmatic close does not
// pass through the modal's OnClose, so clearSearch is called here explicitly; the
// × / Escape / scrim paths fire OnClose → onModalClose, which does the same.
func (f *findReplace) close() {
	f.shown = false
	f.clearSearch()
}

// onModalClose is the modal's OnClose hook (× button, Escape, scrim click): hide
// and clear.
func (f *findReplace) onModalClose() {
	f.shown = false
	f.clearSearch()
}

// clearSearch drops the match set and wipes BOTH editors' highlight overlays (the
// user may have switched tabs while the modal was open, so the highlights could
// sit on either), then resets the count read-out — the "search closed / dismissed"
// reset.
func (f *findReplace) clearSearch() {
	f.matches = nil
	f.current = -1
	f.invalid = false
	f.s.editor.ClearMatchHighlights()
	f.s.wysiwyg().editor.ClearMatchHighlights()
	f.updateCount()
	f.s.dirty = true
}

// onEditorTabChanged re-targets the feature when the Source│WYSIWYG tab flips while
// the modal is open: it clears the editor being left and re-runs the current query
// against the now-active one, so the highlights and count follow the visible editor.
// A no-op while the modal is closed. Called from wysiwyg.enter / leave.
func (f *findReplace) onEditorTabChanged() {
	if !f.shown {
		return
	}
	f.s.editor.ClearMatchHighlights()
	f.s.wysiwyg().editor.ClearMatchHighlights()
	f.search(false)
}

// search runs FindMatches over the active editor's projection for the current
// query and pushes the result to the editor (highlights + current match) and the
// count read-out. keepIndex preserves the active index (clamped) across a
// re-search after a replace; a fresh query passes false so the current match
// resets to the first.
func (f *findReplace) search(keepIndex bool) {
	tgt := f.target()
	matches, err := toolkit.FindMatches(tgt.lines(), f.query.Text().Get(), findSearchOptions)
	if err != nil {
		// An invalid pattern: flag it, drop the highlights, keep no matches.
		f.matches = nil
		f.current = -1
		f.invalid = true
		tgt.clear()
		f.updateCount()
		f.s.dirty = true
		return
	}
	f.invalid = false
	f.matches = matches
	tgt.setMatches(matches)
	f.applyCurrent(keepIndex, tgt)
	f.updateCount()
	f.s.dirty = true
}

// applyCurrent selects the active match after a search: none when the set is empty,
// else index 0 (a fresh query) or the clamped previous index (a re-search that
// keeps its place). It updates the editor's current-match emphasis + scroll.
func (f *findReplace) applyCurrent(keepIndex bool, tgt searchTarget) {
	if len(f.matches) == 0 {
		f.current = -1
		tgt.setCurrent(toolkit.Selection{})
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
	tgt.setCurrent(f.matches[cur])
	tgt.scrollTo(f.matches[cur])
}

// step advances (delta +1) or rewinds (delta -1) the current match with wrap
// around the ends, updating the count and the editor emphasis + scroll.
func (f *findReplace) step(delta int) {
	n := len(f.matches)
	if n == 0 {
		return
	}
	f.current = ((f.current+delta)%n + n) % n
	tgt := f.target()
	tgt.setCurrent(f.matches[f.current])
	tgt.scrollTo(f.matches[f.current])
	f.updateCount()
	f.s.dirty = true
}

// replaceCurrent substitutes the current match with the replacement text, then
// re-searches keeping the index so the panel lands on the following occurrence.
func (f *findReplace) replaceCurrent() {
	if f.current < 0 || f.current >= len(f.matches) {
		return
	}
	f.target().replaceMatches(f.matches[f.current:f.current+1], f.replace.Text().Get())
	f.search(true)
}

// replaceAll substitutes every match with the replacement text in one pass, then
// re-searches.
func (f *findReplace) replaceAll() {
	if len(f.matches) == 0 {
		return
	}
	f.target().replaceMatches(f.matches, f.replace.Text().Get())
	f.search(true)
}

// countText is the occurrence read-out: the invalid-pattern state, the no-results
// state, or "<i> of <n>" for the current match. It is the single source the panel
// Label and the FindCountText introspection both take.
func (f *findReplace) countText() string {
	switch {
	case f.invalid:
		return "Bad pattern"
	case len(f.matches) == 0:
		return "No results"
	default:
		return fmt.Sprintf("%d of %d", f.current+1, len(f.matches))
	}
}

// updateCount refreshes the panel's count Label from countText.
func (f *findReplace) updateCount() { f.count.Text().Set(f.countText()) }

// layout spreads the scrim over the whole surface and positions the panel over the
// lower portion of the editor pane (see positionPanel). Called from State.layout
// and re-asserted each draw.
func (f *findReplace) layout() {
	f.modal.SetBounds(toolkit.Rect{X: 0, Y: 0, W: f.s.w, H: f.s.h})
	f.positionPanel()
}

// positionPanel bottom-anchors the modal card within the editor pane (below its
// vertical centre) so the current match, scrolled toward the top of the pane, reads
// above the panel rather than under it. The scrim still covers the whole surface;
// only the panel is moved off the default centre.
func (f *findReplace) positionPanel() {
	er := f.s.editor.Bounds() // the editor-pane region (both tabs lay out into it)
	w := toolkit.Scaled(findPanelW)
	h := toolkit.Scaled(findPanelH)
	if w > er.W {
		w = er.W
	}
	if h > er.H {
		h = er.H
	}
	x := er.X + (er.W-w)/2
	y := er.Y + er.H - h - toolkit.Scaled(findBottomMargin)
	if y < er.Y {
		y = er.Y
	}
	f.modal.Panel.SetBounds(toolkit.Rect{X: x, Y: y, W: w, H: h})
}

// draw paints the modal (scrim + panel) on top of the scene, re-asserting its
// bounds first. A no-op while the modal is hidden.
func (f *findReplace) draw(p painter.Painter, theme *toolkit.Theme) {
	if !f.shown {
		return
	}
	f.layout()
	f.modal.Draw(p, theme)
}

// handleClick routes a pointer press while the modal is open. The modal is modal:
// it consumes every click. A click outside the panel (on the scrim) dismisses it; a
// click on the query or replacement field moves focus there and forwards to it;
// every other click on the panel (the action buttons, the × close button, the
// title bar) is routed to the Dialog, whose own OnEvent fires the button under the
// pointer and dismisses on ×.
func (f *findReplace) handleClick(x, y int) bool {
	if !f.shown {
		return false
	}
	panel := f.modal.Panel.Bounds()
	if !panel.Contains(x, y) {
		f.onModalClose() // scrim click (CloseOnScrim)
		return true
	}
	if qb := f.query.Bounds(); qb.Contains(x, y) {
		f.focusQuery()
		f.query.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - qb.X, Y: y - qb.Y})
		f.s.dirty = true
		return true
	}
	if rb := f.replace.Bounds(); rb.Contains(x, y) {
		f.focusReplace()
		f.replace.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - rb.X, Y: y - rb.Y})
		f.s.dirty = true
		return true
	}
	// Everything else on the panel — the action buttons and the × close button —
	// goes to the Dialog. Its OnEvent reconstructs absolute coordinates by adding
	// the panel origin back, so hand it a panel-local point.
	f.modal.Panel.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - panel.X, Y: y - panel.Y})
	f.s.dirty = true
	return true
}

// handleDrag and handleRelease carry a title-bar drag through to the Dialog.
//
// [toolkit.Dialog] arms a drag on a press over the title strip, moves the panel
// on each EventMouseDrag and lets go on EventMouseUp. handleClick delivered the
// press and nothing delivered the rest, so the modal armed a drag it was never
// told to continue — the title bar looked draggable and the panel never moved.
//
// The panel-local coordinates are recomputed against the panel's CURRENT bounds
// on every tick, which is what makes a moving target work: the Dialog adds its
// own origin back to reconstruct the absolute pointer position, and that is the
// only thing it compares against where the pointer was last.
func (f *findReplace) handleDrag(x, y int) {
	if !f.shown {
		return
	}
	panel := f.modal.Panel.Bounds()
	f.modal.Panel.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - panel.X, Y: y - panel.Y})
	f.s.dirty = true
}

func (f *findReplace) handleRelease(x, y int) {
	if !f.shown {
		return
	}
	panel := f.modal.Panel.Bounds()
	f.modal.Panel.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x - panel.X, Y: y - panel.Y})
	f.s.dirty = true
}

// focusQuery / focusReplace move the keyboard focus between the two text fields,
// lighting the focused one's caret and muting the other.
func (f *findReplace) focusQuery() {
	f.focusRepl = false
	f.query.SetFocused(true)
	f.replace.SetFocused(false)
}

func (f *findReplace) focusReplace() {
	f.focusRepl = true
	f.query.SetFocused(false)
	f.replace.SetFocused(true)
}

// handleChar routes a typed character to the focused field (query or replacement)
// while the modal is open.
func (f *findReplace) handleChar(code string) bool {
	if !f.shown {
		return false
	}
	f.focusedField().OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: code})
	f.s.dirty = true
	return true
}

// handleKey routes a named key while the modal is open: Escape closes it, Enter /
// Shift+Enter step Next / Previous, and every other key (Backspace, arrows) reaches
// the focused field (a "Shift+"-prefixed code is split back into a bare code + the
// Shift modifier the field reads).
func (f *findReplace) handleKey(code string) bool {
	if !f.shown {
		return false
	}
	switch code {
	case "Escape":
		f.close()
		return true
	case "Enter":
		f.step(1)
		return true
	case "Shift+Enter":
		f.step(-1)
		return true
	}
	ev := toolkit.Event{Kind: toolkit.EventKeyDown, Code: code}
	if rest, ok := strings.CutPrefix(code, "Shift+"); ok {
		ev.Shift = true
		ev.Code = rest
	}
	f.focusedField().OnEvent(ev)
	f.s.dirty = true
	return true
}

// fieldEventer is the event sink both the query (SearchEntry) and the replacement
// (Entry) satisfy, so the focused field is dispatched to uniformly.
type fieldEventer interface {
	OnEvent(toolkit.Event)
}

// focusedField is the field the keyboard currently drives: the replacement field
// when focusRepl, else the query field.
func (f *findReplace) focusedField() fieldEventer {
	if f.focusRepl {
		return f.replace
	}
	return f.query
}

// --- active-editor targets -------------------------------------------------

// sourceTarget is the Source CodeEditor as a searchTarget: FindMatches' native
// (line, col) [toolkit.Selection] coordinate needs no conversion.
type sourceTarget struct {
	e *toolkit.CodeEditor
	s *State
}

func (t sourceTarget) lines() []string                   { return strings.Split(t.e.Text().Get(), "\n") }
func (t sourceTarget) setMatches(ms []toolkit.Selection) { t.e.SetMatchHighlights(ms) }
func (t sourceTarget) setCurrent(m toolkit.Selection)    { t.e.SetCurrentMatch(m) }
func (t sourceTarget) scrollTo(m toolkit.Selection)      { t.e.ScrollToMatch(m) }
func (t sourceTarget) clear()                            { t.e.ClearMatchHighlights() }

// replaceMatches substitutes each span in the line buffer literally (reverse
// document order so earlier columns stay valid) and writes the buffer back.
func (t sourceTarget) replaceMatches(ms []toolkit.Selection, repl string) {
	t.e.SetText(replaceSpans(t.e.Text().Get(), ms, repl))
}

// matchPoints maps each soft highlight's start cell to a device pixel via the
// State's CaretPixel (which reuses the editor's own gutter/advance geometry).
func (t sourceTarget) matchPoints() [][2]int {
	hl := t.e.MatchHighlights()
	pts := make([][2]int, 0, len(hl))
	for _, m := range hl {
		x, y := t.s.CaretPixel(m.StartLine, m.StartCol)
		pts = append(pts, [2]int{x, y})
	}
	return pts
}

// richTarget is the WYSIWYG RichEditor as a searchTarget: FindMatches runs over the
// per-block text projection, and each (block, off) [toolkit.Selection] folds into
// the [toolkit.DocSelection] the RichEditor highlights.
type richTarget struct {
	e *toolkit.RichEditor
	s *State
}

func (t richTarget) lines() []string { return t.e.BlockTexts() }
func (t richTarget) setMatches(ms []toolkit.Selection) {
	t.e.SetMatchHighlights(toolkit.DocSelectionsFromMatches(ms))
}

// setCurrent maps m to a DocSelection; the zero Selection folds to an empty
// DocSelection (Start == End), which clears the emphasis.
func (t richTarget) setCurrent(m toolkit.Selection) {
	t.e.SetCurrentMatch(toolkit.DocSelectionFromMatch(m))
}
func (t richTarget) scrollTo(m toolkit.Selection) {
	t.e.ScrollToMatch(toolkit.DocSelectionFromMatch(m))
}
func (t richTarget) clear() { t.e.ClearMatchHighlights() }

// replaceMatches deletes each match's DocSelection and types the replacement in its
// place. It applies matches in reverse document order so an earlier match's offsets
// stay valid after a later one in the same block is replaced; each match spans a
// single block (FindMatches over BlockTexts never crosses a block boundary).
func (t richTarget) replaceMatches(ms []toolkit.Selection, repl string) {
	for i := len(ms) - 1; i >= 0; i-- {
		sel := toolkit.DocSelectionFromMatch(ms[i])
		t.e.Selection().Set(sel)
		t.e.DeleteSelection() // parks the caret at sel.Start
		t.e.InsertText(repl)  // a "" replacement is a pure delete (InsertText no-ops)
	}
}

// matchPoints maps each soft highlight's start position to a device pixel via the
// RichEditor's own CaretPixel.
func (t richTarget) matchPoints() [][2]int {
	hl := t.e.MatchHighlights()
	pts := make([][2]int, 0, len(hl))
	for _, m := range hl {
		x, y := t.e.CaretPixel(m.Start)
		pts = append(pts, [2]int{x, y})
	}
	return pts
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
