// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
	"time"
)

// TestFormatDuration covers the three precision bands: sub-10 ms keeps a decimal,
// up to a second is whole milliseconds, a second or more switches to seconds.
func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{400 * time.Microsecond, "0.4 ms"},
		{9500 * time.Microsecond, "9.5 ms"},
		{37 * time.Millisecond, "37 ms"},
		{999 * time.Millisecond, "999 ms"},
		{1280 * time.Millisecond, "1.28 s"},
		{3 * time.Second, "3.00 s"},
	} {
		if got := formatDuration(tc.d); got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestFormatBytes covers bytes / kB / MB.
func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{1234, "1.2 kB"},
		{999999, "1000.0 kB"},
		{2_500_000, "2.5 MB"},
	} {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestLineCount covers the empty and non-empty (with and without a trailing
// newline) branches.
func TestLineCount(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"", 0},
		{"one line", 1},
		{"a\nb\nc", 3},
		{"trailing\n", 2},
	} {
		if got := lineCount(tc.src); got != tc.want {
			t.Errorf("lineCount(%q) = %d, want %d", tc.src, got, tc.want)
		}
	}
}

// TestLineWord covers the singular and plural units.
func TestLineWord(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1 line"},
		{0, "0 lines"},
		{42, "42 lines"},
	} {
		if got := lineWord(tc.n); got != tc.want {
			t.Errorf("lineWord(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestCompileOutcome checks the verbose block: the headline (pages + time), the
// indented input and output rows, and the "N of M pages drawn" clause that only
// appears when some logical pages were undrawable.
func TestCompileOutcome(t *testing.T) {
	// All pages drawn: no "of" clause.
	clean := compileOutcome(compileResult{
		pages: 1, drawnPages: 1, elapsed: 37 * time.Millisecond,
		srcBytes: 1234, srcLines: 56, outBytes: 80_000,
	})
	for _, want := range []string{"compiled 1 page in 37 ms", "input   1.2 kB, 56 lines", "output  80.0 kB SVG"} {
		if !strings.Contains(clean, want) {
			t.Errorf("clean outcome %q missing %q", clean, want)
		}
	}
	if strings.Contains(clean, "of") {
		t.Errorf("clean outcome should not mention undrawn pages: %q", clean)
	}

	// Some logical pages undrawable: the "N of M pages drawn" clause appears.
	partial := compileOutcome(compileResult{
		pages: 3, drawnPages: 2, elapsed: 1280 * time.Millisecond,
		srcBytes: 400, srcLines: 1, outBytes: 12_345,
	})
	for _, want := range []string{"compiled 2 pages in 1.28 s", "input   400 B, 1 line", "output  12.3 kB SVG, 2 of 3 pages drawn"} {
		if !strings.Contains(partial, want) {
			t.Errorf("partial outcome %q missing %q", partial, want)
		}
	}
}
