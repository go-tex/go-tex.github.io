// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"sort"
	"strconv"
	"strings"
	"time"

	engine "github.com/go-tex/engine"
	"github.com/go-widgets/toolkit"
)

// logCompile appends one timestamped block of entries for the just-finished
// compile to the diagnostics [toolkit.LogView], mapping each finding to a level:
//
//   - a hard compile error, and the silent-swallow alarms (a runaway guard, groups
//     left open, a page-count explosion) -> LogError (brick red);
//   - undefined commands / environments and dropped equations -> LogWarn (amber);
//   - the clean-compile summary ("compiled N pages") -> LogInfo.
//
// It NEVER clears the view: the history accumulates across compiles, newest at
// the bottom, so the user watches the log build up over time (the LogView follows
// the tail on its own). Each entry is stamped with the host clock (s.now) — the
// widget never reads the clock itself.
func (s *State) logCompile(res compileResult) {
	ts := s.now()
	if res.errText != "" {
		s.logView.Append(ts, toolkit.LogError, "compile error: "+res.errText)
		return
	}
	d := res.diag
	if d.Runaway {
		s.logView.Append(ts, toolkit.LogError, "runaway guard tripped: a loop or exponential scan was aborted")
	}
	if d.OpenGroups > 0 {
		s.logView.Append(ts, toolkit.LogError, strconv.Itoa(d.OpenGroups)+" group(s) still open at end of document")
	}
	if d.PageCapHit {
		s.logView.Append(ts, toolkit.LogError, "pagination hit the page cap: a page-count explosion")
	}
	appendDiagWarn(s.logView, ts, "undefined command(s)", d.Skipped, "\\")
	appendDiagWarn(s.logView, ts, "undefined environment(s)", d.UndefinedEnvs, "")
	appendDiagWarn(s.logView, ts, "dropped equation(s)", d.MathDropped, "")
	// The verbose outcome block closes each compile's block: how long it took, how
	// big the input was, and how big the output SVG is — TeX's own logs are chatty
	// this way ("Output written on doc.pdf (4 pages, 123456 bytes)").
	s.logView.Append(ts, toolkit.LogInfo, compileOutcome(res))
}

// compileOutcome renders the verbose, TeXLive-flavoured summary of a clean
// compile: a headline naming the page count and the wall time, then indented
// continuation rows (the LogView renders embedded newlines as hanging rows) for
// the input size / line count and the output SVG size. When some logical pages
// were undrawable, it says how many of how many were drawn.
func compileOutcome(res compileResult) string {
	var b strings.Builder
	b.WriteString("compiled ")
	b.WriteString(pageWord(res.drawnPages))
	b.WriteString(" in ")
	b.WriteString(formatDuration(res.elapsed))
	b.WriteString("\n    input   ")
	b.WriteString(formatBytes(res.srcBytes))
	b.WriteString(", ")
	b.WriteString(lineWord(res.srcLines))
	b.WriteString("\n    output  ")
	b.WriteString(formatBytes(res.outBytes))
	b.WriteString(" SVG")
	if res.pages != res.drawnPages {
		b.WriteString(", ")
		b.WriteString(strconv.Itoa(res.drawnPages))
		b.WriteString(" of ")
		b.WriteString(strconv.Itoa(res.pages))
		b.WriteString(" pages drawn")
	}
	return b.String()
}

// formatDuration renders a compile time at a sensible precision: sub-10 ms keeps
// one decimal (0.4 ms), up to a second is whole milliseconds (37 ms), a second or
// more switches to seconds (1.28 s).
func formatDuration(d time.Duration) string {
	switch {
	case d < 10*time.Millisecond:
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64) + " ms"
	case d < time.Second:
		return strconv.Itoa(int(d.Round(time.Millisecond)/time.Millisecond)) + " ms"
	default:
		return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + " s"
	}
}

// formatBytes renders a byte count human-readably: bytes under 1 kB, then kB, then
// MB, decimal (1000-based) with one fractional digit, matching how a reader reads
// a file size rather than TeX's raw byte figure.
func formatBytes(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n) + " B"
	case n < 1000*1000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + " kB"
	default:
		return strconv.FormatFloat(float64(n)/(1000*1000), 'f', 1, 64) + " MB"
	}
}

// lineWord renders a line count with the correctly pluralised unit.
func lineWord(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

// appendDiagWarn appends one amber (LogWarn) entry summarising a category of
// diagnostics, when m is non-empty: a "N title:" header followed by one
// "count× name" line per entry (most frequent first, ties alphabetical), the
// message carrying embedded newlines that the LogView renders as hanging
// continuation rows. prefix is prepended to each name (e.g. "\" for a control
// sequence).
func appendDiagWarn(lv *toolkit.LogView, ts, title string, m map[string]int, prefix string) {
	if len(m) == 0 {
		return
	}
	names := sortedDiagNames(m)
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(names)))
	b.WriteString(" ")
	b.WriteString(title)
	b.WriteString(":")
	for _, n := range names {
		b.WriteString("\n    ")
		b.WriteString(strconv.Itoa(m[n]))
		b.WriteString("×  ")
		b.WriteString(prefix)
		b.WriteString(n)
	}
	lv.Append(ts, toolkit.LogWarn, b.String())
}

// sortedDiagNames orders a diagnostics count map most-frequent-first, breaking
// ties alphabetically, so the same document always logs the same order.
func sortedDiagNames(m map[string]int) []string {
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
	return names
}

// diagIssueCount is the number of issues the status bar badges: a compile error,
// each silent-swallow alarm, and every distinct undefined command / environment /
// dropped equation. It replaces the old logView.alarmCount now that the Log is a
// generic toolkit widget with no diagnostics knowledge of its own.
func diagIssueCount(d engine.Diagnostics, errText string) int {
	n := 0
	if errText != "" {
		n++
	}
	if d.Runaway {
		n++
	}
	if d.OpenGroups > 0 {
		n++
	}
	if d.PageCapHit {
		n++
	}
	return n + len(d.Skipped) + len(d.UndefinedEnvs) + len(d.MathDropped)
}

// pageWord renders a drawn-page count with the correctly pluralised unit.
func pageWord(n int) string {
	if n == 1 {
		return "1 page"
	}
	return strconv.Itoa(n) + " pages"
}

// defaultTimestamp is the fallback host clock the compile Log uses until the
// driver installs a browser-locale one via SetTimeProvider: a plain wall-clock
// HH:MM:SS, so a native build/test still stamps non-empty, changing timestamps.
func defaultTimestamp() string { return time.Now().Format("15:04:05") }
