// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

func TestHandleMoveReleaseNoCapture(t *testing.T) {
	s := newTestState(t, false)
	if s.HandleMove(5, 5) {
		t.Fatalf("HandleMove with no capture should be a no-op")
	}
	if s.HandleRelease(5, 5) {
		t.Fatalf("HandleRelease with no capture should be a no-op")
	}
}

func TestEditorPressDragSelects(t *testing.T) {
	s := newTestState(t, false)
	er := s.editor.Bounds()
	if !s.HandleClick(er.X+20, er.Y+40) {
		t.Fatalf("editor press not consumed")
	}
	if s.pressKind != pressEditor {
		t.Fatalf("press did not capture the editor")
	}
	if !s.HandleMove(er.X+120, er.Y+40) {
		t.Fatalf("editor drag not consumed")
	}
	if !s.HandleRelease(er.X+120, er.Y+40) {
		t.Fatalf("editor release not consumed")
	}
}

func TestHandleClickToolbarMiss(t *testing.T) {
	s := newTestState(t, false)
	// A press in the toolbar row but not on any control is not consumed.
	if s.HandleClick(s.w-2, 2) {
		t.Fatalf("empty toolbar press should not be consumed")
	}
	// A press below the status bar (nowhere) is not consumed.
	if s.HandleClick(s.w/2, s.h+50) {
		t.Fatalf("out-of-scene press should not be consumed")
	}
}

func TestOnDividerBounds(t *testing.T) {
	s := newTestState(t, false)
	pr := s.paned.Bounds()
	if s.onDivider(pr.X+s.paned.Position().Get(), pr.Y-5) {
		t.Fatalf("a point above the paned should not be on the divider")
	}
	if s.onDivider(pr.X+5, pr.Y+5) {
		t.Fatalf("a point far from the handle should not be on the divider")
	}
	if !s.onDivider(pr.X+s.paned.Position().Get()+1, pr.Y+5) {
		t.Fatalf("a point on the handle should be on the divider")
	}
}

func TestHandleScrollRegionsAndOutside(t *testing.T) {
	s := newTestState(t, false)
	er := s.editor.Bounds()
	if !s.HandleScroll(er.X+10, er.Y+10, 0, 3) {
		t.Fatalf("scroll over editor not consumed")
	}
	mm := s.minimap.Bounds()
	if !s.HandleScroll(mm.X+2, mm.Y+10, 0, 3) {
		t.Fatalf("scroll over minimap not consumed")
	}
	if s.HandleScroll(-5, -5, 0, 1) {
		t.Fatalf("scroll outside every pane should not be consumed")
	}
}

// TestHandleScrollLogPane drives a wheel over the Log tab through the full State
// router: the toolkit LogView owns its own scroll offset (no host-side offset
// state), so we assert only that the scroll is consumed on both wheel directions.
func TestHandleScrollLogPane(t *testing.T) {
	s := newTestState(t, false)
	s.toggleLog()
	rr := s.rightPane.contentRect()
	if !s.HandleScroll(rr.X+10, rr.Y+10, 0, 4) {
		t.Fatalf("scroll over log not consumed")
	}
	if !s.HandleScroll(rr.X+10, rr.Y+10, 0, -100) {
		t.Fatalf("upward scroll over log not consumed")
	}
}

func TestIntrospectionAccessors(t *testing.T) {
	s := newTestState(t, false)
	if s.EditorWidth() != s.editor.Bounds().W {
		t.Fatalf("EditorWidth mismatch")
	}
	// Zoom / mode / current-page read straight from the PagedView observables.
	s.renderView.Zoom().Set(150)
	if s.ZoomPercent() != 150 {
		t.Fatalf("ZoomPercent = %d, want 150", s.ZoomPercent())
	}
	s.renderView.Mode().Set(toolkit.PagedPaginated)
	if s.RenderMode() != int(toolkit.PagedPaginated) {
		t.Fatalf("RenderMode = %d, want paginated", s.RenderMode())
	}
	s.renderView.CurrentPage().Set(1)
	if s.RenderCurrentPage() != 1 {
		t.Fatalf("RenderCurrentPage = %d, want 1", s.RenderCurrentPage())
	}
	if s.ShowLog() {
		t.Fatalf("ShowLog should be false by default")
	}
	s.toggleLog()
	if !s.ShowLog() {
		t.Fatalf("ShowLog should be true after toggle")
	}
	if s.DividerX() != s.paned.Bounds().X+s.paned.Position().Get() {
		t.Fatalf("DividerX mismatch")
	}
}

func TestHandleCharViewerFocusAndContentRectClamp(t *testing.T) {
	s := newTestState(t, false)
	s.renderView.SetPages(testBitmaps(6)) // short pages so key-nav flips cleanly
	s.renderView.Mode().Set(toolkit.PagedPaginated)
	s.editor.Focused().Set(false)
	s.renderView.SetFocused(true)

	// The space bar pages the viewer when it holds keyboard focus.
	if !s.HandleChar(" ") {
		t.Fatalf("space should page the focused viewer")
	}
	if s.RenderCurrentPage() != 2 {
		t.Fatalf("space did not advance the page: %d", s.RenderCurrentPage())
	}
	// A non-space character is swallowed (never reaches the unfocused editor) and
	// leaves the page unchanged.
	page := s.RenderCurrentPage()
	if s.HandleChar("Z") {
		t.Fatalf("a non-space char should not be consumed while the viewer is focused")
	}
	if s.RenderCurrentPage() != page {
		t.Fatalf("a swallowed char changed the page")
	}

	// renderContentRect clamps its toolbar band to a pane shorter than the strip.
	s.renderView.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 100, H: 10})
	if cr := s.renderContentRect(); cr.H != 0 {
		t.Fatalf("renderContentRect tiny-pane clamp H = %d, want 0", cr.H)
	}
}

func TestVisibleEditorLinesFloor(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 100, H: 1})
	if got := s.visibleEditorLines(); got != 1 {
		t.Fatalf("visibleEditorLines floor = %d, want 1", got)
	}
}
