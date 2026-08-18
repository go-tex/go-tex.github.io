// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"testing"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

// withCompileFn swaps the engine seam for the duration of fn.
func withCompileFn(stub func([]byte, engine.Options) ([]string, engine.Diagnostics, error), fn func()) {
	prev := compileFn
	compileFn = stub
	defer func() { compileFn = prev }()
	fn()
}

func TestCompileLaTeXHardErrorBranch(t *testing.T) {
	withCompileFn(
		func([]byte, engine.Options) ([]string, engine.Diagnostics, error) {
			return nil, engine.Diagnostics{}, errors.New("boom")
		},
		func() {
			res := compileLaTeX("anything", toolkit.DefaultLight())
			if res.errText != "boom" {
				t.Fatalf("errText = %q, want boom", res.errText)
			}
			if res.bitmaps != nil {
				t.Fatalf("hard error should yield no bitmaps")
			}
		},
	)
}

func TestCompileLaTeXUnrasterizablePages(t *testing.T) {
	// Pages that svgraster cannot parse are skipped; with none left, the result
	// reports zero drawable pages without an image.
	withCompileFn(
		func([]byte, engine.Options) ([]string, engine.Diagnostics, error) {
			return []string{"not an svg", "also not svg"}, engine.Diagnostics{}, nil
		},
		func() {
			res := compileLaTeX("anything", toolkit.DefaultLight())
			if res.bitmaps != nil {
				t.Fatalf("unrasterizable pages should yield no bitmaps")
			}
			if res.pages != 2 {
				t.Fatalf("pages = %d, want 2 (the engine's page count is kept)", res.pages)
			}
		},
	)
}

func TestCompileEmptyDocumentBranch(t *testing.T) {
	s := newTestState(t, false)
	// An empty document compiles to zero drawable pages: Compile's pixels==nil
	// branch clears the render image.
	s.editor.SetText(`\documentclass{article}\begin{document}\end{document}`)
	s.Compile()
	if s.renderView.PageCount() != 0 {
		t.Fatalf("empty document did not clear the render pane (PageCount=%d)", s.renderView.PageCount())
	}
	if s.pages != 0 {
		t.Fatalf("empty document pages = %d, want 0", s.pages)
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf) // must not panic with an empty render image
}

func TestDrawErrorOverlay(t *testing.T) {
	s := newTestState(t, false)
	s.errText = "synthetic error"
	s.updateStatus()
	if s.status.Segments[2] != "compile error" {
		t.Fatalf("status segment 2 = %q, want compile error", s.status.Segments[2])
	}
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	// The error band paints Accent ink somewhere in the render content's top rows
	// (below the PagedView toolbar strip).
	rr := s.renderContentRect()
	found := false
	acc := s.theme.Accent
	for y := rr.Y; y < rr.Y+22 && !found; y++ {
		for x := rr.X; x < rr.X+rr.W; x++ {
			i := (y*testW + x) * 4
			if buf[i] == acc.R && buf[i+1] == acc.G && buf[i+2] == acc.B {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("drawError did not paint the accent-coloured message")
	}
}

func TestHandleClickOutsidePanes(t *testing.T) {
	s := newTestState(t, false)
	if s.HandleClick(-100, -100) {
		t.Fatalf("a click outside both panes should not be consumed")
	}
}

func TestLayoutClampsNegativeBody(t *testing.T) {
	s := newTestState(t, false)
	// A surface shorter than the toolbar + status bar clamps the body height to 0.
	s.Resize(200, 8)
	if s.paned.Bounds().H != 0 {
		t.Fatalf("body height = %d, want 0 (clamped)", s.paned.Bounds().H)
	}
}

func TestUpdateStatusSingularPage(t *testing.T) {
	s := newTestState(t, false)
	s.errText = ""
	s.pages = 1
	s.updateStatus()
	if s.status.Segments[2] != "1 page" {
		t.Fatalf("segment 2 = %q, want '1 page'", s.status.Segments[2])
	}
	s.pages = 3
	s.updateStatus()
	if s.status.Segments[2] != "3 pages" {
		t.Fatalf("segment 2 = %q, want '3 pages'", s.status.Segments[2])
	}
}
