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

func TestHandleScrollLogPane(t *testing.T) {
	s := newTestState(t, false)
	s.toggleLog()
	rr := s.rightPane.contentRect()
	if !s.HandleScroll(rr.X+10, rr.Y+10, 0, 4) {
		t.Fatalf("scroll over log not consumed")
	}
	if s.logView.offset <= 0 {
		t.Fatalf("log did not scroll down: offset=%d", s.logView.offset)
	}
	// Scrolling up past the top clamps to 0.
	s.HandleScroll(rr.X+10, rr.Y+10, 0, -100)
	if s.logView.offset != 0 {
		t.Fatalf("log offset did not clamp to 0: %d", s.logView.offset)
	}
}

func TestScrollRenderToLineSkippedWhenLog(t *testing.T) {
	s := newTestState(t, false)
	s.toggleLog()
	before := s.rscroll().OffsetY
	s.scrollRenderToLine(20) // no-op while the Log is shown
	if s.rscroll().OffsetY != before {
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
	m.update([]string{"a", "b", "c", "d"}, nil, 0, 2)
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

	// Zero-size: no-op (also exercises segmentCount's empty guard).
	m.Draw(p, th)
	if m.segmentCount(th.OnSurface) != 0 {
		t.Fatalf("segmentCount on a zero-size minimap should be 0")
	}

	// Empty line list: paints the ground but no segments.
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 80, H: 400})
	m.update(nil, nil, 0, 0)
	m.Draw(p, th)
	if m.segmentCount(th.OnSurface) != 0 {
		t.Fatalf("segmentCount with no lines should be 0")
	}

	// Real content: an indented line (leading spaces -> gap), a comment, a
	// multi-token line whose per-rune spans force a colour-run split, and a
	// blank line. The viewport indicator is visible.
	lines := []string{
		"    indented word", // leading whitespace, then two space-separated runs
		"%comment",          // one dimmed run (no internal spaces)
		"ab",                // two adjacent runes of DIFFERENT colours -> a run split
		"",                  // blank line -> no segments
	}
	red := toolkit.RGB(0xFF, 0, 0)
	blue := toolkit.RGB(0, 0, 0xFF)
	spans := [][]toolkit.TextSpan{
		nil, // line 0 falls back to the neutral ink
		{{Start: 0, End: 8, Color: toolkit.RGB(0x80, 0x80, 0x80)}},
		{{Start: 0, End: 1, Color: red}, {Start: 1, End: 2, Color: blue}}, // 'a' red, 'b' blue
		nil,
	}
	m.update(lines, spans, 1, 2)
	m.Draw(p, th)
	// Segments: line0 has 2 runs, line1 has 1 run, line2 splits into 2 runs,
	// line3 (blank) has 0 -> 5 in total.
	if got := m.segmentCount(th.OnSurface); got != 5 {
		t.Fatalf("segmentCount = %d, want 5 (2+1+2+0)", got)
	}

	// A viewport taller than the widget clamps the indicator height.
	m.update([]string{"a", "b"}, nil, 0, 100)
	m.Draw(p, th)

	// More lines than pixel rows: lines that collapse onto an already-drawn row
	// are sampled out (the y==prevY branch) rather than overflowing.
	many := make([]string, 600)
	for i := range many {
		many[i] = "z"
	}
	m.update(many, nil, 0, 1)
	m.Draw(p, th)
	if got := m.segmentCount(th.OnSurface); got >= len(many) {
		t.Fatalf("more lines than rows should sample: got %d segments for %d lines", got, len(many))
	}

	// A near-zero width clamps usableW/maxCols to 1 (a long line is clipped).
	m.SetBounds(toolkit.Rect{X: 0, Y: 0, W: 4, H: 100})
	m.update([]string{strings.Repeat("y", 400)}, nil, 0, 1)
	m.Draw(p, th)
}

func TestIntrospectionAccessors(t *testing.T) {
	s := newTestState(t, false)
	if s.EditorWidth() != s.editor.Bounds().W {
		t.Fatalf("EditorWidth mismatch")
	}
	s.rscroll().Scroll(0, 40)
	if s.RenderOffsetY() != s.rscroll().OffsetY {
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
