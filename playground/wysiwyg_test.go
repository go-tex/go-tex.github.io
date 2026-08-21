// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
	"github.com/go-widgets/toolkit"
)

// wysSnippet is a small LaTeX document exercising the structures the headless
// harness also asserts on: a \section, an existing \textbf, a plain paragraph to
// embolden, and an itemize list.
const wysSnippet = `\section{Introduction}

This has \textbf{bold} already.

A plain line to embolden.

\begin{itemize}
\item First
\item Second
\end{itemize}`

// wysRefSnippet exercises the go-richdoc v0.2.0 reference inlines: a \section
// whose \label folds into Heading.ID, a \footnote (an inline Footnote), and a
// \ref + a \cite (two CrossRefs). It is the fixture for the v0.2-node proof.
const wysRefSnippet = `\section{Intro}\label{sec:i}

A paragraph with a note\footnote{a note} and a reference to \ref{sec:i} plus a citation \cite{knuth}.`

// newWysState builds a laid-out State (light theme) at the test surface size.
func newWysState(t *testing.T) *State {
	t.Helper()
	SetupText(1)
	return NewState(testW, testH, false)
}

func TestWysiwygLaTeXRoundTrip(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)

	if s.WysiwygActive() {
		t.Fatal("mode should start inactive")
	}

	// Enter WYSIWYG: parse the LaTeX into the RichEditor.
	s.ToggleWysiwyg()
	if !s.WysiwygActive() {
		t.Fatal("toggle did not activate")
	}
	if s.WysiwygParseError() != "" {
		t.Fatalf("unexpected parse error: %s", s.WysiwygParseError())
	}
	if n := s.RichBlockCount(); n < 4 {
		t.Fatalf("block count = %d, want >= 4 (heading, 2 paragraphs, list)", n)
	}
	if h := s.RichFirstHeading(); h != "Introduction" {
		t.Fatalf("first heading = %q, want Introduction", h)
	}
	if !s.RichHasBold() {
		t.Fatal("parsed document should carry a bold run from \\textbf")
	}

	before := strings.Count(s.Source(), `\textbf`)

	// Bold the plain paragraph (block index 2) and leave WYSIWYG: the edited
	// document is written back to the source editor as LaTeX.
	s.RichSelectBlock(2)
	s.RichToggleStrong()
	s.ToggleWysiwyg()
	if s.WysiwygActive() {
		t.Fatal("toggle did not deactivate")
	}
	src := s.Source()
	if after := strings.Count(src, `\textbf`); after != before+1 {
		t.Fatalf("\\textbf count = %d, want %d (one more after emboldening)", after, before+1)
	}
	if !strings.Contains(src, `\section{Introduction}`) {
		t.Fatalf("round-tripped source lost the section:\n%s", src)
	}
	if !strings.Contains(src, `\begin{itemize}`) {
		t.Fatalf("round-tripped source lost the list:\n%s", src)
	}
}

// TestWysiwygV02Nodes is the go-richdoc v0.2.0 proof: a document carrying a
// \footnote, a \ref, a \cite and a \section+\label parses into the RichEditor as
// Footnote / CrossRef inlines and a Heading with an ID (the marker + accent runs
// the RichEditor paints), and toggling back to Source round-trips every construct
// unchanged.
func TestWysiwygV02Nodes(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysRefSnippet)

	s.ToggleWysiwyg()
	if !s.WysiwygActive() || s.WysiwygParseError() != "" {
		t.Fatalf("activation failed: active=%v err=%q", s.WysiwygActive(), s.WysiwygParseError())
	}

	// The RichEditor holds the v0.2 reference inlines — the nodes it renders as a
	// superscript footnote marker and an accent-coloured crossref run.
	if got := s.WysiwygFootnoteCount(); got != 1 {
		t.Fatalf("footnote count = %d, want 1", got)
	}
	if got := s.WysiwygCrossRefCount(); got != 2 { // \ref + \cite
		t.Fatalf("crossref count = %d, want 2 (\\ref + \\cite)", got)
	}
	// \section{Intro}\label{sec:i} folds the label into the heading's anchor id.
	if got := s.RichFirstHeadingID(); got != "sec:i" {
		t.Fatalf("first heading id = %q, want sec:i", got)
	}
	if got := s.RichFirstHeading(); got != "Intro" {
		t.Fatalf("first heading = %q, want Intro", got)
	}
	if txt := s.RichPlainText(); !strings.Contains(txt, "a note") || !strings.Contains(txt, "Intro") {
		t.Fatalf("plain text = %q, want it to carry the heading + footnote body", txt)
	}

	// Draw the active WYSIWYG tab so the RichEditor paints its footnote marker and
	// crossref run (a headless render, asserting no panic at the surface size).
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// Toggle back to Source: the written LaTeX preserves every v0.2 construct.
	s.ToggleWysiwyg()
	if s.WysiwygActive() {
		t.Fatal("toggle back did not deactivate")
	}
	src := s.Source()
	for _, want := range []string{`\footnote{`, `\label{sec:i}`, `\ref{sec:i}`, `\cite{knuth}`} {
		if !strings.Contains(src, want) {
			t.Fatalf("round-tripped source lost %q:\n%s", want, src)
		}
	}
}

// TestWysiwygParseErrorBounces feeds malformed LaTeX (an unclosed environment):
// enter() records the error and bounces the strip back to Source, and the error
// band is painted while inactive.
func TestWysiwygParseErrorBounces(t *testing.T) {
	s := newWysState(t)
	s.SetSource("\\begin{itemize}\n\\item orphan") // never closed -> latex.Parse fails
	s.ToggleWysiwyg()
	if s.WysiwygActive() {
		t.Fatal("a parse failure must not activate WYSIWYG")
	}
	if s.WysiwygParseError() == "" {
		t.Fatal("expected a recorded parse error")
	}
	if s.ActiveEditorTab() != tabSource {
		t.Fatalf("a parse failure must bounce to Source, tab = %d", s.ActiveEditorTab())
	}
	// The error band is painted while inactive.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
}

// TestWysiwygEventRouting drives the State-level event hooks so a real pointer/
// key path exercises the RichEditor while active.
func TestWysiwygEventRouting(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)

	// Click the "WYSIWYG" tab in the editor-pane strip to activate the mode.
	tb := s.wysiwyg().tabs.TabRect(tabWysiwyg)
	if !s.HandleClick(tb.X+tb.W/2, tb.Y+tb.H/2) {
		t.Fatal("WYSIWYG tab click not consumed")
	}
	if !s.WysiwygActive() {
		t.Fatal("clicking the WYSIWYG tab did not activate")
	}

	// Click on the formatting toolbar strip (a one-shot verb, no drag capture).
	tr := s.wysiwyg().toolbarRect
	if !s.HandleClick(tr.X+tr.W/2, tr.Y+tr.H/2) {
		t.Fatal("toolbar click not consumed")
	}

	// Click inside the RichEditor overlay, then drag + release (selection).
	er := s.wysiwyg().editor.Bounds()
	cx, cy := er.X+er.W/2, er.Y+er.H/3
	if !s.HandleClick(cx, cy) {
		t.Fatal("editor click not routed to RichEditor")
	}
	if !s.HandleMove(cx+20, cy+10) {
		t.Fatal("drag not routed to RichEditor")
	}
	if !s.HandleRelease(cx+20, cy+10) {
		t.Fatal("release not routed to RichEditor")
	}

	// Type a character, a plain key, and a Shift+navigation key.
	if !s.HandleChar("Z") {
		t.Fatal("char not routed to RichEditor")
	}
	if !s.HandleKeyDown("ArrowRight") {
		t.Fatal("key not routed to RichEditor")
	}
	if !s.HandleKeyDown("Shift+ArrowLeft") {
		t.Fatal("shift-key not routed to RichEditor")
	}
	// Wheel scroll over the overlay.
	if !s.HandleScroll(cx, cy, 0, 3) {
		t.Fatal("scroll not routed to RichEditor")
	}

	// Re-layout while active so wysiwygLayout re-sizes the RichEditor overlay.
	s.Resize(testW-40, testH-20)

	// Draw the active tab (RichEditor + toolbar strip).
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
}

// TestWysiwygInactiveHooksPassThrough asserts the hooks return false while the
// mode is inactive, so normal editor input is untouched.
func TestWysiwygInactiveHooksPassThrough(t *testing.T) {
	s := newWysState(t)
	if s.wysiwygMove(10, 10) || s.wysiwygRelease(10, 10) {
		t.Fatal("no drag captured, move/release must pass through")
	}
	if s.wysiwygScroll(10, 200, 3) {
		t.Fatal("inactive scroll must pass through")
	}
	if s.wysiwygChar("a") || s.wysiwygKey("ArrowLeft") {
		t.Fatal("inactive char/key must pass through")
	}
	// A body click while inactive (not over the strip nor the RichEditor) passes
	// through.
	if s.wysiwygClick(s.editor.Bounds().X+5, s.editor.Bounds().Y+5) {
		t.Fatal("inactive body click must pass through")
	}
}

// TestWysiwygIntrospectionEdges covers the empty-heading / no-bold / no-refs /
// out-of-range / default-block-length branches.
func TestWysiwygIntrospectionEdges(t *testing.T) {
	doc := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.List{Items: []richdoc.ListItem{{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Text{Value: "x"}}}}}}},
	}}
	s := newWysState(t)
	s.wysiwyg().editor.SetDocument(doc)

	if h := s.RichFirstHeading(); h != "" {
		t.Fatalf("no heading, got %q", h)
	}
	if id := s.RichFirstHeadingID(); id != "" {
		t.Fatalf("no heading id, got %q", id)
	}
	if s.RichHasBold() {
		t.Fatal("no bold expected")
	}
	if s.WysiwygFootnoteCount() != 0 || s.WysiwygCrossRefCount() != 0 || s.WysiwygAnchorCount() != 0 {
		t.Fatal("no reference inlines expected")
	}
	s.RichSelectBlock(-1)  // out of range (negative): no-op
	s.RichSelectBlock(0)   // a List block: blockRuneLen default branch -> 0
	s.RichSelectBlock(999) // out of range (high): no-op

	// blockRuneLen over each editable block kind + the default branch.
	if got := blockRuneLen(richdoc.Heading{Inlines: []richdoc.Inline{richdoc.Text{Value: "abc"}}}); got != 3 {
		t.Fatalf("heading len = %d, want 3", got)
	}
	if got := blockRuneLen(richdoc.CodeBlock{Text: "code"}); got != 4 {
		t.Fatalf("code len = %d, want 4", got)
	}
	if got := blockRuneLen(richdoc.ThematicBreak{}); got != 0 {
		t.Fatalf("rule len = %d, want 0", got)
	}
}

// TestWysiwygRefCounterAnchor covers refCounter's Anchor branch with a point
// Anchor (no heading to fold into), which a document can carry directly.
func TestWysiwygRefCounterAnchor(t *testing.T) {
	s := newWysState(t)
	s.wysiwyg().editor.SetDocument(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{
			richdoc.Anchor{ID: "pt"},
			richdoc.Footnote{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Text{Value: "n"}}}}},
			richdoc.CrossRef{Target: "pt", Kind: richdoc.RefLabel},
		}},
	}})
	if s.WysiwygAnchorCount() != 1 {
		t.Fatalf("anchor count = %d, want 1", s.WysiwygAnchorCount())
	}
	if s.WysiwygFootnoteCount() != 1 {
		t.Fatalf("footnote count = %d, want 1", s.WysiwygFootnoteCount())
	}
	if s.WysiwygCrossRefCount() != 1 {
		t.Fatalf("crossref count = %d, want 1", s.WysiwygCrossRefCount())
	}
}

// TestWysiwygEditorTabState covers the reactive active-editor-tab hooks: the tab
// index reported by ActiveEditorTab tracks SetEditorTab / the toggle.
func TestWysiwygEditorTabState(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)

	if got := s.ActiveEditorTab(); got != tabSource {
		t.Fatalf("initial active tab = %d, want Source (%d)", got, tabSource)
	}
	s.SetEditorTab(tabWysiwyg)
	if s.ActiveEditorTab() != tabWysiwyg || !s.WysiwygActive() {
		t.Fatalf("SetEditorTab(WYSIWYG): tab=%d active=%v", s.ActiveEditorTab(), s.WysiwygActive())
	}
	s.ToggleWysiwyg() // flip back to Source
	if s.ActiveEditorTab() != tabSource || s.WysiwygActive() {
		t.Fatalf("ToggleWysiwyg back: tab=%d active=%v", s.ActiveEditorTab(), s.WysiwygActive())
	}
}

// TestWysiwygTabRectsAndToolbar covers the editor-tab rect hook and the
// formatting-toolbar introspection (visible flag, rect, button count/rects,
// pressed state incl. the out-of-range guard, current block kind).
func TestWysiwygTabRectsAndToolbar(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)

	if r := s.EditorTabRect(tabSource); r[2] <= 0 || r[3] <= 0 {
		t.Fatalf("source tab rect non-positive: %v", r)
	}
	if s.RichToolbarVisible() {
		t.Fatal("toolbar must be hidden on the Source tab")
	}

	s.SetEditorTab(tabWysiwyg)
	if !s.RichToolbarVisible() {
		t.Fatal("toolbar must be visible on the WYSIWYG tab")
	}
	if n := s.RichToolbarButtonCount(); n != 12 {
		t.Fatalf("toolbar button count = %d, want 12", n)
	}
	if rects := s.RichToolbarButtonRects(); len(rects) != 12 {
		t.Fatalf("toolbar button rects = %d, want 12", len(rects))
	}
	if r := s.RichToolbarRect(); r[3] <= 0 {
		t.Fatalf("toolbar strip rect non-positive height: %v", r)
	}
	// A block button's pressed state is a bool; the out-of-range index guards false.
	_ = s.RichToolbarButtonPressed(rtbBold)
	if s.RichToolbarButtonPressed(-1) || s.RichToolbarButtonPressed(999) {
		t.Fatal("out-of-range toolbar button must report not-pressed")
	}
	// The caret block kind is readable while active.
	_ = s.RichCurrentBlockKind()
}

// TestWysiwygLayoutShortPane enters WYSIWYG then resizes to a very short surface,
// covering layoutBounds's toolbar-height clamp (toolbar taller than the editor
// region) and a draw at that degenerate size.
func TestWysiwygLayoutShortPane(t *testing.T) {
	s := newWysState(t)
	s.SetSource(wysSnippet)
	s.SetEditorTab(tabWysiwyg)
	s.Resize(testW, 90) // editor region shorter than the toolbar strip
	tb := s.RichToolbarRect()
	er := s.wysiwyg().editor.Bounds()
	if er.H < 0 {
		t.Fatalf("editor height must never go negative: %d (toolbar %v)", er.H, tb)
	}
	buf := make([]byte, testW*90*4)
	s.Draw(buf)
}

// TestBlockRuneLenSelectionOnParagraph makes RichSelectBlock hit its valid path
// with a Paragraph and confirms the selection covers the block.
func TestBlockRuneLenSelectionOnParagraph(t *testing.T) {
	s := newWysState(t)
	s.wysiwyg().editor.SetDocument(&richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Text{Value: "hello"}}},
	}})
	s.RichSelectBlock(0)
	sel := s.wysiwyg().editor.Selection().Get()
	if sel.Start != (toolkit.DocPos{Block: 0, Off: 0}) || sel.End.Off != 5 {
		t.Fatalf("selection = %+v, want full first block", sel)
	}
}
