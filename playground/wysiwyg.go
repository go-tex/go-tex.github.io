// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

// This file adds the playground's WYSIWYG editing mode. The go-tex playground is
// TeX-specific, so the mode is deliberately LaTeX-only: a "Source | WYSIWYG" tab
// strip and a formatting toolbar pinned to the top of the editor pane, a toolkit
// RichEditor shown in place of the CodeEditor on the WYSIWYG tab, and the
// LaTeX-source<->document round-trip through the go-richdoc/latex converter.
// app.go and the wasm driver carry only a handful of one-line additive hooks
// (s.wysiwyg*(...)).
//
// The multi-format machinery (a registry over many go-richdoc converters, ODT/RTF
// import/export) lives in the SHARED components — the toolkit RichEditor and the
// go-richdoc converters — for other consumers such as loom; this playground wires
// exactly one converter, LaTeX, because that is what the go-tex compile -> render
// pipeline consumes.
//
// The strip is the shared [toolkit.FolderTabs] — the SAME widget the render pane
// uses for its Rendered│Log tabs — placed at the top of the editor (left) pane so
// the two panes read consistently. The active editor tab is the single source of
// truth (an [mvvm.Observable] on the strip, mirroring the render pane's tab
// state): there is no shadow "active" bool. Selecting WYSIWYG parses the source
// into the RichEditor; selecting Source writes the edited document back.

import (
	"strings"

	"github.com/go-richdoc/richdoc"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	latex "github.com/go-richdoc/latex"
)

// Editor-pane tab indices. Source shows the CodeEditor (LaTeX source, completion,
// minimap, compile-on-edit); WYSIWYG shows the RichEditor. The strip's reactive
// selection IS the mode's active state — see [wysiwyg.active].
const (
	tabSource  = 0
	tabWysiwyg = 1
)

// parseLaTeX / writeLaTeX are the mode's LaTeX codec: the neutral richdoc model
// in and out through go-richdoc/latex. They are named indirections so the single
// place the converter is wired is obvious and the enter/leave paths read the same
// whichever way the document is flowing.
var (
	parseLaTeX = latex.Parse
	writeLaTeX = latex.Write
)

// wysiwyg is the mode's whole state: the editor-pane tab strip and the formatting
// toolbar (both pinned to the top of the editor pane), the RichEditor shown on the
// WYSIWYG tab, a parse-error string (shown while it bounces back to Source), a
// pointer-capture flag for a drag inside the RichEditor, the strip rect for click
// routing and a "bouncing" flag raised while a failed enter() reverts the strip to
// Source, so the resulting leave() keeps the parse error instead of writing back.
type wysiwyg struct {
	s *State

	editor  *toolkit.RichEditor
	toolbar *toolkit.RichEditorToolbar
	tabs    *toolkit.FolderTabs

	parseErr string
	pressing bool

	strip       toolkit.Rect
	toolbarRect toolkit.Rect
	bouncing    bool
}

// newWysiwyg builds the mode's widgets over State s. The RichEditor starts empty
// (it is fed a parsed document when the WYSIWYG tab is first selected); the tab
// strip flips the mode.
func newWysiwyg(s *State) *wysiwyg {
	w := &wysiwyg{s: s, editor: toolkit.NewRichEditor(nil)}
	// The formatting toolbar is bound to the one RichEditor for the whole app's
	// life: it drives the editor's verbs and lights the buttons whose formatting is
	// in force at the caret (it subscribes to the editor's Caret/Selection/Doc). It
	// is shown only on the WYSIWYG tab, laid out directly under the tab strip.
	w.toolbar = toolkit.NewRichEditorToolbar(w.editor)
	w.tabs = toolkit.NewFolderTabs([]string{"Source", "WYSIWYG"}, tabSource)
	// The active editor tab is the single source of truth (mirrors the render
	// pane's Rendered│Log strip): selecting WYSIWYG parses the source into the
	// RichEditor, selecting Source writes the edited document back. A parse failure
	// bounces the strip back to Source (see enter/leave and the bouncing flag). The
	// strip lives for the whole app, so the unsubscribe handle is dropped.
	w.tabs.Selected().Subscribe(func(idx int) {
		if idx == tabWysiwyg {
			w.enter()
		} else {
			w.leave()
		}
	})
	return w
}

// wysiwyg lazily builds and returns the mode, so app.go carries only a field.
func (s *State) wysiwyg() *wysiwyg {
	if s.wys == nil {
		s.wys = newWysiwyg(s)
	}
	return s.wys
}

// active reports whether the WYSIWYG tab is selected — read straight from the
// strip's reactive Observable (the single source of truth, mirroring
// [rightPane.isLog]); there is no shadow copy of the mode to keep in sync.
func (w *wysiwyg) active() bool { return w.tabs.Selected().Get() == tabWysiwyg }

// selectTab drives the strip's reactive selection, firing the enter/leave
// transition through the strip subscriber — the single mutate path a host toggle
// and a strip click both take.
func (w *wysiwyg) selectTab(tab int) { w.tabs.Selected().Set(tab) }

// toggle_ flips the active editor tab: Source <-> WYSIWYG. It Sets the strip's
// reactive selection, so the enter/leave side effects run through the subscriber
// exactly as a strip click would.
func (w *wysiwyg) toggle_() {
	if w.active() {
		w.selectTab(tabSource)
	} else {
		w.selectTab(tabWysiwyg)
	}
}

// enter is run when the WYSIWYG tab becomes active: it parses the current LaTeX
// source into the RichEditor. A parse failure records the error and bounces the
// strip back to Source (the documented graceful path — malformed LaTeX, e.g. an
// unclosed environment), so the strip's selection and the visible editor never
// disagree.
func (w *wysiwyg) enter() {
	doc, err := parseLaTeX([]byte(w.s.Source()))
	if err != nil {
		w.parseErr = err.Error()
		w.bounceToSource()
		w.s.dirty = true
		return
	}
	w.parseErr = ""
	w.editor.SetDocument(doc)
	w.applyWysiwygBounds()
	w.editor.Focused().Set(true)
	w.s.dirty = true
}

// leave is run when the Source tab becomes active: the edited document is written
// back into the source editor as LaTeX (driving the existing compile -> render
// pipeline). When the transition is the guarded revert of a failed enter() (the
// bouncing flag), the recorded parse error is kept and nothing is written back —
// otherwise the freshly-set error would be wiped by an empty write.
func (w *wysiwyg) leave() {
	if w.bouncing {
		w.bouncing = false
		return
	}
	w.editor.Focused().Set(false)
	// writeLaTeX never fails (it always emits a well-formed preamble + body), so
	// the result is used directly; there is no error path to guard.
	out, _ := writeLaTeX(w.editor.Document())
	w.parseErr = ""
	w.s.SetSource(string(out))
	w.s.dirty = true
}

// bounceToSource reverts the strip to the Source tab after a failed enter(). The
// [mvvm.Observable] is mid-notification here (enter runs from its subscriber), so
// the Set is deferred and re-notifies the subscriber with tabSource; the bouncing
// flag makes that re-entrant leave() a no-op so the parse error survives.
func (w *wysiwyg) bounceToSource() {
	w.bouncing = true
	w.tabs.Selected().Set(tabSource)
}

// --- layout ---------------------------------------------------------------

// stripHeight is the device height reserved at the top of the editor pane for the
// tab strip — the shared FolderTabs height, so the editor strip lines up exactly
// with the render pane's Rendered│Log strip. applyLeftSplit reserves it above the
// CodeEditor + minimap.
func (w *wysiwyg) stripHeight() int { return toolkit.FolderTabsHeight() }

// toolbarShown reports whether the formatting toolbar is visible. It shows ONLY
// while the WYSIWYG tab is active; on the Source tab (the CodeEditor) it is hidden.
func (w *wysiwyg) toolbarShown() bool { return w.active() }

// toolbarHeight is the device height the toolbar reserves above the RichEditor —
// its measured icon-strip height (metric-scaled by the toolkit) — or 0 when it is
// hidden, so the RichEditor claims the whole editor region on the Source path.
func (w *wysiwyg) toolbarHeight() int {
	if !w.toolbarShown() {
		return 0
	}
	_, h := w.toolbar.Measure(0, 0)
	return h
}

// layoutBounds splits the editor pane (below the Source│WYSIWYG tab strip) into
// the toolbar strip pinned at the top and the RichEditor region below it. The
// toolbar spans the full editor width (its Surface fill reads as a strip, like the
// tab strip above it); the RichEditor takes the remainder. With the toolbar hidden
// the toolbar rect is zero-height and the editor claims the whole region.
func (w *wysiwyg) layoutBounds() (toolbar, editor toolkit.Rect) {
	er := w.s.editor.Bounds()
	th := w.toolbarHeight()
	if th > er.H {
		th = er.H
	}
	toolbar = toolkit.Rect{X: er.X, Y: er.Y, W: er.W, H: th}
	editor = toolkit.Rect{X: er.X, Y: er.Y + th, W: er.W, H: er.H - th}
	return
}

// applyWysiwygBounds pins the formatting toolbar to the top of the editor pane and
// sizes the RichEditor to the region below it. It is the single seam every path
// that (re)establishes the RichEditor bounds routes through — layout, a divider
// drag, a draw and enter — so the reserved strip and the click routing never
// disagree.
func (w *wysiwyg) applyWysiwygBounds() {
	tb, ed := w.layoutBounds()
	w.toolbarRect = tb
	w.toolbar.SetBounds(tb)
	w.editor.SetBounds(ed)
}

// wysiwygLayout pins the Source│WYSIWYG strip to the top of the editor (left)
// pane; while the WYSIWYG tab is active it sizes the RichEditor to the editor
// region (below the strip). Called at the tail of State.layout (and after a
// divider drag), so the strip tracks the left pane's current width.
func (s *State) wysiwygLayout() {
	w := s.wysiwyg()
	pr := s.paned.Bounds()
	leftW := s.paned.Position().Get() // Paned clamps the divider to [10, W-10], never < 0
	h := w.stripHeight()
	w.strip = toolkit.Rect{X: pr.X, Y: pr.Y, W: leftW, H: h}
	// The FolderTabs spans the whole strip width (its background + bottom border
	// read as the pane's top edge, matching the render pane); it hit-tests only the
	// label-width tab rects.
	w.tabs.SetBounds(w.strip)
	if w.active() {
		// Reserve the formatting-toolbar strip above the RichEditor and size the
		// editor to the region below it (tracks the left pane's current width on a
		// divider drag / resize, exactly like the tab strip above).
		w.applyWysiwygBounds()
	}
}

// --- draw -----------------------------------------------------------------

// wysiwygDraw paints the editor-pane tab strip, then, on the WYSIWYG tab, the
// formatting toolbar strip and the RichEditor over the editor region. A pending
// parse error is shown as a band over the source editor while the strip bounces
// back to Source. Called from State.Draw.
func (s *State) wysiwygDraw(p painter.Painter) {
	w := s.wysiwyg()
	w.tabs.Draw(p, s.theme)
	if w.active() {
		// Re-establish the toolbar + RichEditor bounds every frame (the editor pane
		// may have been re-laid by a divider drag), then paint the formatting toolbar
		// strip directly under the tab strip and the RichEditor below it.
		w.applyWysiwygBounds()
		w.toolbar.Draw(p, s.theme)
		w.editor.Draw(p, s.theme)
	}
	if w.parseErr != "" {
		r := s.editor.Bounds()
		band := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: toolkit.Scaled(22)}
		p.FillRect(band, s.theme.SurfaceAlt)
		toolkit.DrawText(p, r.X+toolkit.Scaled(8), r.Y+toolkit.Scaled(6), "Parse error: "+w.parseErr, s.theme.Accent)
	}
}

// --- input ----------------------------------------------------------------

// wysiwygClick routes a pointer press: the editor-pane tab strip (the
// Source│WYSIWYG tabs) or (while the WYSIWYG tab is active) the formatting toolbar
// or the RichEditor. It returns true when it consumed the press so
// State.HandleClick stops. Called before the generic toolbar/editor routing.
func (s *State) wysiwygClick(x, y int) bool {
	w := s.wysiwyg()
	if w.strip.Contains(x, y) {
		tr := w.tabs.Bounds()
		w.tabs.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - tr.X, Y: y - tr.Y})
		s.dirty = true
		return true
	}
	if w.active() && w.toolbarRect.Contains(x, y) {
		// The formatting toolbar sits between the tab strip and the RichEditor:
		// forward a press over it to the toolbar (parent-local coords, the same
		// convention the strip uses), so the click lands on the icon button under the
		// pointer rather than the editor. No drag capture — a toolbar press is a
		// one-shot verb, not a selection drag.
		w.toolbar.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - w.toolbarRect.X, Y: y - w.toolbarRect.Y})
		s.dirty = true
		return true
	}
	if w.active() && w.editor.Bounds().Contains(x, y) {
		r := w.editor.Bounds()
		w.editor.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - r.X, Y: y - r.Y})
		w.pressing = true
		s.dirty = true
		return true
	}
	return false
}

// wysiwygMove forwards a captured drag to the RichEditor (extending a selection).
func (s *State) wysiwygMove(x, y int) bool {
	w := s.wysiwyg()
	if !w.pressing {
		return false
	}
	r := w.editor.Bounds()
	w.editor.OnEvent(toolkit.Event{Kind: toolkit.EventMouseDrag, X: x - r.X, Y: y - r.Y})
	s.dirty = true
	return true
}

// wysiwygRelease ends a captured RichEditor drag.
func (s *State) wysiwygRelease(x, y int) bool {
	w := s.wysiwyg()
	if !w.pressing {
		return false
	}
	w.pressing = false
	s.dirty = true
	return true
}

// wysiwygScroll forwards a wheel scroll to the RichEditor while active and under
// the pointer.
func (s *State) wysiwygScroll(x, y, dy int) bool {
	w := s.wysiwyg()
	if !w.active() || !w.editor.Bounds().Contains(x, y) {
		return false
	}
	r := w.editor.Bounds()
	w.editor.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, X: x - r.X, Y: y - r.Y, Delta: dy})
	s.dirty = true
	return true
}

// wysiwygChar routes a printable character to the focused RichEditor.
func (s *State) wysiwygChar(code string) bool {
	w := s.wysiwyg()
	if !w.active() || !w.editor.Focused().Get() {
		return false
	}
	w.editor.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: code})
	s.dirty = true
	return true
}

// wysiwygKey routes a named key (splitting a "Shift+"-prefixed navigation key
// into Code + the Shift modifier the RichEditor expects) to the focused editor.
func (s *State) wysiwygKey(code string) bool {
	w := s.wysiwyg()
	if !w.active() || !w.editor.Focused().Get() {
		return false
	}
	ev := toolkit.Event{Kind: toolkit.EventKeyDown}
	if rest, ok := strings.CutPrefix(code, "Shift+"); ok {
		ev.Shift = true
		ev.Code = rest
	} else {
		ev.Code = code
	}
	w.editor.OnEvent(ev)
	s.dirty = true
	return true
}

// --- host / test introspection -------------------------------------------

// ToggleWysiwyg flips the active editor tab (Source <-> WYSIWYG) from the host
// (the wasm driver and the headless harness), driving the same enter/leave path a
// strip click takes.
func (s *State) ToggleWysiwyg() { s.wysiwyg().toggle_() }

// SetEditorTab selects the editor pane's tab (0 = Source, 1 = WYSIWYG) directly,
// driving the same enter/leave path a strip click takes — the host/headless hook
// for the reactive active-tab state.
func (s *State) SetEditorTab(idx int) { s.wysiwyg().selectTab(idx) }

// ActiveEditorTab is the selected editor-pane tab index (0 = Source, 1 = WYSIWYG),
// read from the strip's reactive Observable. Host/headless introspection.
func (s *State) ActiveEditorTab() int { return s.wysiwyg().tabs.Selected().Get() }

// EditorTabRect is the device rectangle of editor tab i (0 = Source, 1 = WYSIWYG)
// in surface coordinates, so a headless harness can click a tab precisely.
func (s *State) EditorTabRect(i int) [4]int {
	r := s.wysiwyg().tabs.TabRect(i)
	return [4]int{r.X, r.Y, r.W, r.H}
}

// WysiwygActive reports whether the RichEditor (the WYSIWYG tab) is currently
// shown.
func (s *State) WysiwygActive() bool { return s.wysiwyg().active() }

// WysiwygParseError is the last parse error message ("" when none).
func (s *State) WysiwygParseError() string { return s.wysiwyg().parseErr }

// RichBlockCount is the number of top-level blocks in the RichEditor document.
func (s *State) RichBlockCount() int { return len(s.wysiwyg().editor.Document().Blocks) }

// RichFirstHeading is the plain text of the document's first Heading block ("" if
// none), for a headless structure assertion.
func (s *State) RichFirstHeading() string {
	for _, b := range s.wysiwyg().editor.Document().Blocks {
		if h, ok := b.(richdoc.Heading); ok {
			return richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: h.Inlines}}})
		}
	}
	return ""
}

// RichFirstHeadingID is the ID (LaTeX \label anchor) of the document's first
// Heading block ("" if none / no anchor) — the v0.2.0 Heading.ID, so a headless
// test can prove `\section{...}\label{...}` folded the label into the heading.
func (s *State) RichFirstHeadingID() string {
	for _, b := range s.wysiwyg().editor.Document().Blocks {
		if h, ok := b.(richdoc.Heading); ok {
			return h.ID
		}
	}
	return ""
}

// RichHasBold reports whether any Strong (bold) inline exists anywhere in the
// document, so a headless test can assert a parsed \textbf survived.
func (s *State) RichHasBold() bool {
	v := &boldFinder{}
	richdoc.Walk(s.wysiwyg().editor.Document(), v)
	return v.found
}

// WysiwygFootnoteCount / WysiwygCrossRefCount / WysiwygAnchorCount count the
// v0.2.0 reference inlines the RichEditor holds — the marker the RichEditor paints
// as a superscript (Footnote), the accent run it paints for an inline-less
// CrossRef (\ref / \cite), and any point Anchor (\label). A headless test asserts
// on these to prove Parse yielded the v0.2 nodes and the editor adopted them.
func (s *State) WysiwygFootnoteCount() int { return s.refCounts().foot }
func (s *State) WysiwygCrossRefCount() int { return s.refCounts().xref }
func (s *State) WysiwygAnchorCount() int   { return s.refCounts().anchor }

// refCounts walks the current RichEditor document once, tallying the v0.2.0
// reference inlines.
func (s *State) refCounts() refCounter {
	v := &refCounter{}
	richdoc.Walk(s.wysiwyg().editor.Document(), v)
	return *v
}

// RichPlainText is the document's plain text, for a headless content assertion.
func (s *State) RichPlainText() string { return richdoc.PlainText(s.wysiwyg().editor.Document()) }

// RichSelectBlock selects block bi's whole editable content and parks the caret
// at its end, so a headless test can apply an inline verb to a real selection.
func (s *State) RichSelectBlock(bi int) {
	w := s.wysiwyg()
	d := w.editor.Document()
	if bi < 0 || bi >= len(d.Blocks) {
		return
	}
	n := blockRuneLen(d.Blocks[bi])
	end := toolkit.DocPos{Block: bi, Off: n}
	w.editor.Caret().Set(end)
	w.editor.Selection().Set(toolkit.DocSelection{Start: toolkit.DocPos{Block: bi}, End: end})
	s.dirty = true
}

// RichToggleStrong applies bold over the current RichEditor selection.
func (s *State) RichToggleStrong() {
	s.wysiwyg().editor.ToggleStrong()
	s.dirty = true
}

// Formatting-toolbar button indices, in the toolbar's grouped order (Inline,
// Block, Lists). They index [State.RichToolbarButtonRects] /
// [State.RichToolbarButtonPressed] so the host / headless harness can click and
// probe a specific button by name.
const (
	rtbBold = iota
	rtbItalic
	rtbStrikethrough
	rtbCode
	rtbParagraph
	rtbH1
	rtbH2
	rtbH3
	rtbQuote
	rtbCodeBlock
	rtbBullet
	rtbNumbered
)

// toolbarButtons is the toolbar's icon buttons in grouped order — the HBox
// children that are Buttons (the group dividers, which are not, are skipped), so
// the slice indices line up with the rtb* constants above.
func (w *wysiwyg) toolbarButtons() []*toolkit.Button {
	var bs []*toolkit.Button
	for _, c := range w.toolbar.Children() {
		if b, ok := c.(*toolkit.Button); ok {
			bs = append(bs, b)
		}
	}
	return bs
}

// RichToolbarVisible reports whether the formatting toolbar is currently shown
// (the WYSIWYG tab is active). Host / headless introspection.
func (s *State) RichToolbarVisible() bool { return s.wysiwyg().toolbarShown() }

// RichToolbarRect is the device rectangle of the formatting-toolbar strip (zero
// height while it is hidden), for the headless harness.
func (s *State) RichToolbarRect() [4]int {
	r := s.wysiwyg().toolbarRect
	return [4]int{r.X, r.Y, r.W, r.H}
}

// RichToolbarButtonCount is the number of formatting buttons (12: 4 inline, 6
// block, 2 list).
func (s *State) RichToolbarButtonCount() int { return len(s.wysiwyg().toolbarButtons()) }

// RichToolbarButtonRects is the device rectangle of every formatting button, in
// grouped order (see the rtb* constants), so a headless harness can dispatch a
// real pointer click at a button's centre.
func (s *State) RichToolbarButtonRects() [][4]int {
	bs := s.wysiwyg().toolbarButtons()
	out := make([][4]int, len(bs))
	for i, b := range bs {
		r := b.Bounds()
		out[i] = [4]int{r.X, r.Y, r.W, r.H}
	}
	return out
}

// RichToolbarButtonPressed reports whether button i shows its active (pressed)
// pill — the toolbar's reflection of the formatting in force at the caret /
// selection. An out-of-range index reports false.
func (s *State) RichToolbarButtonPressed(i int) bool {
	bs := s.wysiwyg().toolbarButtons()
	if i < 0 || i >= len(bs) {
		return false
	}
	return bs[i].Selected().Get()
}

// RichCurrentBlockKind is the caret block's kind as the toolkit BlockKind int
// (BlockParagraph, BlockH1.., BlockCodeKind, BlockQuoteKind), so a headless test
// can assert a block button changed the caret block.
func (s *State) RichCurrentBlockKind() int {
	return int(s.wysiwyg().editor.CurrentBlockKind())
}

// blockRuneLen is the number of editable caret cells in a text block (its
// flattened inline text length in runes); 0 for a block with no editable text.
func blockRuneLen(b richdoc.Block) int {
	switch n := b.(type) {
	case richdoc.Heading:
		return len([]rune(inlinesPlain(n.Inlines)))
	case richdoc.Paragraph:
		return len([]rune(inlinesPlain(n.Inlines)))
	case richdoc.CodeBlock:
		return len([]rune(n.Text))
	}
	return 0
}

// inlinesPlain is the plain text of a slice of inlines.
func inlinesPlain(inlines []richdoc.Inline) string {
	return richdoc.PlainText(&richdoc.Document{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: inlines}}})
}

// boldFinder is a richdoc.Visitor that trips found on the first Strong inline.
type boldFinder struct{ found bool }

func (v *boldFinder) Enter(node any) bool {
	if _, ok := node.(richdoc.Strong); ok {
		v.found = true
	}
	return !v.found
}

func (v *boldFinder) Leave(any) {}

// refCounter is a richdoc.Visitor that tallies the v0.2.0 reference inlines
// (Footnote / CrossRef / Anchor) across a document.
type refCounter struct{ foot, xref, anchor int }

func (v *refCounter) Enter(node any) bool {
	switch node.(type) {
	case richdoc.Footnote:
		v.foot++
	case richdoc.CrossRef:
		v.xref++
	case richdoc.Anchor:
		v.anchor++
	}
	return true
}

func (v *refCounter) Leave(any) {}
