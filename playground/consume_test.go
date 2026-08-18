// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strconv"
	"testing"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

// The message inks toolkit.LogView paints each level in (mirrors its levelInk):
// LogWarn amber, LogError brick red. The headless log-level assertions scan the
// drawn Log tab for these inks — a fully-covered glyph pixel lands on the exact
// colour, so a small tolerance absorbs anti-aliasing without matching the ground.
var (
	logWarnInk = toolkit.RGB(0xE0, 0xA0, 0x30)
	logErrInk  = toolkit.RGB(0xC0, 0x30, 0x30)
)

// bufHasColor reports whether any pixel of buf (row-major RGBA, width w) inside
// region is within a small tolerance of c — used to prove a level's tinted ink
// was actually painted.
func bufHasColor(buf []byte, w int, region toolkit.Rect, c toolkit.RGBA) bool {
	const tol = 40
	near := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tol
	}
	for y := region.Y; y < region.Y+region.H; y++ {
		if y < 0 {
			continue
		}
		for x := region.X; x < region.X+region.W; x++ {
			if x < 0 {
				continue
			}
			i := (y*w + x) * 4
			if i+2 >= len(buf) {
				continue
			}
			if near(buf[i], c.R) && near(buf[i+1], c.G) && near(buf[i+2], c.B) {
				return true
			}
		}
	}
	return false
}

// diagAllFields is a diagnostics set exercising every logCompile branch at once:
// the three silent-swallow alarms (Error) and the three undefined/dropped
// categories (Warn), the command map carrying a count tie to drive the
// alphabetical tie-break.
var diagAllFields = engine.Diagnostics{
	Runaway:       true,
	OpenGroups:    2,
	PageCapHit:    true,
	Skipped:       map[string]int{"foo": 3, "bar": 3, "baz": 1},
	UndefinedEnvs: map[string]int{"mysteryenv": 1},
	MathDropped:   map[string]int{"\\zzz": 1},
}

// TestLogAccumulatesWithLevelsAndTimestamps is the core of the LogView
// consumption: a compile appends a timestamped block to the Log at the right
// levels, a SECOND compile ACCUMULATES on top (history kept, never cleared) with
// a fresh timestamp, and drawing the Log tab paints both the amber (Warn) and the
// brick-red (Error) inks.
func TestLogAccumulatesWithLevelsAndTimestamps(t *testing.T) {
	s := newTestState(t, false)

	var ticks int
	s.SetTimeProvider(func() string { ticks++; return "T" + strconv.Itoa(ticks) })

	before := s.LogEntryCount()
	res := compileResult{drawnPages: 2, diag: diagAllFields}

	s.logCompile(res)
	afterFirst := s.LogEntryCount()
	if afterFirst <= before {
		t.Fatalf("first compile did not append entries: %d -> %d", before, afterFirst)
	}
	// A second compile keeps the history and adds to it (the user watches it build
	// up), rather than clearing.
	s.logCompile(res)
	if s.LogEntryCount() <= afterFirst {
		t.Fatalf("second compile did not accumulate: %d -> %d", afterFirst, s.LogEntryCount())
	}
	if ticks < 2 {
		t.Fatalf("timestamp provider not called once per compile (ticks=%d)", ticks)
	}

	// Draw the Log tab and assert both level inks are painted.
	s.toggleLog()
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	cr := s.rightPane.contentRect()
	if !bufHasColor(buf, testW, cr, logWarnInk) {
		t.Fatalf("Log did not paint amber (Warn) ink")
	}
	if !bufHasColor(buf, testW, cr, logErrInk) {
		t.Fatalf("Log did not paint brick-red (Error) ink")
	}
}

// TestLogCompileErrorBranch: a hard compile error logs a single Error entry and
// skips the per-diagnostic detail (the early return).
func TestLogCompileErrorBranch(t *testing.T) {
	s := newTestState(t, false)
	before := s.LogEntryCount()
	s.logCompile(compileResult{errText: "boom"})
	if s.LogEntryCount() != before+1 {
		t.Fatalf("error compile should add exactly one entry: %d -> %d", before, s.LogEntryCount())
	}
	s.toggleLog()
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)
	if !bufHasColor(buf, testW, s.rightPane.contentRect(), logErrInk) {
		t.Fatalf("error entry did not paint brick-red ink")
	}
}

func TestDiagIssueCount(t *testing.T) {
	if got := diagIssueCount(engine.Diagnostics{}, ""); got != 0 {
		t.Fatalf("clean diagnostics issue count = %d, want 0", got)
	}
	d := engine.Diagnostics{
		Runaway:       true,
		OpenGroups:    1,
		PageCapHit:    true,
		Skipped:       map[string]int{"a": 1, "b": 1},
		UndefinedEnvs: map[string]int{"e": 1},
		MathDropped:   map[string]int{"m": 1},
	}
	// 3 alarms + 2 skipped + 1 env + 1 math = 7, plus the error string = 8.
	if got := diagIssueCount(d, "err"); got != 8 {
		t.Fatalf("issue count = %d, want 8", got)
	}
	if got := diagIssueCount(d, ""); got != 7 {
		t.Fatalf("issue count without error = %d, want 7", got)
	}
}

func TestPageWord(t *testing.T) {
	if got := pageWord(1); got != "1 page" {
		t.Fatalf("pageWord(1) = %q", got)
	}
	if got := pageWord(0); got != "0 pages" {
		t.Fatalf("pageWord(0) = %q", got)
	}
	if got := pageWord(3); got != "3 pages" {
		t.Fatalf("pageWord(3) = %q", got)
	}
}

func TestSortedDiagNames(t *testing.T) {
	got := sortedDiagNames(map[string]int{"foo": 3, "bar": 3, "baz": 1})
	want := []string{"bar", "foo", "baz"} // 3,3 alphabetical, then the lone 1
	if len(got) != len(want) {
		t.Fatalf("sortedDiagNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedDiagNames[%d] = %q, want %q (%v)", i, got[i], want[i], got)
		}
	}
	if n := sortedDiagNames(map[string]int{}); len(n) != 0 {
		t.Fatalf("empty map should sort to no names, got %v", n)
	}
}

func TestDefaultTimestampNonEmpty(t *testing.T) {
	if defaultTimestamp() == "" {
		t.Fatalf("defaultTimestamp should produce a non-empty wall-clock string")
	}
}

// TestSetTimeProvider: a nil hook is ignored (the default stays and still works);
// a non-nil hook is installed and called on the next compile.
func TestSetTimeProvider(t *testing.T) {
	s := newTestState(t, false)
	s.SetTimeProvider(nil) // ignored, no panic
	s.Compile()            // the default clock still stamps entries

	called := false
	s.SetTimeProvider(func() string { called = true; return "X" })
	s.Compile()
	if !called {
		t.Fatalf("installed time provider was not called on compile")
	}
}
