// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
	"github.com/go-widgets/toolkit"

	odf "github.com/go-odf/odf"
	rtf "github.com/go-rtf/rtf"
)

// wysSnippet is a small LaTeX document exercising the three structures the
// headless harness also asserts on: a \section, an existing \textbf, a plain
// paragraph to embolden, and an itemize list.
const wysSnippet = `\section{Introduction}

This has \textbf{bold} already.

A plain line to embolden.

\begin{itemize}
\item First
\item Second
\end{itemize}`

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
	if got := s.WysiwygFormat(); got != "LaTeX" {
		t.Fatalf("default format = %q, want LaTeX", got)
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

func TestWysiwygMarkdownRoundTrip(t *testing.T) {
	s := newWysState(t)
	const md = "# Title\n\nSome **bold** and *italic*.\n\n- a\n- b\n"
	s.SetSource(md)
	s.SetWysiwygFormat(1) // Markdown

	s.ToggleWysiwyg()
	if !s.WysiwygActive() || s.WysiwygFormat() != "Markdown" {
		t.Fatalf("markdown activation failed: active=%v format=%q", s.WysiwygActive(), s.WysiwygFormat())
	}
	if h := s.RichFirstHeading(); h != "Title" {
		t.Fatalf("first heading = %q, want Title", h)
	}
	if !s.RichHasBold() {
		t.Fatal("markdown **bold** did not survive into the document")
	}
	if txt := s.RichPlainText(); !strings.Contains(txt, "italic") {
		t.Fatalf("plain text = %q, want it to contain italic", txt)
	}

	s.ToggleWysiwyg() // write back as Markdown
	if src := s.Source(); !strings.Contains(src, "# Title") || !strings.Contains(src, "**bold**") {
		t.Fatalf("markdown round-trip lost structure:\n%s", src)
	}
}

func TestWysiwygRTFImportExport(t *testing.T) {
	doc := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Heading{Level: 1, Inlines: []richdoc.Inline{richdoc.Text{Value: "Hi"}}},
		richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Strong{Inlines: []richdoc.Inline{richdoc.Text{Value: "b"}}}}},
	}}
	src, err := rtf.Write(doc)
	if err != nil {
		t.Fatalf("rtf.Write: %v", err)
	}

	s := newWysState(t)
	if err := s.WysiwygImport(FormatRTF, src); err != nil {
		t.Fatalf("import RTF: %v", err)
	}
	if !s.WysiwygActive() || s.WysiwygFormat() != "RTF" {
		t.Fatalf("RTF import state: active=%v format=%q", s.WysiwygActive(), s.WysiwygFormat())
	}
	if !s.RichHasBold() {
		t.Fatal("RTF bold run lost on import")
	}
	// A non-textual format leaves the source editor untouched on exit.
	before := s.Source()
	s.ToggleWysiwyg()
	if s.Source() != before {
		t.Fatal("leaving a non-textual WYSIWYG must not overwrite the source editor")
	}
	out, err := s.WysiwygExport(FormatRTF)
	if err != nil {
		t.Fatalf("export RTF: %v", err)
	}
	if _, err := rtf.Parse(out); err != nil {
		t.Fatalf("exported RTF does not re-parse: %v", err)
	}
}

func TestWysiwygODTImportExport(t *testing.T) {
	doc := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.Heading{Level: 1, Inlines: []richdoc.Inline{richdoc.Text{Value: "Doc"}}},
		richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Text{Value: "body"}}},
	}}
	pkg, err := odf.Write(doc)
	if err != nil {
		t.Fatalf("odf.Write: %v", err)
	}
	s := newWysState(t)
	if err := s.WysiwygImport(FormatODT, pkg); err != nil {
		t.Fatalf("import ODT: %v", err)
	}
	if s.RichFirstHeading() != "Doc" {
		t.Fatalf("ODT heading lost: %q", s.RichFirstHeading())
	}
	out, err := s.WysiwygExport(FormatODT)
	if err != nil {
		t.Fatalf("export ODT: %v", err)
	}
	if _, err := odf.Parse(out); err != nil {
		t.Fatalf("exported ODT does not re-parse: %v", err)
	}
}

// TestWysiwygParseErrorStaysInSource feeds a plain-text buffer to the ODT reader
// (which wants a zip): the mode records the error and stays in source mode.
func TestWysiwygParseErrorStaysInSource(t *testing.T) {
	s := newWysState(t)
	s.SetSource("this is not a zip container")
	s.SetWysiwygFormat(2) // ODT
	s.ToggleWysiwyg()
	if s.WysiwygActive() {
		t.Fatal("a parse failure must not activate WYSIWYG")
	}
	if s.WysiwygParseError() == "" {
		t.Fatal("expected a recorded parse error")
	}
	// The error band is painted while inactive.
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
}

// TestWysiwygImportExportUnknownFormat covers the unregistered-format guards.
func TestWysiwygImportExportUnknownFormat(t *testing.T) {
	s := newWysState(t)
	if err := s.WysiwygImport(Format(999), []byte("x")); err != errUnknownFormat {
		t.Fatalf("import unknown = %v, want errUnknownFormat", err)
	}
	if _, err := s.WysiwygExport(Format(999)); err != errUnknownFormat {
		t.Fatalf("export unknown = %v, want errUnknownFormat", err)
	}
	if errUnknownFormat.Error() != "unknown format" {
		t.Fatalf("error text = %q", errUnknownFormat.Error())
	}
}

// TestWysiwygImportParseError covers WysiwygImport's parse-error path with a
// synthetic failing codec (also the seam future formats plug in through).
func TestWysiwygImportParseError(t *testing.T) {
	const f = Format(90)
	formatRegistry[f] = Codec{Name: "FailParse", Textual: true,
		Parse: func([]byte) (*richdoc.Document, error) { return nil, wysiwygError("boom") },
		Write: func(*richdoc.Document) ([]byte, error) { return nil, nil }}
	defer delete(formatRegistry, f)

	s := newWysState(t)
	if err := s.WysiwygImport(f, []byte("x")); err == nil {
		t.Fatal("expected a parse error")
	}
	if s.WysiwygActive() {
		t.Fatal("a failed import must not activate")
	}
	if s.WysiwygParseError() != "boom" {
		t.Fatalf("parseErr = %q", s.WysiwygParseError())
	}
}

// TestWysiwygLeaveWriteError covers leave's write-error branch: a textual codec
// whose Parse succeeds but Write fails. It drives the transition through the tab
// strip (Set WYSIWYG to enter, Set Source to leave), the real user path.
func TestWysiwygLeaveWriteError(t *testing.T) {
	const f = Format(91)
	formatRegistry[f] = Codec{Name: "FailWrite", Textual: true,
		Parse: func([]byte) (*richdoc.Document, error) { return &richdoc.Document{}, nil },
		Write: func(*richdoc.Document) ([]byte, error) { return nil, wysiwygError("nope") }}
	defer delete(formatRegistry, f)

	s := newWysState(t)
	w := s.wysiwyg()
	w.format = f
	s.SetEditorTab(tabWysiwyg) // fires enter(): Parse succeeds, WYSIWYG becomes active
	if !w.active() {
		t.Fatal("selecting the WYSIWYG tab should have entered (parse succeeded)")
	}
	s.SetEditorTab(tabSource) // fires leave(): Write fails, error recorded
	if s.WysiwygParseError() != "nope" {
		t.Fatalf("write error not recorded: %q", s.WysiwygParseError())
	}
}

// TestRegisterFormatReRegistration covers the "already seen" branch (the codec is
// overwritten but the picker order does not grow).
func TestRegisterFormatReRegistration(t *testing.T) {
	before := len(formatOrder)
	RegisterFormat(FormatLaTeX, formatRegistry[FormatLaTeX])
	if len(formatOrder) != before {
		t.Fatalf("re-registration grew formatOrder %d -> %d", before, len(formatOrder))
	}
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

	// Open the format picker and draw with the popover up, then click it.
	pk := s.wysiwyg().picker.Bounds()
	if !s.HandleClick(pk.X+pk.W/2, pk.Y+pk.H/2) {
		t.Fatal("picker click not consumed")
	}
	if !s.wysiwyg().picker.Open().Get() {
		t.Fatal("picker did not open")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // draws the RichEditor overlay AND the open format popover
	pb := s.wysiwyg().picker.PopoverBounds()
	s.HandleClick(pb.X+pb.W/2, pb.Y+2) // popover intercept closes it
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
	// A toolbar click that misses both WYSIWYG controls passes through.
	if s.wysiwygClick(1, 1) {
		t.Fatal("empty toolbar spot must pass through")
	}
	// A body click while inactive passes through (not over the RichEditor).
	if s.wysiwygClick(s.editor.Bounds().X+5, s.editor.Bounds().Y+5) {
		t.Fatal("inactive body click must pass through")
	}
}

// TestWysiwygIntrospectionEdges covers the empty-heading / no-bold / out-of-range
// / default-block-length branches.
func TestWysiwygIntrospectionEdges(t *testing.T) {
	doc := &richdoc.Document{Blocks: []richdoc.Block{
		richdoc.List{Items: []richdoc.ListItem{{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{richdoc.Text{Value: "x"}}}}}}},
	}}
	s := newWysState(t)
	s.wysiwyg().editor.SetDocument(doc)

	if h := s.RichFirstHeading(); h != "" {
		t.Fatalf("no heading, got %q", h)
	}
	if s.RichHasBold() {
		t.Fatal("no bold expected")
	}
	s.RichSelectBlock(-1) // out of range: no-op
	s.RichSelectBlock(0)  // a List block: blockRuneLen default branch -> 0

	// blockRuneLen over each editable block kind.
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

// TestWysiwygEditorTabState covers the reactive active-editor-tab hooks: the tab
// index reported by ActiveEditorTab tracks SetEditorTab / the toggle, and the
// non-textual Source note is painted when a binary format is selected on Source.
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

	// A non-textual format on the Source tab paints the "binary format" note over
	// the CodeEditor (no human-editable source for ODT/RTF).
	s.SetWysiwygFormat(3) // RTF (non-textual)
	if s.ActiveEditorTab() != tabSource {
		t.Fatal("selecting a format must not change the active tab")
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // exercises the non-textual Source-tab note branch
}

// TestWysiwygLayoutNarrowPane drives the divider hard left so the editor pane is
// narrower than the format DropDown, covering wysiwygLayout's picker-shrink and
// zero-width clamps.
func TestWysiwygLayoutNarrowPane(t *testing.T) {
	s := newWysState(t)
	s.paned.MoveHandle(10) // clamps to the minimum left width
	s.layout()             // re-runs applyLeftSplit + wysiwygLayout at that width
	if got := s.FormatPickerRect(); got[2] != 0 {
		t.Fatalf("picker width in a 10px pane = %d, want 0 (clamped)", got[2])
	}
	// Draw must not panic at this degenerate size.
	buf := make([]byte, testW*testH*4)
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
