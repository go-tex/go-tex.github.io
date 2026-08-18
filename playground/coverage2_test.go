// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/painter"
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
	if s.onDivider(pr.X+s.paned.Position, pr.Y-5) {
		t.Fatalf("a point above the paned should not be on the divider")
	}
	if s.onDivider(pr.X+5, pr.Y+5) {
		t.Fatalf("a point far from the handle should not be on the divider")
	}
	if !s.onDivider(pr.X+s.paned.Position+1, pr.Y+5) {
		t.Fatalf("a point on the handle should be on the divider")
	}
}

func TestHandleScrollRegionsAndOutside(t *testing.T) {
	s := newTestState(t, false)
	er := s.editor.Bounds()
	if !s.HandleScroll(er.X+10, er.Y+10, 3) {
		t.Fatalf("scroll over editor not consumed")
	}
	mm := s.minimap.Bounds()
	if !s.HandleScroll(mm.X+2, mm.Y+10, 3) {
		t.Fatalf("scroll over minimap not consumed")
	}
	if s.HandleScroll(-5, -5, 1) {
		t.Fatalf("scroll outside every pane should not be consumed")
	}
}

func TestHandleScrollLogPane(t *testing.T) {
	s := newTestState(t, false)
	s.toggleLog()
	rr := s.paned.Second.Bounds()
	if !s.HandleScroll(rr.X+10, rr.Y+10, 4) {
		t.Fatalf("scroll over log not consumed")
	}
	if s.logView.offset <= 0 {
		t.Fatalf("log did not scroll down: offset=%d", s.logView.offset)
	}
	// Scrolling up past the top clamps to 0.
	s.HandleScroll(rr.X+10, rr.Y+10, -100)
	if s.logView.offset != 0 {
		t.Fatalf("log offset did not clamp to 0: %d", s.logView.offset)
	}
}

func TestScrollRenderToLineSkippedWhenLog(t *testing.T) {
	s := newTestState(t, false)
	s.toggleLog()
	before := s.renderScroll.OffsetY
	s.scrollRenderToLine(20) // no-op while the Log is shown
	if s.renderScroll.OffsetY != before {
		t.Fatalf("scrollRenderToLine moved the render while Log was shown")
	}
}

func TestLogViewAlarmsAndDraw(t *testing.T) {
	lv := &logView{}
	lv.set(engine.Diagnostics{
		Runaway:       true,
		OpenGroups:    2,
		PageCapHit:    true,
		Skipped:       map[string]int{"foo": 3, "bar": 3, "baz": 1},
		UndefinedEnvs: map[string]int{"env": 1},
		MathDropped:   map[string]int{"\\zzz": 1},
	}, "boom")
	if got := lv.alarmCount(); got != 4+3+1+1 {
		t.Fatalf("alarmCount = %d, want 9", got)
	}
	var b strings.Builder
	for _, e := range lv.rows() {
		b.WriteString(e.text + "\n")
	}
	body := b.String()
	for _, want := range []string{"Compile error: boom", "Runaway", "group(s) still open", "page cap", "foo", "bar", "baz", "env", "zzz"} {
		if !strings.Contains(body, want) {
			t.Fatalf("log rows missing %q:\n%s", want, body)
		}
	}
	// Tie-break: equal counts sort alphabetically (bar before foo).
	if strings.Index(body, "3x  \\bar") > strings.Index(body, "3x  \\foo") {
		t.Fatalf("equal-count entries not alphabetised:\n%s", body)
	}
	// Draw on both a dark and a light ground (the alarm-red picker branches), and
	// with a scroll offset (clips top rows).
	buf := make([]byte, 400*300*4)
	lv.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 400, H: 300})
	lv.Draw(painter.NewPixelPainter(buf, 400, 300), toolkit.DefaultDark())
	lv.Draw(painter.NewPixelPainter(buf, 400, 300), toolkit.DefaultLight())
	lv.offset = 40
	lv.Draw(painter.NewPixelPainter(buf, 400, 300), toolkit.DefaultDark())
	// Zero-size draw is a no-op (no panic).
	lv.SetBounds(toolkit.Rect{})
	lv.Draw(painter.NewPixelPainter(buf, 400, 300), toolkit.DefaultDark())
}

func TestMinimapLineAtYEdges(t *testing.T) {
	m := &minimap{}
	m.SetBounds(toolkit.Rect{X: 0, Y: 100, W: 80, H: 200})
	// No lines yet -> 0.
	if got := m.lineAtY(150); got != 0 {
		t.Fatalf("lineAtY with no lines = %d, want 0", got)
	}
	m.update([]string{"a", "b", "c", "d"}, 0, 2)
	if got := m.lineAtY(50); got != 0 { // above the top clamps to 0
		t.Fatalf("lineAtY above top = %d, want 0", got)
	}
	if got := m.lineAtY(10000); got != 3 { // below the bottom clamps to n-1
		t.Fatalf("lineAtY below bottom = %d, want 3", got)
	}
}

func TestMinimapDrawBranches(t *testing.T) {
	m := &minimap{}
	buf := make([]byte, 100*400*4)
	p := painter.NewPixelPainter(buf, 100, 400)
	th := toolkit.DefaultLight()

	// Zero-size: no-op.
	m.Draw(p, th)

	// Empty line list: paints the ground but no bars.
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 80, H: 400})
	m.update(nil, 0, 0)
	m.Draw(p, th)

	// Real content incl a blank line + a long line; viewport indicator visible.
	m.update([]string{"", "short", strings.Repeat("x", 200), "mid"}, 1, 2)
	m.Draw(p, th)

	// A viewport taller than the widget clamps the indicator height.
	m.update([]string{"a", "b"}, 0, 100)
	m.Draw(p, th)

	// A 1-char line beside a very long one rounds its bar width below 1 (clamped);
	// a comment line takes the dimmed colour.
	m.update([]string{"x", "% a comment", strings.Repeat("y", 400)}, 0, 1)
	m.Draw(p, th)

	// More lines than pixels of height (rowH/barH clamp to 1) with a tiny visible
	// window so the viewport indicator height clamps up to its minimum.
	many := make([]string, 600)
	for i := range many {
		many[i] = "z"
	}
	m.update(many, 0, 1)
	m.Draw(p, th)

	// A near-zero width clamps usableW to 1.
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 4, H: 100})
	m.update([]string{"a", "bb", "ccc"}, 0, 1)
	m.Draw(p, th)
}

func TestIntrospectionAccessors(t *testing.T) {
	s := newTestState(t, false)
	if s.EditorWidth() != s.editor.Bounds().W {
		t.Fatalf("EditorWidth mismatch")
	}
	s.renderScroll.Scroll(0, 40)
	if s.RenderOffsetY() != s.renderScroll.OffsetY {
		t.Fatalf("RenderOffsetY mismatch")
	}
	if s.ShowLog() {
		t.Fatalf("ShowLog should be false by default")
	}
	s.toggleLog()
	if !s.ShowLog() {
		t.Fatalf("ShowLog should be true after toggle")
	}
	if s.DividerX() != s.paned.Bounds().X+s.paned.Position {
		t.Fatalf("DividerX mismatch")
	}
}

func TestVisibleEditorLinesFloor(t *testing.T) {
	s := newTestState(t, false)
	s.editor.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 100, H: 1})
	if got := s.visibleEditorLines(); got != 1 {
		t.Fatalf("visibleEditorLines floor = %d, want 1", got)
	}
}
