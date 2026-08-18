// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package latexhl

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// RED/GREEN discipline: a no-op highlighter (one that returned all-nil
// rows, or the wrong colours) would fail every assertion below. In
// particular TestExactSpanSet pins a complete span slice, so an
// implementation that "did nothing" — or that mis-classified a single
// rune — cannot pass. With no highlighter these spans would be absent.

func span(start, end int, c toolkit.RGBA) toolkit.TextSpan {
	return toolkit.TextSpan{Start: start, End: end, Color: c}
}

// find returns the first span whose [Start,End) contains rune index pos,
// or (span{}, false) if none.
func spanAt(spans []toolkit.TextSpan, pos int) (toolkit.TextSpan, bool) {
	for _, s := range spans {
		if pos >= s.Start && pos < s.End {
			return s, true
		}
	}
	return toolkit.TextSpan{}, false
}

func TestInterfaceSatisfaction(t *testing.T) {
	// Compile-time check is the var _ line in latexhl.go; here we call
	// through the interface type to prove it dispatches.
	var hl toolkit.Highlighter = New()
	theme := toolkit.DefaultLight()
	got := hl.Highlight("latex", []string{`\alpha`}, theme)
	if len(got) != 1 || len(got[0]) == 0 {
		t.Fatalf("interface dispatch produced no spans: %#v", got)
	}
}

func TestDefaultPaletteDistinct(t *testing.T) {
	for _, theme := range []*toolkit.Theme{toolkit.DefaultLight(), toolkit.DefaultDark()} {
		p := DefaultPalette(theme)
		if p.Comment == p.Default {
			t.Errorf("Comment must differ from Default: %v", p.Comment)
		}
		if p.Command == p.Default {
			t.Errorf("Command must differ from Default: %v", p.Command)
		}
		if p.EnvName == p.Command {
			t.Errorf("EnvName must differ from Command: %v", p.EnvName)
		}
		if p.Math == p.Default || p.Math == p.Command {
			t.Errorf("Math must differ from Default and Command: %v", p.Math)
		}
		if p.Delimiter == p.Default {
			t.Errorf("Delimiter must differ from Default: %v", p.Delimiter)
		}
		// Every derived colour is opaque.
		for _, c := range []toolkit.RGBA{p.Default, p.Comment, p.Command, p.Math, p.Delimiter, p.EnvName} {
			if c.A != 0xFF {
				t.Errorf("derived colour not opaque: %v", c)
			}
		}
	}
}

// TestReportLightPalette logs the exact DefaultLight colours for a
// human visual-distinctness sanity check (visible with -v).
func TestReportLightPalette(t *testing.T) {
	p := DefaultPalette(toolkit.DefaultLight())
	t.Logf("DefaultLight palette:")
	t.Logf("  Default   = #%02X%02X%02X", p.Default.R, p.Default.G, p.Default.B)
	t.Logf("  Comment   = #%02X%02X%02X", p.Comment.R, p.Comment.G, p.Comment.B)
	t.Logf("  Command   = #%02X%02X%02X", p.Command.R, p.Command.G, p.Command.B)
	t.Logf("  Math      = #%02X%02X%02X", p.Math.R, p.Math.G, p.Math.B)
	t.Logf("  Delimiter = #%02X%02X%02X", p.Delimiter.R, p.Delimiter.G, p.Delimiter.B)
	t.Logf("  EnvName   = #%02X%02X%02X", p.EnvName.R, p.EnvName.G, p.EnvName.B)
}

func TestComment(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// "x = 1 % hi": '%' at rune 6, line length 10.
	got := New().Highlight("", []string{"x = 1 % hi"}, theme)
	line := got[0]
	s, ok := spanAt(line, 6)
	if !ok || s.Start != 6 || s.End != 10 || s.Color != p.Comment {
		t.Fatalf("comment span wrong: %#v", s)
	}
	// The text before the '%' is not comment-coloured.
	before, ok := spanAt(line, 0)
	if !ok || before.Color == p.Comment {
		t.Fatalf("pre-comment text wrongly coloured: %#v", before)
	}
	if before.Color != p.Default {
		t.Fatalf("pre-comment text should be Default: %#v", before)
	}
}

func TestEscapedPercentNoComment(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	got := New().Highlight("", []string{`50\% off`}, theme)
	for _, s := range got[0] {
		if s.Color == p.Comment {
			t.Fatalf("escaped percent produced a comment span: %#v", s)
		}
	}
	// The \% is a Command (backslash + non-letter).
	s, ok := spanAt(got[0], 2) // '\' at index 2
	if !ok || s.Color != p.Command || s.Start != 2 || s.End != 4 {
		t.Fatalf(`\%% should be a Command span [2,4): %#v`, s)
	}
}

func TestExactSpanSet(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// \section{Intro}
	got := New().Highlight("latex", []string{`\section{Intro}`}, theme)
	want := []toolkit.TextSpan{
		span(0, 8, p.Command),     // \section
		span(8, 9, p.Delimiter),   // {
		span(9, 14, p.Default),    // Intro
		span(14, 15, p.Delimiter), // }
	}
	if len(got[0]) != len(want) {
		t.Fatalf("span count: got %d want %d (%#v)", len(got[0]), len(want), got[0])
	}
	for i := range want {
		if got[0][i] != want[i] {
			t.Fatalf("span %d: got %#v want %#v", i, got[0][i], want[i])
		}
	}
}

func TestEnvironment(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	cases := []struct {
		src     string
		envFrom int
		envTo   int
	}{
		{`\begin{itemize}`, 7, 14},
		{`\end{itemize}`, 5, 12},
		{`\begin{figure*}`, 7, 14}, // starred env, includes '*'
	}
	for _, tc := range cases {
		got := New().Highlight("", []string{tc.src}, theme)[0]
		// \begin / \end is a Command.
		cmd, ok := spanAt(got, 0)
		if !ok || cmd.Color != p.Command {
			t.Fatalf("%s: command span wrong: %#v", tc.src, cmd)
		}
		// The env name is EnvName-coloured, distinct from Command.
		env, ok := spanAt(got, tc.envFrom)
		if !ok || env.Start != tc.envFrom || env.End != tc.envTo || env.Color != p.EnvName {
			t.Fatalf("%s: env-name span wrong: %#v", tc.src, env)
		}
		if env.Color == cmd.Color {
			t.Fatalf("%s: EnvName must differ from Command", tc.src)
		}
		// Braces are Delimiter.
		lb, _ := spanAt(got, tc.envFrom-1)
		rb, _ := spanAt(got, tc.envTo)
		if lb.Color != p.Delimiter || rb.Color != p.Delimiter {
			t.Fatalf("%s: braces not Delimiter: %#v %#v", tc.src, lb, rb)
		}
	}
}

func TestEnvironmentEdgeCases(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)

	// Empty env name: \begin{} -> no EnvName span, both braces Delimiter.
	got := New().Highlight("", []string{`\begin{}`}, theme)[0]
	for _, s := range got {
		if s.Color == p.EnvName {
			t.Fatalf("empty env name produced EnvName span: %#v", s)
		}
	}

	// Unterminated env: \begin{itemize (no closing brace).
	got = New().Highlight("", []string{`\begin{itemize`}, theme)[0]
	env, ok := spanAt(got, 7)
	if !ok || env.Color != p.EnvName || env.Start != 7 || env.End != 14 {
		t.Fatalf("unterminated env name span wrong: %#v", env)
	}

	// \begin NOT followed by a brace: plain command, no env handling.
	got = New().Highlight("", []string{`\begin next`}, theme)[0]
	cmd, _ := spanAt(got, 0)
	if cmd.Color != p.Command || cmd.End != 6 {
		t.Fatalf("\\begin without brace: %#v", cmd)
	}
}

func TestInlineMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// $x^2+1$
	got := New().Highlight("", []string{`$x^2+1$`}, theme)[0]
	open, _ := spanAt(got, 0)
	closeS, _ := spanAt(got, 6)
	if open.Color != p.Delimiter || closeS.Color != p.Delimiter {
		t.Fatalf("$ delimiters not Delimiter: %#v %#v", open, closeS)
	}
	inside, ok := spanAt(got, 1)
	if !ok || inside.Color != p.Math || inside.Start != 1 || inside.End != 6 {
		t.Fatalf("math content span wrong: %#v", inside)
	}
}

func TestCommandInsideMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// $\frac{a}{b}$
	got := New().Highlight("", []string{`$\frac{a}{b}$`}, theme)[0]
	frac, ok := spanAt(got, 1) // backslash of \frac
	if !ok || frac.Color != p.Command || frac.Start != 1 || frac.End != 6 {
		t.Fatalf("\\frac inside math not Command: %#v", frac)
	}
	// 'a' between braces inside math is Math-coloured.
	a, ok := spanAt(got, 7)
	if !ok || a.Color != p.Math {
		t.Fatalf("math arg not Math: %#v", a)
	}
	// Braces inside math are Delimiter.
	brace, _ := spanAt(got, 6)
	if brace.Color != p.Delimiter {
		t.Fatalf("brace inside math not Delimiter: %#v", brace)
	}
}

func TestMultiLineDisplayMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// \[ / a+b / \] : state carried across lines, middle line is Math.
	got := New().Highlight("", []string{`\[`, `a+b`, `\]`}, theme)
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	openD, _ := spanAt(got[0], 0)
	if openD.Color != p.Delimiter || openD.Start != 0 || openD.End != 2 {
		t.Fatalf("\\[ not Delimiter[0,2): %#v", openD)
	}
	mid, ok := spanAt(got[1], 0)
	if !ok || mid.Color != p.Math || mid.Start != 0 || mid.End != 3 {
		t.Fatalf("middle line not Math across lines: %#v", mid)
	}
	closeD, _ := spanAt(got[2], 0)
	if closeD.Color != p.Delimiter || closeD.End != 2 {
		t.Fatalf("\\] not Delimiter: %#v", closeD)
	}
}

func TestDisplayDollarMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// $$ ... $$ across lines, plus a lone $ inside the region.
	got := New().Highlight("", []string{`$$a`, `b$c`, `d$$`}, theme)
	// line0: $$ opens (Delimiter[0,2]), 'a' Math.
	if s, _ := spanAt(got[0], 0); s.Color != p.Delimiter || s.End != 2 {
		t.Fatalf("$$ open wrong: %#v", s)
	}
	if s, _ := spanAt(got[0], 2); s.Color != p.Math {
		t.Fatalf("post-$$ not Math: %#v", s)
	}
	// line1: 'b' Math, lone '$' Delimiter (does not close $$), 'c' Math.
	if s, _ := spanAt(got[1], 0); s.Color != p.Math {
		t.Fatalf("b not Math: %#v", s)
	}
	if s, _ := spanAt(got[1], 1); s.Color != p.Delimiter {
		t.Fatalf("lone $ inside $$ not Delimiter: %#v", s)
	}
	if s, _ := spanAt(got[1], 2); s.Color != p.Math {
		t.Fatalf("c still in math not Math: %#v", s)
	}
	// line2: 'd' Math, then $$ closes.
	if s, _ := spanAt(got[2], 0); s.Color != p.Math {
		t.Fatalf("d not Math: %#v", s)
	}
	if s, _ := spanAt(got[2], 1); s.Color != p.Delimiter || s.End != 3 {
		t.Fatalf("$$ close wrong: %#v", s)
	}
}

func TestParenMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// \(x\) : \( and \) are Delimiter, x is Math.
	got := New().Highlight("", []string{`\(x\)`}, theme)[0]
	if s, _ := spanAt(got, 0); s.Color != p.Delimiter || s.End != 2 {
		t.Fatalf("\\( not Delimiter[0,2): %#v", s)
	}
	if s, _ := spanAt(got, 2); s.Color != p.Math {
		t.Fatalf("paren-math content not Math: %#v", s)
	}
	if s, _ := spanAt(got, 3); s.Color != p.Delimiter {
		t.Fatalf("\\) not Delimiter: %#v", s)
	}
}

func TestStrayMathDelimiters(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// Stray \) and \] with no open math: coloured Delimiter, no math opened.
	got := New().Highlight("", []string{`a\)b\]c`}, theme)[0]
	if s, _ := spanAt(got, 0); s.Color != p.Default {
		t.Fatalf("leading a not Default (stray closer opened math?): %#v", s)
	}
	if s, _ := spanAt(got, 1); s.Color != p.Delimiter {
		t.Fatalf("stray \\) not Delimiter: %#v", s)
	}
	if s, _ := spanAt(got, 3); s.Color != p.Default {
		t.Fatalf("b not Default: %#v", s)
	}
}

func TestDollarInsideParenMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// Inside \(...\), a '$' is a stray delimiter, not a toggle.
	got := New().Highlight("", []string{`\(a$b\)`}, theme)[0]
	if s, _ := spanAt(got, 2); s.Color != p.Math { // 'a'
		t.Fatalf("a not Math: %#v", s)
	}
	if s, _ := spanAt(got, 3); s.Color != p.Delimiter { // '$'
		t.Fatalf("$ inside paren-math not Delimiter: %#v", s)
	}
	if s, _ := spanAt(got, 4); s.Color != p.Math { // 'b' still math
		t.Fatalf("b after stray $ not Math: %#v", s)
	}
}

func TestEscapedDollarNoMath(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// \$5 : \$ is a Command, 5 is Default (no math opened).
	got := New().Highlight("", []string{`\$5`}, theme)[0]
	s, _ := spanAt(got, 0)
	if s.Color != p.Command || s.Start != 0 || s.End != 2 {
		t.Fatalf("\\$ not Command[0,2): %#v", s)
	}
	five, _ := spanAt(got, 2)
	if five.Color != p.Default {
		t.Fatalf("5 not Default (math wrongly open?): %#v", five)
	}
}

func TestLoneBackslashAndSymbols(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// Lone backslash at end of line.
	got := New().Highlight("", []string{`a\`}, theme)[0]
	s, _ := spanAt(got, 1)
	if s.Color != p.Command || s.Start != 1 || s.End != 2 {
		t.Fatalf("lone backslash not Command[1,2): %#v", s)
	}
	// \\ (double backslash) is a single-symbol command.
	got = New().Highlight("", []string{`\\`}, theme)[0]
	s, _ = spanAt(got, 0)
	if s.Color != p.Command || s.End != 2 {
		t.Fatalf("\\\\ not Command[0,2): %#v", s)
	}
	// Uppercase-letter command name (exercises isLetter upper branch).
	got = New().Highlight("", []string{`\Big`}, theme)[0]
	s, _ = spanAt(got, 0)
	if s.Color != p.Command || s.End != 4 {
		t.Fatalf("\\Big not Command[0,4): %#v", s)
	}
}

func TestBareDelimiters(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// { } [ ] & outside math are Delimiter.
	got := New().Highlight("", []string{`{}[]&`}, theme)[0]
	for pos := 0; pos < 5; pos++ {
		if s, _ := spanAt(got, pos); s.Color != p.Delimiter {
			t.Fatalf("bare delimiter at %d not Delimiter: %#v", pos, s)
		}
	}
}

func TestMultibyteRuneOffsets(t *testing.T) {
	theme := toolkit.DefaultLight()
	p := DefaultPalette(theme)
	// "é % c" : é(0) ' '(1) %(2) ' '(3) c(4), 5 runes.
	got := New().Highlight("", []string{"é % c"}, theme)[0]
	cm, ok := spanAt(got, 2)
	if !ok || cm.Color != p.Comment || cm.Start != 2 || cm.End != 5 {
		t.Fatalf("comment span on multibyte line wrong (rune coords): %#v", cm)
	}
	// The leading 'é ' run is Default over rune [0,2).
	lead, _ := spanAt(got, 0)
	if lead.Color != p.Default || lead.Start != 0 || lead.End != 2 {
		t.Fatalf("leading multibyte run not Default[0,2): %#v", lead)
	}
}

func TestPaletteOverride(t *testing.T) {
	theme := toolkit.DefaultLight()
	custom := Palette{
		Default:   toolkit.RGB(1, 2, 3),
		Comment:   toolkit.RGB(10, 20, 30),
		Command:   toolkit.RGB(40, 50, 60),
		Math:      toolkit.RGB(70, 80, 90),
		Delimiter: toolkit.RGB(100, 110, 120),
		EnvName:   toolkit.RGB(130, 140, 150),
	}
	h := &Highlighter{Palette: custom}
	got := h.Highlight("", []string{`\section{a} % c`}, theme)[0]
	// Command uses the custom colour, not the theme-derived one.
	if s, _ := spanAt(got, 0); s.Color != custom.Command {
		t.Fatalf("override not applied to Command: %#v", s)
	}
	// Comment uses the custom colour.
	if s, _ := spanAt(got, 12); s.Color != custom.Comment {
		t.Fatalf("override not applied to Comment: %#v", s)
	}
	// And definitely not the theme-derived palette.
	if DefaultPalette(theme).Command == custom.Command {
		t.Fatalf("test setup: custom Command accidentally equals derived")
	}
}

func TestPartialPaletteFallback(t *testing.T) {
	theme := toolkit.DefaultLight()
	d := DefaultPalette(theme)
	// Only Command set; every other field (A==0) falls back to derived.
	h := &Highlighter{Palette: Palette{Command: toolkit.RGB(9, 9, 9)}}
	got := h.Highlight("", []string{`\a{b} % c`}, theme)[0]
	if s, _ := spanAt(got, 0); s.Color != (toolkit.RGBA{R: 9, G: 9, B: 9, A: 0xFF}) {
		t.Fatalf("partial override: Command not custom: %#v", s)
	}
	if s, _ := spanAt(got, 2); s.Color != d.Delimiter { // '{'
		t.Fatalf("partial override: Delimiter not derived: %#v", s)
	}
	if s, _ := spanAt(got, 6); s.Color != d.Comment {
		t.Fatalf("partial override: Comment not derived: %#v", s)
	}
}

func TestResultLengthAndEmptyLines(t *testing.T) {
	theme := toolkit.DefaultLight()
	lines := []string{`\a`, ``, `b`, ``}
	got := New().Highlight("", lines, theme)
	if len(got) != len(lines) {
		t.Fatalf("len(result)=%d, want %d", len(got), len(lines))
	}
	if got[1] != nil || got[3] != nil {
		t.Fatalf("empty lines should be nil rows: %#v %#v", got[1], got[3])
	}
	if len(got[0]) == 0 || len(got[2]) == 0 {
		t.Fatalf("non-empty lines should have spans")
	}
}

func TestSpansAscendingNonOverlapping(t *testing.T) {
	theme := toolkit.DefaultLight()
	src := []string{`\begin{eq} $x+\frac{a}{b}$ % done`, `\[`, `y=1`, `\]`}
	got := New().Highlight("", src, theme)
	for li, row := range got {
		prev := 0
		for _, s := range row {
			if s.Start < prev {
				t.Fatalf("line %d: span %#v not ascending (prev end %d)", li, s, prev)
			}
			if s.End < s.Start {
				t.Fatalf("line %d: span %#v has End<Start", li, s)
			}
			prev = s.End
		}
	}
}
