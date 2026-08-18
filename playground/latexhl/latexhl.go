// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package latexhl is a LaTeX/TeX syntax highlighter implementing the
// go-widgets/toolkit Highlighter interface. It colours comments,
// control sequences (commands), environment names, math regions and
// structural delimiters. The whole buffer is scanned as one stream so
// multi-line math ($$...$$, \[...\]) is coloured across line
// boundaries.
//
// Coverage convention: plain runs are emitted as explicit Default spans
// (Math spans inside math regions), so every rune of the input is
// covered by exactly one ascending, non-overlapping span. This keeps
// the output fully testable and lets a consumer rely on complete
// coverage rather than a mix of spanned and unspanned runes.
package latexhl

import "github.com/go-widgets/toolkit"

// Palette maps LaTeX token categories to ink colours. A field whose
// colour has A==0 is treated as "derive from the theme handed to
// Highlight"; the zero Palette therefore derives every colour.
type Palette struct {
	Default   toolkit.RGBA // ordinary text
	Comment   toolkit.RGBA // % ... end of line
	Command   toolkit.RGBA // \controlsequence
	Math      toolkit.RGBA // $...$, \(...\), \[...\] content
	Delimiter toolkit.RGBA // { } [ ] & and math $ delimiters
	EnvName   toolkit.RGBA // the NAME inside \begin{NAME} / \end{NAME}
}

// warmAnchor is a burnt-orange hue mixed into EnvName so environment
// names read in a warm tone clearly distinct from the (blue) command
// accent, in both light and dark themes.
var warmAnchor = toolkit.RGB(0xB5, 0x51, 0x00)

// opaque returns c forced to full alpha.
func opaque(c toolkit.RGBA) toolkit.RGBA { c.A = 0xFF; return c }

// mix blends channel a toward b by fraction t in [0,1], rounded.
func mixChan(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t + 0.5)
}

// blend mixes colour a toward b by fraction t, returning an opaque colour.
func blend(a, b toolkit.RGBA, t float64) toolkit.RGBA {
	return toolkit.RGBA{
		R: mixChan(a.R, b.R, t),
		G: mixChan(a.G, b.G, t),
		B: mixChan(a.B, b.B, t),
		A: 0xFF,
	}
}

// DefaultPalette derives a theme-consistent palette from a toolkit
// theme: comments are dimmed toward the background, commands take the
// accent, math is an accent/ink blend, delimiters a toned border, and
// environment names a warm tone.
func DefaultPalette(theme *toolkit.Theme) Palette {
	return Palette{
		Default:   opaque(theme.OnSurface),
		Comment:   blend(theme.OnSurface, theme.Background, 0.5),
		Command:   opaque(theme.Accent),
		Math:      blend(theme.OnSurface, theme.Accent, 0.5),
		Delimiter: blend(theme.Border, theme.OnSurface, 0.5),
		EnvName:   blend(warmAnchor, theme.OnSurface, 0.25),
	}
}

// Highlighter is a LaTeX/TeX Highlighter. A zero-value Palette field is
// filled from the theme-derived DefaultPalette at Highlight time.
type Highlighter struct {
	Palette Palette
}

// New returns a ready LaTeX highlighter using a theme-derived palette.
func New() *Highlighter { return &Highlighter{} }

var _ toolkit.Highlighter = (*Highlighter)(nil)

// resolve returns the effective palette: each field with A==0 falls
// back to the theme-derived DefaultPalette, so a fully-zero Palette is
// fully derived and an explicitly set Palette overrides per field.
func (h *Highlighter) resolve(theme *toolkit.Theme) Palette {
	d := DefaultPalette(theme)
	p := h.Palette
	if p.Default.A == 0 {
		p.Default = d.Default
	}
	if p.Comment.A == 0 {
		p.Comment = d.Comment
	}
	if p.Command.A == 0 {
		p.Command = d.Command
	}
	if p.Math.A == 0 {
		p.Math = d.Math
	}
	if p.Delimiter.A == 0 {
		p.Delimiter = d.Delimiter
	}
	if p.EnvName.A == 0 {
		p.EnvName = d.EnvName
	}
	return p
}

// mathKind identifies which delimiter opened the current math region,
// so the matching closer can be recognised.
type mathKind int

const (
	kindNone mathKind = iota
	kindDollar
	kindDDollar
	kindParen
	kindBrack
)

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isSpecial(r rune) bool {
	switch r {
	case '\\', '%', '$', '{', '}', '[', ']', '&':
		return true
	}
	return false
}

func kindFor(n rune) mathKind {
	if n == '(' {
		return kindParen
	}
	return kindBrack
}

// Highlight colours the given lines as LaTeX/TeX. The language argument
// is accepted but ignored (this package is LaTeX-only). The result has
// one entry per input line; empty rows are nil.
func (h *Highlighter) Highlight(language string, lines []string, theme *toolkit.Theme) [][]toolkit.TextSpan {
	_ = language
	p := h.resolve(theme)

	out := make([][]toolkit.TextSpan, len(lines))
	inMath := false
	kind := kindNone

	for li, line := range lines {
		r := []rune(line)
		var spans []toolkit.TextSpan
		add := func(start, end int, c toolkit.RGBA) {
			spans = append(spans, toolkit.TextSpan{Start: start, End: end, Color: c})
		}

		i := 0
		for i < len(r) {
			c := r[i]

			// Comment wins to end of line, even inside math.
			if c == '%' {
				add(i, len(r), p.Comment)
				break
			}

			// Backslash: math delimiter form or control sequence.
			if c == '\\' {
				if i+1 < len(r) {
					n := r[i+1]
					switch n {
					case '(', '[':
						if !inMath {
							inMath = true
							kind = kindFor(n)
						}
						add(i, i+2, p.Delimiter)
						i += 2
						continue
					case ')':
						if inMath && kind == kindParen {
							inMath = false
							kind = kindNone
						}
						add(i, i+2, p.Delimiter)
						i += 2
						continue
					case ']':
						if inMath && kind == kindBrack {
							inMath = false
							kind = kindNone
						}
						add(i, i+2, p.Delimiter)
						i += 2
						continue
					}
				}
				// Ordinary control sequence.
				start := i
				i++ // consume backslash
				if i < len(r) && isLetter(r[i]) {
					for i < len(r) && isLetter(r[i]) {
						i++
					}
					word := string(r[start+1 : i])
					add(start, i, p.Command)
					if (word == "begin" || word == "end") && i < len(r) && r[i] == '{' {
						lb := i
						j := i + 1
						for j < len(r) && r[j] != '}' {
							j++
						}
						add(lb, lb+1, p.Delimiter)
						if j > lb+1 {
							add(lb+1, j, p.EnvName)
						}
						if j < len(r) {
							add(j, j+1, p.Delimiter)
							i = j + 1
						} else {
							i = j
						}
					}
					continue
				}
				if i < len(r) {
					i++ // single non-letter control symbol
					add(start, i, p.Command)
					continue
				}
				add(start, i, p.Command) // lone backslash at EOL
				continue
			}

			// Dollar math toggles.
			if c == '$' {
				if !inMath {
					if i+1 < len(r) && r[i+1] == '$' {
						inMath = true
						kind = kindDDollar
						add(i, i+2, p.Delimiter)
						i += 2
					} else {
						inMath = true
						kind = kindDollar
						add(i, i+1, p.Delimiter)
						i++
					}
					continue
				}
				switch kind {
				case kindDDollar:
					if i+1 < len(r) && r[i+1] == '$' {
						inMath = false
						kind = kindNone
						add(i, i+2, p.Delimiter)
						i += 2
					} else {
						add(i, i+1, p.Delimiter) // lone $ inside $$
						i++
					}
				case kindDollar:
					inMath = false
					kind = kindNone
					add(i, i+1, p.Delimiter)
					i++
				default:
					// $ inside a \(...\) or \[...\] region: a stray delimiter.
					add(i, i+1, p.Delimiter)
					i++
				}
				continue
			}

			// Structural delimiters.
			if c == '{' || c == '}' || c == '[' || c == ']' || c == '&' {
				add(i, i+1, p.Delimiter)
				i++
				continue
			}

			// Plain run: Default outside math, Math inside.
			j := i
			for j < len(r) && !isSpecial(r[j]) {
				j++
			}
			if inMath {
				add(i, j, p.Math)
			} else {
				add(i, j, p.Default)
			}
			i = j
		}

		out[li] = spans
	}

	return out
}
