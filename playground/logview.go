// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"sort"
	"strconv"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// logView renders the compile Diagnostics as a scrollable-looking report: the
// undefined control sequences and environments that were skipped and the whole
// equations the math layer dropped (each with a count), plus the silent-swallow
// alarms (a runaway that tripped, groups left open, a page-count explosion). It
// restores the "Log" tab the legacy textarea playground had, now inside the
// canvas UI, and refreshes whenever the document recompiles.
type logView struct {
	toolkit.Base

	diag    engine.Diagnostics
	errText string
	offset  int // vertical scroll offset in device pixels
}

// set updates the report inputs.
func (l *logView) set(d engine.Diagnostics, errText string) {
	l.diag = d
	l.errText = errText
}

// logEntry is one rendered row: text plus a role that picks its colour.
type logEntry struct {
	text string
	role int // 0 plain, 1 header, 2 warn/alarm, 3 count-dim
}

// rows builds the report lines from the diagnostics.
func (l *logView) rows() []logEntry {
	var out []logEntry
	alarms := 0
	if l.errText != "" {
		out = append(out, logEntry{"Compile error: " + l.errText, 2})
		alarms++
	}
	if l.diag.Runaway {
		out = append(out, logEntry{"! Runaway guard tripped - a loop or exponential scan was aborted.", 2})
		alarms++
	}
	if l.diag.OpenGroups > 0 {
		out = append(out, logEntry{"! " + strconv.Itoa(l.diag.OpenGroups) + " group(s) still open at end of document.", 2})
		alarms++
	}
	if l.diag.PageCapHit {
		out = append(out, logEntry{"! Pagination hit the page cap - a page-count explosion.", 2})
		alarms++
	}
	out = appendCounts(out, "Undefined command(s) skipped", l.diag.Skipped, "\\")
	out = appendCounts(out, "Undefined environment(s)", l.diag.UndefinedEnvs, "")
	out = appendCounts(out, "Dropped equation(s)", l.diag.MathDropped, "")

	if alarms == 0 && len(out) == 0 {
		out = append(out, logEntry{"OK - No undefined commands, environments or dropped math; clean compile.", 1})
	}
	return out
}

// appendCounts adds a header + one "count  name" row per entry (most frequent
// first) when m is non-empty. prefix is prepended to each name (e.g. "\" for a
// control sequence).
func appendCounts(out []logEntry, title string, m map[string]int, prefix string) []logEntry {
	if len(m) == 0 {
		return out
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	out = append(out, logEntry{strconv.Itoa(len(names)) + " " + title + " (most frequent first):", 1})
	for _, n := range names {
		out = append(out, logEntry{"    " + strconv.Itoa(m[n]) + "x  " + prefix + n, 3})
	}
	return out
}

// alarmCount returns the number of alarm/error rows, for the toolbar badge.
func (l *logView) alarmCount() int {
	n := 0
	if l.errText != "" {
		n++
	}
	if l.diag.Runaway {
		n++
	}
	if l.diag.OpenGroups > 0 {
		n++
	}
	if l.diag.PageCapHit {
		n++
	}
	return n + len(l.diag.Skipped) + len(l.diag.UndefinedEnvs) + len(l.diag.MathDropped)
}

// Draw paints the report.
func (l *logView) Draw(p painter.Painter, theme *toolkit.Theme) {
	r := l.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	p.FillRect(r, theme.Background)
	// A theme-safe red for alarms/errors, readable on both light and dark grounds.
	warn := toolkit.RGB(0xD3, 0x3A, 0x3A)
	if l.dark(theme) {
		warn = toolkit.RGB(0xF2, 0x6D, 0x6D)
	}
	lineH := toolkit.GlyphHeight() + toolkit.Scaled(6)
	pad := toolkit.Scaled(10)
	y := r.Y + pad - l.offset
	for _, e := range l.rows() {
		if y > r.Y-lineH && y < r.Y+r.H {
			ink := theme.OnSurface // plain + count rows: full contrast
			switch e.role {
			case 1:
				ink = theme.Accent // section header
			case 2:
				ink = warn // alarm / error
			}
			toolkit.DrawText(p, r.X+pad, y, e.text, ink) // active OpenType font, not the 5x7 bitmap
		}
		y += lineH
	}
}

// dark reports whether the theme reads as a dark scheme (its background is
// darker than its ink), used to pick a readable alarm red.
func (l *logView) dark(theme *toolkit.Theme) bool {
	lum := func(c toolkit.RGBA) int { return int(c.R) + int(c.G) + int(c.B) }
	return lum(theme.Background) < lum(theme.OnSurface)
}
