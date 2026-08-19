// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// typeInto sends each rune of s to the editor as an EventChar through the app's
// HandleChar, exactly as the wasm driver forwards a browser keypress — so the
// completion popup opens/refreshes through the real input path.
func typeInto(s *State, text string) {
	for _, r := range text {
		s.HandleChar(string(r))
	}
}

// expandSnippet is the test's minimal mirror of the toolkit's parseSnippet for
// the single "$0" caret stop every playground candidate uses: it returns the
// literal insert text (marker removed) and the caret's rune offset within it.
func expandSnippet(insert string) (body string, caret int) {
	i := strings.Index(insert, "$0")
	if i < 0 {
		return insert, len([]rune(insert))
	}
	return insert[:i] + insert[i+len("$0"):], len([]rune(insert[:i]))
}

// TestCompletionSourceInstalled proves installCompletion wired both hooks and the
// word rule admits a control sequence as one word.
func TestCompletionSourceInstalled(t *testing.T) {
	s := newTestState(t, false)
	if s.editor.CompletionSource == nil {
		t.Fatal("CompletionSource not installed")
	}
	if s.editor.CompletionWordChar == nil {
		t.Fatal("CompletionWordChar not installed")
	}
	if !s.editor.CompletionWordChar('\\') || !s.editor.CompletionWordChar('a') || !s.editor.CompletionWordChar('@') {
		t.Fatal("word rule must admit backslash, letters and @")
	}
	if s.editor.CompletionWordChar('5') || s.editor.CompletionWordChar('{') {
		t.Fatal("word rule must reject digits and braces")
	}
	if n := len(s.editor.CompletionSource(nil, 0, 0)); n < 80 {
		t.Fatalf("curated list has %d items, want a solid few dozen (>=80)", n)
	}
}

// TestCompletionOpensAndFiltersOnPrefix types "\se" and asserts the popup is
// active and every offered item filters under that prefix.
func TestCompletionOpensAndFiltersOnPrefix(t *testing.T) {
	s := newTestState(t, false)
	typeInto(s, `\se`)

	if !s.editor.CompletionActive() {
		t.Fatal(`typing "\se" did not open the completion popup`)
	}
	items := s.editor.CompletionItems()
	if len(items) == 0 {
		t.Fatal("popup is active but offers no items")
	}
	for _, it := range items {
		filter := it.Label
		if it.FilterText != "" {
			filter = it.FilterText
		}
		if !strings.HasPrefix(strings.ToLower(filter), `\se`) {
			t.Fatalf("item %q does not start with the typed prefix \\se", filter)
		}
	}
	// \section must be reachable under this prefix (the headline command).
	found := false
	for _, it := range items {
		if it.Label == `\section` {
			found = true
		}
	}
	if !found {
		t.Fatal(`\section not offered for prefix \se`)
	}

	// The popup rectangle is non-empty and sits inside the editor bounds (nothing
	// clips it): CodeEditor.Draw paints it last, over the text.
	pb := s.editor.CompletionBounds()
	if pb.W <= 0 || pb.H <= 0 {
		t.Fatalf("completion popup has empty bounds %+v", pb)
	}
	er := s.editor.Bounds()
	if pb.X < er.X || pb.X+pb.W > er.X+er.W {
		t.Fatalf("popup %+v spills outside the editor %+v", pb, er)
	}

	// Draw the whole scene with the popup open — must not panic and must paint.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !nonBlank(buf, s.theme.Background) {
		t.Fatal("scene drew nothing with the completion popup open")
	}
}

// TestCompletionArrowAcceptInsertsSnippet drives Arrow-Down then Enter and asserts
// the selected item's snippet body replaced the typed word and the caret landed
// on its $0 stop.
func TestCompletionArrowAcceptInsertsSnippet(t *testing.T) {
	s := newTestState(t, false)
	typeInto(s, `\se`)
	if !s.editor.CompletionActive() {
		t.Fatal("popup not active before accept")
	}

	s.HandleKeyDown("ArrowDown") // move off row 0
	sel := s.editor.CompletionSelected()
	if sel != 1 {
		t.Fatalf("ArrowDown selected row %d, want 1", sel)
	}
	item := s.editor.CompletionItems()[sel]
	insert := item.InsertText
	if insert == "" {
		insert = item.Label
	}
	wantBody, wantCaret := expandSnippet(insert)

	s.HandleKeyDown("Enter") // accept

	if s.editor.CompletionActive() {
		t.Fatal("popup did not close after accept")
	}
	line0 := s.editorLines()[0]
	if !strings.HasPrefix(line0, wantBody) {
		t.Fatalf("accepted line %q does not start with snippet body %q", line0, wantBody)
	}
	// The typed word started at column 0, so the caret offset is the column.
	if got := s.editor.CursorCol().Get(); got != wantCaret {
		t.Fatalf("caret at col %d after accept, want the $0 stop at %d", got, wantCaret)
	}
	// Accepting an item is an edit: it must schedule a recompile.
	if !s.pendingCompile {
		t.Fatal("accepting a completion did not mark a pending compile")
	}
}

// TestCompletionEscapeCloses proves Escape dismisses the popup without editing.
func TestCompletionEscapeCloses(t *testing.T) {
	s := newTestState(t, false)
	typeInto(s, `\se`)
	before := s.editorLines()[0]
	if !s.editor.CompletionActive() {
		t.Fatal("popup not active before Escape")
	}
	s.HandleKeyDown("Escape")
	if s.editor.CompletionActive() {
		t.Fatal("Escape did not close the popup")
	}
	if s.editorLines()[0] != before {
		t.Fatal("Escape changed the buffer")
	}
}

// TestCompletionClosesOnNonWordChar proves typing a non-word character (a space)
// dismisses the popup.
func TestCompletionClosesOnNonWordChar(t *testing.T) {
	s := newTestState(t, false)
	typeInto(s, `\se`)
	if !s.editor.CompletionActive() {
		t.Fatal("popup not active before the non-word char")
	}
	s.HandleChar(" ")
	if s.editor.CompletionActive() {
		t.Fatal("a non-word character did not close the popup")
	}
}

// TestCompletionCompilesAfterAccept proves the edit→compile→render pipeline still
// runs after a completion accept: the pending compile materialises pages.
func TestCompletionCompilesAfterAccept(t *testing.T) {
	s := newTestState(t, false)
	// Start from a minimal valid document so the accepted command lands in a body
	// that still compiles to at least one page.
	// Line 2 carries real text so the page renders; line 3 is blank so the caret
	// there begins a fresh word (no preceding word char to absorb into the prefix).
	s.SetSource("\\documentclass{article}\n\\begin{document}\nHello world.\n\n\\end{document}")
	s.editor.CursorLine().Set(3)
	s.editor.CursorCol().Set(0)
	typeInto(s, `\emph`)
	if !s.editor.CompletionActive() {
		t.Fatal("popup not active for \\emph")
	}
	s.HandleKeyDown("Enter")
	if !s.pendingCompile {
		t.Fatal("no pending compile after accept")
	}
	if !s.CompilePending() {
		t.Fatal("CompilePending reported nothing to do")
	}
	if s.DrawnPages() == 0 {
		t.Fatalf("render produced no pages after the accepted edit (err=%q)", s.errText)
	}
}

// TestCompletionSourceReturnsCuratedGroups checks the list actually contains a
// command, an environment snippet and a math symbol — the three curated kinds.
func TestCompletionSourceReturnsCuratedGroups(t *testing.T) {
	s := newTestState(t, false)
	var haveCmd, haveEnv, haveSym bool
	for _, it := range s.editor.CompletionSource(nil, 0, 0) {
		switch it.Kind {
		case toolkit.CompletionFunction, toolkit.CompletionKeyword:
			haveCmd = true
		case toolkit.CompletionSnippet:
			if strings.HasPrefix(it.Label, `\begin{`) {
				haveEnv = true
			}
		case toolkit.CompletionConstant:
			haveSym = true
		}
	}
	if !haveCmd || !haveEnv || !haveSym {
		t.Fatalf("curated list missing a kind: cmd=%v env=%v sym=%v", haveCmd, haveEnv, haveSym)
	}
}
