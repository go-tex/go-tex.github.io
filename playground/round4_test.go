// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// --- #4 clipboard -----------------------------------------------------------

func TestClipboardCopyCutPasteSelectAll(t *testing.T) {
	s := newTestState(t, false)
	var wrote string
	s.SetClipboardWriter(func(x string) { wrote = x })

	// Select all + copy: the buffer text reaches both the toolkit clipboard and
	// the host writer hook.
	s.editor.SelectAll()
	if !s.HandleCopy() {
		t.Fatalf("HandleCopy not consumed while focused")
	}
	full := s.editor.Text()
	if s.clip.ClipboardText() != full {
		t.Fatalf("clipboard text = %q, want the whole buffer", s.clip.ClipboardText())
	}
	if wrote != full {
		t.Fatalf("host writer got %q, want the copied text", wrote)
	}
	if toolkit.ClipboardText() != full {
		t.Fatalf("toolkit-wide clipboard not routed through the app clipboard")
	}

	// Paste inserts at the caret (buffer changes).
	before := s.editor.Text()
	if !s.HandlePaste("PASTED") {
		t.Fatalf("HandlePaste not consumed")
	}
	if s.editor.Text() == before || !strings.Contains(s.editor.Text(), "PASTED") {
		t.Fatalf("paste did not insert text")
	}

	// Select-all then cut empties the buffer and the cut text is on the clipboard.
	if !s.HandleSelectAll() {
		t.Fatalf("HandleSelectAll not consumed")
	}
	if !s.editor.HasSelection() {
		t.Fatalf("SelectAll left no selection")
	}
	if !s.HandleCut() {
		t.Fatalf("HandleCut not consumed")
	}
	if strings.TrimSpace(s.editor.Text()) != "" {
		t.Fatalf("cut-all left text: %q", s.editor.Text())
	}

	// Unfocused: every clipboard op is a no-op.
	s.editor.Focused = false
	if s.HandleCopy() || s.HandleCut() || s.HandlePaste("x") || s.HandleSelectAll() {
		t.Fatalf("clipboard ops should be no-ops when the editor is unfocused")
	}
}

func TestAppClipboardNilWriter(t *testing.T) {
	c := &appClipboard{} // no onWrite hook
	c.SetClipboardText("hello")
	if c.ClipboardText() != "hello" {
		t.Fatalf("appClipboard did not store text without a writer")
	}
}

// --- #5 continuous / paginated render ---------------------------------------

// multiPageDoc is a document long enough to paginate into several sheets.
func multiPageDoc() string {
	var b strings.Builder
	b.WriteString(`\documentclass{article}\begin{document}`)
	for i := 0; i < 6; i++ {
		b.WriteString(`\section{Section}`)
		for j := 0; j < 40; j++ {
			b.WriteString("Paragraph text filling the page. ")
		}
		b.WriteString(`\newpage`)
	}
	b.WriteString(`\end{document}`)
	return b.String()
}

func TestCompileLaTeXStacksAllPages(t *testing.T) {
	single := compileLaTeX(SampleLaTeX, toolkit.DefaultLight())
	multi := compileLaTeX(multiPageDoc(), toolkit.DefaultLight())
	if multi.drawnPages < 2 {
		t.Fatalf("multi-page doc rasterized %d pages, want >=2", multi.drawnPages)
	}
	if multi.pages != multi.drawnPages {
		t.Fatalf("pages(%d) != drawnPages(%d) for a clean multi-page compile", multi.pages, multi.drawnPages)
	}
	// One bitmap per drawn page; the multi-page doc yields more than the single.
	if len(multi.bitmaps) != multi.drawnPages {
		t.Fatalf("bitmaps(%d) != drawnPages(%d)", len(multi.bitmaps), multi.drawnPages)
	}
	if len(multi.bitmaps) <= len(single.bitmaps) {
		t.Fatalf("multi-page bitmaps %d not > single-page %d", len(multi.bitmaps), len(single.bitmaps))
	}
}

func TestPaginationIntrospection(t *testing.T) {
	s := newTestState(t, false)
	if s.PageCount() != 1 || s.DrawnPages() != 1 {
		t.Fatalf("sample doc should be 1 page: pages=%d drawn=%d", s.PageCount(), s.DrawnPages())
	}
	if s.renderView.PageCount() != 1 {
		t.Fatalf("render pane should hold 1 page, got %d", s.renderView.PageCount())
	}

	s.SetSource(multiPageDoc())
	if s.PageCount() < 2 || s.DrawnPages() < 2 {
		t.Fatalf("multi-page: pages=%d drawn=%d, want >=2", s.PageCount(), s.DrawnPages())
	}
	if s.renderView.PageCount() != s.DrawnPages() {
		t.Fatalf("render pane page count %d != drawn %d", s.renderView.PageCount(), s.DrawnPages())
	}
}
