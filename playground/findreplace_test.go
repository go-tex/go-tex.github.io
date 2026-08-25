// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// findDoc is a tiny, deterministic buffer with three "foo" occurrences (two on
// line 0, one on line 1) so match counts and (line,col) ranges are predictable.
const findDoc = "foo bar foo\nbaz foo qux"

// setFindDoc installs findDoc as the editor buffer (no compile needed — the find
// logic reads Text() directly).
func setFindDoc(s *State) { s.editor.SetText(findDoc) }

// TestFindToolbarButtonToggles proves the Find toolbar button opens and closes
// the bar through the real pointer router (the toolbar click loop → the button's
// OnClick → fr.toggle).
func TestFindToolbarButtonToggles(t *testing.T) {
	s := newTestState(t, false)
	b := s.findBtn.Bounds()
	cx, cy := b.X+b.W/2, b.Y+b.H/2

	if s.FindVisible() {
		t.Fatalf("find bar visible before any interaction")
	}
	if !s.HandleClick(cx, cy) {
		t.Fatalf("click on Find button not consumed")
	}
	if s.pressKind != pressToolbar {
		t.Fatalf("Find button click did not capture the toolbar (pressKind=%d)", s.pressKind)
	}
	if !s.FindVisible() {
		t.Fatalf("find bar not visible after the Find button click")
	}
	// A second click closes it.
	if !s.HandleClick(cx, cy) {
		t.Fatalf("second click on Find button not consumed")
	}
	if s.FindVisible() {
		t.Fatalf("find bar still visible after the second Find button click")
	}
}

// TestFindQueryHighlightsAndCount is the core flow: a query runs FindMatches,
// paints one soft highlight per occurrence, emphasises the first, and the bar's
// count reads "1 of 3".
func TestFindQueryHighlightsAndCount(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	if !s.ToggleFindReplace() {
		t.Fatalf("ToggleFindReplace did not report a change")
	}
	s.fr.fr.Query().Set("foo")

	if got := len(s.fr.matches); got != 3 {
		t.Fatalf("matches = %d, want 3", got)
	}
	if got := len(s.editor.MatchHighlights()); got != 3 {
		t.Fatalf("editor soft highlights = %d, want 3", got)
	}
	if got := s.fr.fr.Total().Get(); got != 3 {
		t.Fatalf("Total = %d, want 3", got)
	}
	if got := s.fr.fr.Current().Get(); got != 0 {
		t.Fatalf("Current = %d, want 0", got)
	}
	if got := s.fr.fr.CountText(); got != "1 of 3" {
		t.Fatalf("CountText = %q, want %q", got, "1 of 3")
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
	s.fr.fr.Query().Set("foo") // current = 0 of 3

	next := func() { s.HandleKeyDown("Enter") }
	prev := func() { s.HandleKeyDown("Shift+Enter") }

	next()
	if got := s.fr.fr.Current().Get(); got != 1 {
		t.Fatalf("after next, Current = %d, want 1", got)
	}
	next()
	next() // 2 -> wrap to 0
	if got := s.fr.fr.Current().Get(); got != 0 {
		t.Fatalf("next past the end did not wrap: Current = %d, want 0", got)
	}
	prev() // 0 -> wrap to 2
	if got := s.fr.fr.Current().Get(); got != 2 {
		t.Fatalf("prev past the start did not wrap: Current = %d, want 2", got)
	}
	// The editor's current-match emphasis tracks the last match.
	if got := s.editor.CurrentMatch(); got != s.fr.matches[2] {
		t.Fatalf("current-match emphasis = %+v, want %+v", got, s.fr.matches[2])
	}
}

// TestFindInvalidRegex proves a bad pattern flags the bar and clears the
// highlights rather than tallying stale matches.
func TestFindInvalidRegex(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo") // seed a valid result first
	s.fr.fr.Query().Set("(")   // now break it

	if !s.fr.fr.Invalid().Get() {
		t.Fatalf("Invalid flag not set for a bad pattern")
	}
	if got := s.fr.fr.CountText(); got != "Bad pattern" {
		t.Fatalf("CountText = %q, want %q", got, "Bad pattern")
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
	s.fr.fr.Query().Set("foo")
	s.fr.fr.Query().Set("") // back to empty

	if got := s.fr.fr.CountText(); got != "No results" {
		t.Fatalf("CountText = %q, want %q", got, "No results")
	}
	if s.fr.current != -1 {
		t.Fatalf("current = %d, want -1 for no results", s.fr.current)
	}
}

// TestFindReplaceCurrent replaces the current match and re-searches, keeping the
// index so the bar lands on the following occurrence.
func TestFindReplaceCurrent(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo") // 3 matches, current 0 = line0 col0..3
	s.fr.fr.Replace().Set("X")

	s.fr.replace()

	if got := s.editor.Text().Get(); got != "X bar foo\nbaz foo qux" {
		t.Fatalf("buffer after replace = %q", got)
	}
	if got := len(s.fr.matches); got != 2 {
		t.Fatalf("matches after replace = %d, want 2", got)
	}
	if got := s.fr.fr.Current().Get(); got != 0 {
		t.Fatalf("current after replace = %d, want 0 (kept, clamped)", got)
	}
}

// TestFindReplaceAll replaces every match in one edit and re-searches to zero.
func TestFindReplaceAll(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo")
	s.fr.fr.Replace().Set("X")

	s.fr.replaceAll()

	if got := s.editor.Text().Get(); got != "X bar X\nbaz X qux" {
		t.Fatalf("buffer after replace-all = %q", got)
	}
	if got := len(s.fr.matches); got != 0 {
		t.Fatalf("matches after replace-all = %d, want 0", got)
	}
	if got := s.fr.fr.CountText(); got != "No results" {
		t.Fatalf("CountText after replace-all = %q, want %q", got, "No results")
	}
}

// TestFindReplaceGuards proves replace / replace-all are no-ops with no current
// match (the empty-result guards).
func TestFindReplaceGuards(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("nomatch") // 0 matches, current -1
	s.fr.fr.Replace().Set("X")

	before := s.editor.Text().Get()
	s.fr.replace()    // current < 0 → guard
	s.fr.replaceAll() // len == 0 → guard
	if got := s.editor.Text().Get(); got != before {
		t.Fatalf("buffer changed by a guarded replace: %q", got)
	}
}

// TestFindCloseClears proves closing the bar (toolbar/keybind path) drops the
// highlights, and the Escape path does the same through the OnClose callback.
func TestFindCloseClears(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)

	// Toggle-close path.
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo")
	s.ToggleFindReplace() // close
	if s.FindVisible() {
		t.Fatalf("bar still visible after toggle-close")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on toggle-close: %d", got)
	}

	// Escape path (fires OnClose → clearSearch).
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo")
	if !s.HandleKeyDown("Escape") {
		t.Fatalf("Escape not routed to the find bar")
	}
	if s.FindVisible() {
		t.Fatalf("bar still visible after Escape")
	}
	if got := len(s.editor.MatchHighlights()); got != 0 {
		t.Fatalf("highlights not cleared on Escape: %d", got)
	}
}

// TestFindClickRouting proves a click on a bar control is consumed (captured as
// pressFind, then released), while a click on the editor beneath the bar falls
// through so the editor still works.
func TestFindClickRouting(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo")

	// Draw relayouts the bar's children so their bounds are current.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// A control (the query field is the first child) is consumed by the bar.
	qb := s.fr.fr.Children()[0].Bounds()
	if !s.HandleClick(qb.X+qb.W/2, qb.Y+qb.H/2) {
		t.Fatalf("click on a find control not consumed")
	}
	if s.pressKind != pressFind {
		t.Fatalf("find control click did not capture pressFind (pressKind=%d)", s.pressKind)
	}
	if !s.HandleRelease(qb.X+qb.W/2, qb.Y+qb.H/2) {
		t.Fatalf("release after a find click not consumed")
	}
	if s.pressKind != pressNone {
		t.Fatalf("pressKind not cleared after release")
	}

	// A click low in the editor pane (within the bar's bounds but far from the
	// top-right panel) falls through to the editor.
	er := s.editor.Bounds()
	ex, ey := er.X+10, er.Y+er.H-10
	if !s.HandleClick(ex, ey) {
		t.Fatalf("editor click under the open bar not consumed")
	}
	if s.pressKind != pressEditor {
		t.Fatalf("editor click under the bar did not reach the editor (pressKind=%d)", s.pressKind)
	}
	s.HandleRelease(ex, ey)
}

// TestFindCharRouting proves typed characters reach the bar's focused field
// while it is open, and are ignored by the bar while it is closed.
func TestFindCharRouting(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)

	if s.fr.handleChar("z") {
		t.Fatalf("closed bar consumed a character")
	}

	s.ToggleFindReplace() // opens with the query field focused
	if !s.HandleChar("z") {
		t.Fatalf("open bar did not consume a typed character")
	}
	if got := s.fr.fr.Query().Get(); got != "z" {
		t.Fatalf("typed character did not reach the query field: %q", got)
	}
}

// TestFindKeyRoutingClosed proves a named key is not consumed by a closed bar.
func TestFindKeyRoutingClosed(t *testing.T) {
	s := newTestState(t, false)
	if s.fr.handleKey("Enter") {
		t.Fatalf("closed bar consumed a key")
	}
}

// TestFindDrawVisible covers the bar's visible draw branch (it is a no-op while
// hidden, exercised by every other Draw).
func TestFindDrawVisible(t *testing.T) {
	s := newTestState(t, false)
	setFindDoc(s)
	s.ToggleFindReplace()
	s.fr.fr.Query().Set("foo")
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !nonBlank(buf, s.theme.Background) {
		t.Fatalf("scene drew nothing with the find bar open")
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
	s.fr.fr.Query().Set("foo")

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
	s.fr.fr.Query().Set("foo")

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
	s.fr.fr.Query().Set("(")
	if !s.FindInvalid() {
		t.Fatalf("FindInvalid false for a bad pattern")
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
