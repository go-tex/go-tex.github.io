// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// findDoc is a tiny, deterministic Source buffer with three "foo" occurrences (two
// on line 0, one on line 1) so match counts and (line,col) ranges are predictable.
const findDoc = "foo bar foo\nbaz foo qux"

// wysDoc is a LaTeX document whose body parses (go-richdoc/latex) into exactly two
// paragraph blocks — "Alpha foo beta." and "Gamma foo foo delta." — so the WYSIWYG
// RichEditor holds three "foo" occurrences across two blocks (one in block 0, two
// in block 1), the block-coordinate analogue of findDoc.
const wysDoc = "\\documentclass{article}\n\\begin{document}\nAlpha foo beta.\n\nGamma foo foo delta.\n\\end{document}\n"

// setFindDoc installs findDoc as the Source buffer (no compile needed — the find
// logic reads Text() directly).
func setFindDoc(s *State) { s.editor.SetText(findDoc) }

// enterWysiwygFindDoc loads wysDoc and switches to the WYSIWYG tab, so the active
// searchTarget is the RichEditor holding the three "foo" blocks.
func enterWysiwygFindDoc(t *testing.T, s *State) {
	t.Helper()
	s.SetSource(wysDoc)
	s.SetEditorTab(tabWysiwyg)
	if !s.WysiwygActive() {
		t.Fatalf("WYSIWYG tab did not activate (parse error %q)", s.WysiwygParseError())
	}
}

// setQuery drives the modal's query bar the way typing does — through its Text
// observable, which fires the search subscription.
func setQuery(s *State, q string) { s.fr.query.Text().Set(q) }

// TestFindToolbarButtonToggles proves the Find toolbar button opens and closes the
// modal through the real pointer router (the toolbar click loop → the button's
// OnClick → fr.toggle).
func TestFindToolbarButtonToggles(t *testing.T) {
	s := newTestState(t, false)
	b := s.findBtn.Bounds()
	cx, cy := b.X+b.W/2, b.Y+b.H/2

	if s.FindVisible() {
		t.Fatalf("find modal visible before any interaction")
	}
	if !s.HandleClick(cx, cy) {
		t.Fatalf("click on Find button not consumed")
	}
	if s.pressKind != pressToolbar {
		t.Fatalf("Find button click did not capture the toolbar (pressKind=%d)", s.pressKind)
	}
	if !s.FindVisible() {
		t.Fatalf("find modal not visible after the Find button click")
	}
	// A second click closes it.
	if !s.HandleClick(cx, cy) {
		t.Fatalf("second click on Find button not consumed")
	}
	if s.FindVisible() {
		t.Fatalf("find modal still visible after the second Find button click")
	}
}

// TestFindQueryHighlightsAndCount is the core Source flow: a query runs FindMatches,
// paints one soft highlight per occurrence, emphasises the first, and the count
// reads "1 of 3".
func TestFindQueryHighlightsAndCount(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	if !s.ToggleFindReplace() {
		t.Fatalf("ToggleFindReplace did not report a change")
	}
	setQuery(s, "foo")

	if got := len(s.fr.matches); got != 3 {
		t.Fatalf("matches = %d, want 3", got)
	}
	if got := len(s.editor.MatchHighlights()); got != 3 {
		t.Fatalf("editor soft highlights = %d, want 3", got)
	}
	if got := s.FindTotal(); got != 3 {
		t.Fatalf("FindTotal = %d, want 3", got)
	}
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("FindCurrent = %d, want 0", got)
	}
	if got := s.FindCountText(); got != "1 of 3" {
		t.Fatalf("FindCountText = %q, want %q", got, "1 of 3")
	}
	if s.editor.CurrentMatch().IsEmpty() {
		t.Fatalf("current-match emphasis not set")
	}
}

// TestFindNextPrevWrap steps the current match forward and backward through the
// keyboard (Enter / Shift+Enter) and proves both wrap around the ends.
func TestFindNextPrevWrap(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo") // current = 0 of 3

	next := func() { s.HandleKeyDown("Enter") }
	prev := func() { s.HandleKeyDown("Shift+Enter") }

	next()
	if got := s.FindCurrent(); got != 1 {
		t.Fatalf("after next, Current = %d, want 1", got)
	}
	next()
	next() // 2 -> wrap to 0
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("next past the end did not wrap: Current = %d, want 0", got)
	}
	prev() // 0 -> wrap to 2
	if got := s.FindCurrent(); got != 2 {
		t.Fatalf("prev past the start did not wrap: Current = %d, want 2", got)
	}
	if got := s.editor.CurrentMatch(); got != s.fr.matches[2] {
		t.Fatalf("current-match emphasis = %+v, want %+v", got, s.fr.matches[2])
	}
}

// TestFindInvalidRegex proves a bad pattern flags the modal and clears the
// highlights rather than tallying stale matches.
func TestFindInvalidRegex(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo") // seed a valid result first
	setQuery(s, "(")   // now break it

	if !s.FindInvalid() {
		t.Fatalf("Invalid flag not set for a bad pattern")
	}
	if got := s.FindCountText(); got != "Bad pattern" {
		t.Fatalf("FindCountText = %q, want %q", got, "Bad pattern")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on a bad pattern: %d", got)
	}
	if s.fr.matches != nil {
		t.Fatalf("matches not dropped on a bad pattern")
	}
}

// TestFindNoResults proves an empty query (after a real one) resets to the
// no-results state.
func TestFindNoResults(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo")
	setQuery(s, "") // back to empty

	if got := s.FindCountText(); got != "No results" {
		t.Fatalf("FindCountText = %q, want %q", got, "No results")
	}
	if s.fr.current != -1 {
		t.Fatalf("current = %d, want -1 for no results", s.fr.current)
	}
}

// TestFindReplaceCurrent replaces the current Source match and re-searches, keeping
// the index so the modal lands on the following occurrence.
func TestFindReplaceCurrent(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo") // 3 matches, current 0 = line0 col0..3
	s.fr.replace.Text().Set("X")

	s.fr.replaceCurrent()

	if got := s.editor.Text().Get(); got != "X bar foo\nbaz foo qux" {
		t.Fatalf("buffer after replace = %q", got)
	}
	if got := len(s.fr.matches); got != 2 {
		t.Fatalf("matches after replace = %d, want 2", got)
	}
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("current after replace = %d, want 0 (kept, clamped)", got)
	}
}

// TestFindReplaceAll replaces every Source match in one edit and re-searches to zero.
func TestFindReplaceAll(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo")
	s.fr.replace.Text().Set("X")

	s.fr.replaceAll()

	if got := s.editor.Text().Get(); got != "X bar X\nbaz X qux" {
		t.Fatalf("buffer after replace-all = %q", got)
	}
	if got := len(s.fr.matches); got != 0 {
		t.Fatalf("matches after replace-all = %d, want 0", got)
	}
	if got := s.FindCountText(); got != "No results" {
		t.Fatalf("FindCountText after replace-all = %q, want %q", got, "No results")
	}
}

// TestFindReplaceGuards proves replace / replace-all are no-ops with no current
// match (the empty-result guards).
func TestFindReplaceGuards(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "nomatch") // 0 matches, current -1
	s.fr.replace.Text().Set("X")

	before := s.editor.Text().Get()
	s.fr.replaceCurrent() // current < 0 → guard
	s.fr.replaceAll()     // len == 0 → guard
	if got := s.editor.Text().Get(); got != before {
		t.Fatalf("buffer changed by a guarded replace: %q", got)
	}
}

// TestFindCloseClears proves closing the modal (toggle path) drops the highlights,
// and the Escape path does the same through onModalClose.
func TestFindCloseClears(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)

	// Toggle-close path.
	s.ToggleFindReplace()
	setQuery(s, "foo")
	s.ToggleFindReplace() // close
	if s.FindVisible() {
		t.Fatalf("modal still visible after toggle-close")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on toggle-close: %d", got)
	}

	// Escape path.
	s.ToggleFindReplace()
	setQuery(s, "foo")
	if !s.HandleKeyDown("Escape") {
		t.Fatalf("Escape not routed to the find modal")
	}
	if s.FindVisible() {
		t.Fatalf("modal still visible after Escape")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on Escape: %d", got)
	}
}

// TestFindScrimAndCloseButton proves a click on the scrim (outside the panel) and a
// click on the panel's × close button each dismiss the modal and clear highlights.
func TestFindScrimAndCloseButton(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)

	// Scrim click: a point at the top-left corner is on the scrim, outside the
	// lower-anchored panel.
	s.ToggleFindReplace()
	setQuery(s, "foo")
	if !s.HandleClick(0, 0) {
		t.Fatalf("scrim click not consumed by the modal")
	}
	if s.FindVisible() {
		t.Fatalf("modal still visible after a scrim click")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on a scrim click: %d", got)
	}

	// × close button: the trailing square of the panel's title bar.
	s.ToggleFindReplace()
	setQuery(s, "foo")
	p := s.fr.modal.Panel.Bounds()
	if !s.HandleClick(p.X+p.W-3, p.Y+3) {
		t.Fatalf("× close click not consumed")
	}
	if s.FindVisible() {
		t.Fatalf("modal still visible after the × close click")
	}
}

// TestFindClickRouting proves a click on the query bar and on the replacement field
// each move focus there and are consumed (captured as pressFind), and that a click
// on an action button (Next) fires it via the Dialog.
func TestFindClickRouting(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	if s.fr.handleClick(10, 10) {
		t.Fatalf("closed modal consumed a click")
	}
	s.ToggleFindReplace()
	setQuery(s, "foo")

	// Draw relays out the panel's children so their bounds are current.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// A click on the query bar focuses it and is consumed as pressFind.
	qb := s.fr.query.Bounds()
	if !s.HandleClick(qb.X+qb.W/2, qb.Y+qb.H/2) {
		t.Fatalf("click on the query bar not consumed")
	}
	if s.pressKind != pressFind {
		t.Fatalf("query click did not capture pressFind (pressKind=%d)", s.pressKind)
	}
	if s.fr.focusRepl {
		t.Fatalf("query click focused the replacement field")
	}
	s.HandleRelease(qb.X+qb.W/2, qb.Y+qb.H/2)
	if s.pressKind != pressNone {
		t.Fatalf("pressKind not cleared after release")
	}

	// A click on the replacement field focuses it.
	rb := s.fr.replace.Bounds()
	if !s.HandleClick(rb.X+rb.W/2, rb.Y+rb.H/2) {
		t.Fatalf("click on the replacement field not consumed")
	}
	if !s.fr.focusRepl {
		t.Fatalf("replacement click did not focus the replacement field")
	}

	// A click on the Next button advances the current match (routed via the Dialog).
	nb := s.fr.nextBtn.Bounds()
	if !s.HandleClick(nb.X+nb.W/2, nb.Y+nb.H/2) {
		t.Fatalf("click on the Next button not consumed")
	}
	if got := s.FindCurrent(); got != 1 {
		t.Fatalf("Next button did not advance the current match: %d, want 1", got)
	}

	// A click on the Previous button rewinds it (its onClick closure).
	pb := s.fr.prevBtn.Bounds()
	if !s.HandleClick(pb.X+pb.W/2, pb.Y+pb.H/2) {
		t.Fatalf("click on the Previous button not consumed")
	}
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("Previous button did not rewind the current match: %d, want 0", got)
	}
}

// TestFindCharRouting proves typed characters reach the focused field (query, then
// the replacement field once it holds focus) while the modal is open, and are
// ignored while it is closed.
func TestFindCharRouting(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)

	if s.fr.handleChar("z") {
		t.Fatalf("closed modal consumed a character")
	}

	s.ToggleFindReplace() // opens with the query field focused
	if !s.HandleChar("z") {
		t.Fatalf("open modal did not consume a typed character")
	}
	if got := s.fr.query.Text().Get(); got != "z" {
		t.Fatalf("typed character did not reach the query field: %q", got)
	}

	// Move focus to the replacement field; characters now land there.
	s.fr.focusReplace()
	if !s.HandleChar("Y") {
		t.Fatalf("open modal did not consume a replacement character")
	}
	if got := s.fr.replace.Text().Get(); got != "Y" {
		t.Fatalf("typed character did not reach the replacement field: %q", got)
	}
	// A Backspace on the focused replacement field deletes.
	s.HandleKeyDown("Backspace")
	if got := s.fr.replace.Text().Get(); got != "" {
		t.Fatalf("Backspace did not reach the replacement field: %q", got)
	}
	// A "Shift+"-prefixed non-Enter key is split back into a bare code + Shift and
	// routed to the focused field (covering the modifier-split path).
	s.fr.replace.Text().Set("ab")
	if !s.HandleKeyDown("Shift+ArrowLeft") {
		t.Fatalf("Shift+ArrowLeft not consumed by the open modal")
	}
}

// TestFindKeyRoutingClosed proves a named key is not consumed by a closed modal.
func TestFindKeyRoutingClosed(t *testing.T) {
	s := newTestState(t, false)
	if s.fr.handleKey("Enter") {
		t.Fatalf("closed modal consumed a key")
	}
}

// TestFindDrawVisible covers the modal's visible draw branch (a no-op while hidden,
// exercised by every other Draw).
func TestFindDrawVisible(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo")
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !nonBlank(buf, s.theme.Background) {
		t.Fatalf("scene drew nothing with the find modal open")
	}
}

// TestFindStepNoMatches proves stepping with no matches is a safe no-op.
func TestFindStepNoMatches(t *testing.T) {
	s := newTestState(t, false)
	before := s.fr.current
	s.fr.step(1)
	if s.fr.current != before {
		t.Fatalf("step changed current with no matches: %d -> %d", before, s.fr.current)
	}
}

// TestFindApplyCurrentClamp drives the keep-index clamp branches directly (a
// re-search after an out-of-range current index).
func TestFindApplyCurrentClamp(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetText("foo foo") // two matches on one line
	s.ToggleFindReplace()
	setQuery(s, "foo")

	// current below range → clamps up to 0.
	s.fr.current = -5
	s.fr.search(true)
	if s.fr.current != 0 {
		t.Fatalf("keep-index clamp (low) = %d, want 0", s.fr.current)
	}
	// current above range → clamps down to last.
	s.fr.current = 99
	s.fr.search(true)
	if s.fr.current != 1 {
		t.Fatalf("keep-index clamp (high) = %d, want 1", s.fr.current)
	}
}

// TestFindDebugAccessors covers the headless-proof introspection surface
// (FindTotal / FindCurrent / FindCountText / FindInvalid / FindMatchPoints).
func TestFindDebugAccessors(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	setQuery(s, "foo")

	if got := s.FindTotal(); got != 3 {
		t.Fatalf("FindTotal = %d, want 3", got)
	}
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("FindCurrent = %d, want 0", got)
	}
	if got := s.FindCountText(); got != "1 of 3" {
		t.Fatalf("FindCountText = %q, want %q", got, "1 of 3")
	}
	if s.FindInvalid() {
		t.Fatalf("FindInvalid true for a valid pattern")
	}
	if got := len(s.FindMatchPoints()); got != 3 {
		t.Fatalf("FindMatchPoints = %d points, want 3", got)
	}
	// A bad pattern flips FindInvalid.
	setQuery(s, "(")
	if !s.FindInvalid() {
		t.Fatalf("FindInvalid false for a bad pattern")
	}
}

// TestFindPanelPositionClamp shrinks the surface so the editor pane is smaller than
// the panel, exercising positionPanel's width / height / top clamps.
func TestFindPanelPositionClamp(t *testing.T) {
	s := newTestState(t, false)
	s.Resize(220, 180) // editor pane now smaller than the preferred panel size
	s.ToggleFindReplace()
	buf := make([]byte, 220*180*4)
	s.Draw(buf) // draws the modal, running positionPanel with the clamps live

	er := s.editor.Bounds()
	p := s.fr.modal.Panel.Bounds()
	if p.W > er.W {
		t.Fatalf("panel width %d exceeds the editor pane %d (clamp failed)", p.W, er.W)
	}
	if p.H > er.H {
		t.Fatalf("panel height %d exceeds the editor pane %d (clamp failed)", p.H, er.H)
	}
	if p.Y < er.Y {
		t.Fatalf("panel top %d above the editor pane %d (clamp failed)", p.Y, er.Y)
	}
}

// --- WYSIWYG editor ---------------------------------------------------------

// TestFindWysiwygQueryHighlights proves the SAME modal, targeting the WYSIWYG
// RichEditor, runs the regex over the block text: three "foo" occurrences highlight
// on the RichEditor and the count reads "1 of 3".
func TestFindWysiwygQueryHighlights(t *testing.T) {
	s := newTestState(t, false)
	enterWysiwygFindDoc(t, s)
	s.ToggleFindReplace()
	setQuery(s, "foo")

	if got := s.FindTotal(); got != 3 {
		t.Fatalf("WYSIWYG FindTotal = %d, want 3", got)
	}
	if got := len(s.wysiwyg().editor.MatchHighlights()); got != 3 {
		t.Fatalf("RichEditor soft highlights = %d, want 3", got)
	}
	if got := s.FindCountText(); got != "1 of 3" {
		t.Fatalf("WYSIWYG FindCountText = %q, want %q", got, "1 of 3")
	}
	if s.wysiwyg().editor.CurrentMatch().IsEmpty() {
		t.Fatalf("RichEditor current-match emphasis not set")
	}
	if got := len(s.FindMatchPoints()); got != 3 {
		t.Fatalf("WYSIWYG FindMatchPoints = %d, want 3", got)
	}
}

// TestFindWysiwygNextPrev steps the current match on the RichEditor.
func TestFindWysiwygNextPrev(t *testing.T) {
	s := newTestState(t, false)
	enterWysiwygFindDoc(t, s)
	s.ToggleFindReplace()
	setQuery(s, "foo")

	s.HandleKeyDown("Enter")
	if got := s.FindCurrent(); got != 1 {
		t.Fatalf("WYSIWYG next → Current = %d, want 1", got)
	}
	s.HandleKeyDown("Shift+Enter")
	if got := s.FindCurrent(); got != 0 {
		t.Fatalf("WYSIWYG prev → Current = %d, want 0", got)
	}
}

// TestFindWysiwygReplaceCurrent replaces one RichEditor match; the block text loses
// one occurrence.
func TestFindWysiwygReplaceCurrent(t *testing.T) {
	s := newTestState(t, false)
	enterWysiwygFindDoc(t, s)
	s.ToggleFindReplace()
	setQuery(s, "foo")
	s.fr.replace.Text().Set("X")

	s.fr.replaceCurrent()

	if got := s.FindTotal(); got != 2 {
		t.Fatalf("matches after WYSIWYG replace = %d, want 2", got)
	}
	joined := strings.Join(s.wysiwyg().editor.BlockTexts(), "\n")
	if strings.Count(joined, "foo") != 2 {
		t.Fatalf("block text after replace = %q, want two remaining foo", joined)
	}
	if !strings.Contains(joined, "X") {
		t.Fatalf("replacement not written into the RichEditor: %q", joined)
	}
}

// TestFindWysiwygReplaceAll replaces every RichEditor match in one pass (reverse
// per-block order) and re-searches to zero, covering richTarget.setCurrent's clear.
func TestFindWysiwygReplaceAll(t *testing.T) {
	s := newTestState(t, false)
	enterWysiwygFindDoc(t, s)
	s.ToggleFindReplace()
	setQuery(s, "foo")
	s.fr.replace.Text().Set("Z")

	s.fr.replaceAll()

	if got := s.FindTotal(); got != 0 {
		t.Fatalf("matches after WYSIWYG replace-all = %d, want 0", got)
	}
	joined := strings.Join(s.wysiwyg().editor.BlockTexts(), "\n")
	if strings.Contains(joined, "foo") {
		t.Fatalf("block text still holds foo after replace-all: %q", joined)
	}
	if strings.Count(joined, "Z") != 3 {
		t.Fatalf("replace-all wrote %d Z, want 3: %q", strings.Count(joined, "Z"), joined)
	}
	if got := s.FindCountText(); got != "No results" {
		t.Fatalf("WYSIWYG count after replace-all = %q, want %q", got, "No results")
	}
}

// TestFindWysiwygInvalid proves a bad pattern clears the RichEditor highlights
// (covering richTarget.clear on the invalid path).
func TestFindWysiwygInvalid(t *testing.T) {
	s := newTestState(t, false)
	enterWysiwygFindDoc(t, s)
	s.ToggleFindReplace()
	setQuery(s, "foo") // seed a valid result
	setQuery(s, "[")   // break it

	if !s.FindInvalid() {
		t.Fatalf("WYSIWYG bad pattern did not flag invalid")
	}
	if got := len(s.wysiwyg().editor.MatchHighlights()); got != 0 {
		t.Fatalf("RichEditor highlights not cleared on a bad pattern: %d", got)
	}
}

// TestFindTabRetarget proves that switching the editor tab while the modal is open
// re-targets the search: opening on Source highlights the CodeEditor; switching to
// WYSIWYG clears the Source highlights and highlights the RichEditor; switching
// back reverses it.
func TestFindTabRetarget(t *testing.T) {
	s := newTestState(t, false)
	s.SetSource(wysDoc) // parses cleanly for the WYSIWYG round-trip
	s.ToggleFindReplace()
	setQuery(s, "foo")

	// On Source: the CodeEditor carries the highlights.
	if got := len(s.editor.MatchHighlights()); got == 0 {
		t.Fatalf("Source highlights missing before the tab switch")
	}

	// Switch to WYSIWYG: Source cleared, RichEditor highlighted.
	s.SetEditorTab(tabWysiwyg)
	if !s.WysiwygActive() {
		t.Fatalf("WYSIWYG did not activate (parse error %q)", s.WysiwygParseError())
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("Source highlights not cleared after switching to WYSIWYG: %d", got)
	}
	if got := len(s.wysiwyg().editor.MatchHighlights()); got == 0 {
		t.Fatalf("RichEditor highlights missing after switching to WYSIWYG")
	}

	// Switch back to Source: RichEditor cleared, Source highlighted.
	s.SetEditorTab(tabSource)
	if got := len(s.wysiwyg().editor.MatchHighlights()); got != 0 {
		t.Fatalf("RichEditor highlights not cleared after switching back: %d", got)
	}
	if got := len(s.editor.MatchHighlights()); got == 0 {
		t.Fatalf("Source highlights missing after switching back")
	}
}

// TestReplaceSpans covers the pure span-substitution helper's edge cases.
func TestReplaceSpans(t *testing.T) {
	if got := replaceSpans("abc", nil, "X"); got != "abc" {
		t.Fatalf("no spans should leave the text unchanged: %q", got)
	}
	outOfRange := []toolkit.Selection{{StartLine: 5, StartCol: 0, EndLine: 5, EndCol: 1}}
	if got := replaceSpans("abc", outOfRange, "X"); got != "abc" {
		t.Fatalf("out-of-range span should be skipped: %q", got)
	}
	clamp := []toolkit.Selection{{StartLine: 0, StartCol: -2, EndLine: 0, EndCol: 100}}
	if got := replaceSpans("abc", clamp, "X"); got != "X" {
		t.Fatalf("clamped span should replace the whole line: %q", got)
	}
	inverted := []toolkit.Selection{{StartLine: 0, StartCol: 2, EndLine: 0, EndCol: 1}}
	if got := replaceSpans("abc", inverted, "X"); got != "abc" {
		t.Fatalf("inverted span should be skipped: %q", got)
	}
	// Two spans on one line replaced right-to-left keep each other's columns valid.
	two := []toolkit.Selection{
		{StartLine: 0, StartCol: 0, EndLine: 0, EndCol: 3},
		{StartLine: 0, StartCol: 8, EndLine: 0, EndCol: 11},
	}
	if got := replaceSpans("foo bar foo", two, "X"); got != "X bar X" {
		t.Fatalf("two-span replace = %q, want %q", got, "X bar X")
	}
}
