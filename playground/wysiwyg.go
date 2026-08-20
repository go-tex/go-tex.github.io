// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

// This file adds the playground's WYSIWYG multi-format mode. It is deliberately
// self-contained: the whole feature (format registry, a "Source | WYSIWYG" tab
// strip + a format picker pinned to the top of the editor pane, a toolkit
// RichEditor shown in place of the CodeEditor on the WYSIWYG tab, and the
// source<->document round-trip through the go-richdoc converters) lives here, so
// app.go and the wasm driver only carry a handful of one-line additive hooks
// (s.wysiwyg*(...)).
//
// The strip is the shared [toolkit.FolderTabs] — the SAME widget the render pane
// uses for its Rendered│Log tabs — placed at the top of the editor (left) pane so
// the two panes read consistently. The active editor tab is the single source of
// truth (an [mvvm.Observable] on the strip, mirroring the render pane's tab
// state): there is no shadow "active" bool. Selecting WYSIWYG parses the source
// into the RichEditor; selecting Source writes the edited document back.
//
// A Format is bound to a Codec (Parse/Write over the neutral richdoc model) in a
// map, so adding a new format later — go-odf/go-rtf were wired here from the
// start, a future converter is a single RegisterFormat call — never touches the
// toggle, layout, draw or event code below.

import (
	"strings"

	"github.com/go-richdoc/richdoc"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	odf "github.com/go-odf/odf"
	latex "github.com/go-richdoc/latex"
	markdown "github.com/go-richdoc/markdown"
	rtf "github.com/go-rtf/rtf"
)

// Format identifies one document format the WYSIWYG mode can round-trip through.
// The values are dense so the format picker can index the ordered registry by
// selection, but nothing depends on a particular numeric value — a new format is
// registered with the next free value.
type Format int

// The built-in formats. LaTeX is the default because that is what the source
// editor already holds and what the go-tex compile -> render pipeline consumes.
const (
	FormatLaTeX Format = iota
	FormatMarkdown
	FormatODT
	FormatRTF
)

// Codec is the Parse/Write pair for one [Format] over the neutral richdoc model,
// plus whether its serialisation is human-editable plain text.
//
// Textual formats (LaTeX, Markdown) round-trip through the visible source
// editor: entering WYSIWYG parses the editor buffer, leaving writes the edited
// document back into it (which drives the existing compile). Non-textual formats
// (ODT is a zip container, RTF a control-word stream) are import/export only:
// the document is brought in with [State.WysiwygImport] (open a file -> Parse)
// and taken out with [State.WysiwygExport] (Write -> download), never pushed
// through the plain-text source editor.
type Codec struct {
	Name    string
	Parse   func([]byte) (*richdoc.Document, error)
	Write   func(*richdoc.Document) ([]byte, error)
	Textual bool
}

// formatRegistry maps every registered [Format] to its [Codec]; formatOrder
// keeps registration order so the picker's option index maps back to a Format.
// A new format (go-odf/go-rtf were added below at launch) is a single
// RegisterFormat call — the extension point the whole feature pivots on.
var (
	formatRegistry = map[Format]Codec{}
	formatOrder    []Format
)

// RegisterFormat binds f to codec, appending f to the picker order the first
// time it is seen (a re-registration overwrites the codec but keeps the order).
// It is the sole extension point: dropping in another go-richdoc converter is
// one call here.
func RegisterFormat(f Format, codec Codec) {
	if _, seen := formatRegistry[f]; !seen {
		formatOrder = append(formatOrder, f)
	}
	formatRegistry[f] = codec
}

func init() {
	RegisterFormat(FormatLaTeX, Codec{Name: "LaTeX", Parse: latex.Parse, Write: latex.Write, Textual: true})
	RegisterFormat(FormatMarkdown, Codec{Name: "Markdown", Parse: markdown.Parse, Write: markdown.Write, Textual: true})
	RegisterFormat(FormatODT, Codec{Name: "ODT", Parse: odf.Parse, Write: odf.Write, Textual: false})
	RegisterFormat(FormatRTF, Codec{Name: "RTF", Parse: rtf.Parse, Write: rtf.Write, Textual: false})
}

// formatNames is the ordered list of registered format names, for the picker.
func formatNames() []string {
	names := make([]string, len(formatOrder))
	for i, f := range formatOrder {
		names[i] = formatRegistry[f].Name
	}
	return names
}

// Editor-pane tab indices. Source shows the CodeEditor (LaTeX source, completion,
// minimap, compile-on-edit); WYSIWYG shows the RichEditor. The strip's reactive
// selection IS the mode's active state — see [wysiwyg.active].
const (
	tabSource  = 0
	tabWysiwyg = 1
)

// wysiwyg is the mode's whole state: the editor-pane tab strip and the format
// DropDown (both pinned to the top of the editor pane), the RichEditor shown on
// the WYSIWYG tab, the selected format, a parse-error string (shown while it
// bounces back to Source), a pointer-capture flag for a drag inside the
// RichEditor, the strip rect for click routing and a re-entrancy guard raised
// while a programmatic tab change (a parse-error bounce or an import) must not
// re-run the enter/leave side effects.
type wysiwyg struct {
	s *State

	editor  *toolkit.RichEditor
	toolbar *toolkit.RichEditorToolbar
	tabs    *toolkit.FolderTabs
	picker  *toolkit.DropDown

	format   Format
	parseErr string
	pressing bool

	strip       toolkit.Rect
	toolbarRect toolkit.Rect
	syncingTab  bool
}

// newWysiwyg builds the mode's widgets over State s. The RichEditor starts empty
// (it is fed a parsed document when the WYSIWYG tab is first selected); the tab
// strip flips the mode and the picker selects the active format.
func newWysiwyg(s *State) *wysiwyg {
	// formatOrder is always non-empty (init registers the four built-ins before
	// any State is built), so the default format is simply the first registered.
	w := &wysiwyg{s: s, editor: toolkit.NewRichEditor(nil), format: formatOrder[0]}
	// The formatting toolbar is bound to the one RichEditor for the whole app's
	// life: it drives the editor's verbs and lights the buttons whose formatting is
	// in force at the caret (it subscribes to the editor's Caret/Selection/Doc). It
	// is shown only on the WYSIWYG tab, laid out directly under the tab strip.
	w.toolbar = toolkit.NewRichEditorToolbar(w.editor)
	w.tabs = toolkit.NewFolderTabs([]string{"Source", "WYSIWYG"}, tabSource)
	w.picker = toolkit.NewDropDown(formatNames(), 0)
	// The picker selects the session format; the change takes effect on the next
	// enter (and, for a textual format, on the write-back at leave), so switching
	// while on the Source tab simply re-aims what the WYSIWYG tab will parse. The
	// widget lives for the whole app, so the unsubscribe handle is dropped.
	w.picker.Selected().Subscribe(func(idx int) {
		if idx >= 0 && idx < len(formatOrder) {
			w.format = formatOrder[idx]
		}
	})
	// The active editor tab is the single source of truth (mirrors the render
	// pane's Rendered│Log strip): selecting WYSIWYG parses the source into the
	// RichEditor, selecting Source writes the edited document back. A parse
	// failure bounces the strip back to Source under the syncingTab guard, so the
	// guarded Set does not re-enter leave(). The strip lives for the whole app, so
	// the unsubscribe handle is dropped.
	w.tabs.Selected().Subscribe(func(idx int) {
		if w.syncingTab {
			return
		}
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

// codec is the active format's Codec.
func (w *wysiwyg) codec() Codec { return formatRegistry[w.format] }

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

// enter is run when the WYSIWYG tab becomes active: it parses the current source
// with the selected format into the RichEditor. A parse failure records the error
// and bounces the strip back to Source (the documented graceful path — malformed
// LaTeX, or a plain-text buffer handed to the ODT reader, which wants a zip), so
// the strip's selection and the visible editor never disagree.
func (w *wysiwyg) enter() {
	doc, err := w.codec().Parse([]byte(w.s.Source()))
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

// leave is run when the Source tab becomes active. For a textual format the
// edited document is written back into the source editor (driving the existing
// compile -> render pipeline); a non-textual format is import/export only, so the
// source editor is left as it is (the document leaves through WysiwygExport
// instead).
func (w *wysiwyg) leave() {
	w.editor.Focused().Set(false)
	if w.codec().Textual {
		if out, err := w.codec().Write(w.editor.Document()); err != nil {
			w.parseErr = err.Error()
		} else {
			w.parseErr = ""
			w.s.SetSource(string(out))
		}
	}
	w.s.dirty = true
}

// bounceToSource forces the strip back to the Source tab without re-running the
// leave() write-back — the guarded revert used when an enter() parse fails.
func (w *wysiwyg) bounceToSource() {
	w.syncingTab = true
	w.tabs.Selected().Set(tabSource)
	w.syncingTab = false
}

// WysiwygImport switches to format f, parses data into the RichEditor and enters
// WYSIWYG — the open-a-file path for a non-textual format (an .odt package, an
// .rtf stream). A parse error is returned and the mode stays inactive.
func (s *State) WysiwygImport(f Format, data []byte) error {
	w := s.wysiwyg()
	codec, ok := formatRegistry[f]
	if !ok {
		return errUnknownFormat
	}
	doc, err := codec.Parse(data)
	if err != nil {
		w.parseErr = err.Error()
		return err
	}
	w.format = f
	w.parseErr = ""
	w.editor.SetDocument(doc)
	w.editor.Focused().Set(true)
	// Select the WYSIWYG tab WITHOUT re-running enter() (which would re-parse the
	// plain-text source buffer, not this imported document): the RichEditor is
	// already populated from data above, so the tab change is purely a view swap.
	w.syncingTab = true
	w.tabs.Selected().Set(tabWysiwyg)
	w.syncingTab = false
	// Now that the WYSIWYG tab is active, reserve the toolbar strip above the
	// RichEditor (toolbarShown reads the active tab).
	w.applyWysiwygBounds()
	s.dirty = true
	return nil
}

// WysiwygExport serialises the current RichEditor document with format f's
// writer — the download path for a non-textual (or any) format.
func (s *State) WysiwygExport(f Format) ([]byte, error) {
	codec, ok := formatRegistry[f]
	if !ok {
		return nil, errUnknownFormat
	}
	return codec.Write(s.wysiwyg().editor.Document())
}

// errUnknownFormat is returned by import/export for an unregistered Format.
var errUnknownFormat = wysiwygError("unknown format")

// wysiwygError is a tiny string error so the file needs no errors import.
type wysiwygError string

func (e wysiwygError) Error() string { return string(e) }

// --- layout ---------------------------------------------------------------

// stripHeight is the device height reserved at the top of the editor pane for the
// tab strip — the shared FolderTabs height, so the editor strip lines up exactly
// with the render pane's Rendered│Log strip. applyLeftSplit reserves it above the
// CodeEditor + minimap.
func (w *wysiwyg) stripHeight() int { return toolkit.FolderTabsHeight() }

// toolbarShown reports whether the formatting toolbar is visible. It shows ONLY
// while the WYSIWYG tab is active — on either a textual (LaTeX/Markdown) or a
// binary (ODT/RTF) format, since for the binary formats the RichEditor IS the
// primary editing surface. On the Source tab (the CodeEditor, or the binary-note
// placeholder) it is hidden.
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
// drag, a draw, enter and import — so the reserved strip and the click routing
// never disagree.
func (w *wysiwyg) applyWysiwygBounds() {
	tb, ed := w.layoutBounds()
	w.toolbarRect = tb
	w.toolbar.SetBounds(tb)
	w.editor.SetBounds(ed)
}

// wysiwygLayout pins the Source│WYSIWYG strip to the top of the editor (left)
// pane and floats the format DropDown at the strip's right edge; while the
// WYSIWYG tab is active it sizes the RichEditor to the editor region (below the
// strip). Called at the tail of State.layout (and after a divider drag), so the
// strip tracks the left pane's current width.
func (s *State) wysiwygLayout() {
	w := s.wysiwyg()
	pr := s.paned.Bounds()
	leftW := s.paned.Position().Get() // Paned clamps the divider to [10, W-10], never < 0
	h := w.stripHeight()
	w.strip = toolkit.Rect{X: pr.X, Y: pr.Y, W: leftW, H: h}
	// The FolderTabs spans the whole strip width (its background + bottom border
	// read as the pane's top edge, matching the render pane); it hit-tests only
	// the label-width tab rects, so the DropDown floated over its right end never
	// clashes with a tab click.
	w.tabs.SetBounds(w.strip)
	gap := toolkit.Scaled(8)
	pw := toolkit.Scaled(110)
	if max := leftW - 2*gap; pw > max {
		pw = max // shrink the picker to fit a narrow pane
	}
	if pw < 0 {
		pw = 0 // a pane too narrow for even the gaps drops the picker to zero width
	}
	ph := toolkit.Scaled(20)
	w.picker.SetBounds(toolkit.Rect{X: pr.X + leftW - gap - pw, Y: pr.Y + (h-ph)/2, W: pw, H: ph})
	if w.active() {
		// Reserve the formatting-toolbar strip above the RichEditor and size the
		// editor to the region below it (tracks the left pane's current width on a
		// divider drag / resize, exactly like the tab strip above).
		w.applyWysiwygBounds()
	}
}

// --- draw -----------------------------------------------------------------

// wysiwygDraw paints the editor-pane tab strip, then the active tab's editor
// surface: on the WYSIWYG tab the RichEditor over the editor region; on the
// Source tab for a NON-textual (binary) format a clear note instead of the
// CodeEditor, since ODT/RTF have no human-editable source (the CodeEditor,
// already painted by the Paned, is covered by the note). A pending parse error is
// shown as a band; the format DropDown draws over the strip's right end and its
// popover floats on top. Called from State.Draw.
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
	} else if !w.codec().Textual {
		r := s.editor.Bounds()
		p.FillRect(r, s.theme.SurfaceAlt)
		// Clear the parse-error band (drawn next, 22px tall) when one is present, so
		// the note is not overwritten by it.
		noteY := r.Y + toolkit.Scaled(12)
		if w.parseErr != "" {
			noteY = r.Y + toolkit.Scaled(30)
		}
		toolkit.DrawText(p, r.X+toolkit.Scaled(12), noteY,
			"Binary format ("+w.codec().Name+") — edit on the WYSIWYG tab; use import / export to load and save.",
			s.theme.OnSurface)
	}
	if w.parseErr != "" {
		r := s.editor.Bounds()
		band := toolkit.Rect{X: r.X, Y: r.Y, W: r.W, H: toolkit.Scaled(22)}
		p.FillRect(band, s.theme.SurfaceAlt)
		toolkit.DrawText(p, r.X+toolkit.Scaled(8), r.Y+toolkit.Scaled(6), "Parse error: "+w.parseErr, s.theme.Accent)
	}
	w.picker.Draw(p, s.theme)
	if w.picker.Open().Get() {
		w.picker.DrawPopover(p, s.theme)
	}
}

// --- input ----------------------------------------------------------------

// wysiwygClick routes a pointer press: an open format popover, the editor-pane
// tab strip (the format DropDown overlaid at its right, else the Source│WYSIWYG
// tabs), or (while the WYSIWYG tab is active) the RichEditor. It returns true when
// it consumed the press so State.HandleClick stops. Called before the generic
// toolbar/editor routing.
func (s *State) wysiwygClick(x, y int) bool {
	w := s.wysiwyg()
	if w.picker.Open().Get() {
		w.picker.PopoverClick(x, y)
		s.dirty = true
		return true
	}
	if w.strip.Contains(x, y) {
		// The DropDown is floated over the strip's right end; test it first, then
		// fall through to the FolderTabs (which hit-tests its own label-width tabs
		// and is a no-op on the empty strip between the tabs and the picker).
		if pr := w.picker.Bounds(); pr.Contains(x, y) {
			w.picker.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - pr.X, Y: y - pr.Y})
			s.dirty = true
			return true
		}
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

// FormatPickerRect is the device rectangle of the format DropDown, for the
// headless harness.
func (s *State) FormatPickerRect() [4]int {
	r := s.wysiwyg().picker.Bounds()
	return [4]int{r.X, r.Y, r.W, r.H}
}

// SetWysiwygFormat selects the session format by picker index, driving the same
// path as a click on the picker.
func (s *State) SetWysiwygFormat(idx int) { s.wysiwyg().picker.Select(idx) }

// WysiwygActive reports whether the RichEditor (the WYSIWYG tab) is currently
// shown.
func (s *State) WysiwygActive() bool { return s.wysiwyg().active() }

// WysiwygFormat is the selected format's display name.
func (s *State) WysiwygFormat() string { return s.wysiwyg().codec().Name }

// WysiwygParseError is the last parse/write error message ("" when none).
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

// RichHasBold reports whether any Strong (bold) inline exists anywhere in the
// document, so a headless test can assert a parsed \textbf survived.
func (s *State) RichHasBold() bool {
	v := &boldFinder{}
	richdoc.Walk(s.wysiwyg().editor.Document(), v)
	return v.found
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
