// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package svgraster

import (
	"image/color"
	"strconv"
	"strings"
)

// matrix is a 2x3 affine transform [a b c d e f] mapping a point (x, y) to
// (a*x + c*y + e, b*x + d*y + f).
type matrix struct {
	a, b, c, d, e, f float64
}

// identity is the identity transform.
func identity() matrix { return matrix{1, 0, 0, 1, 0, 0} }

// apply maps a point through the matrix.
func (m matrix) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

// mul returns the composition m∘n, i.e. the matrix c such that c.apply(p)
// equals m.apply(n.apply(p)). n is the child (inner) transform.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

// parseTransform parses an SVG transform attribute into a matrix. Multiple
// primitives compose left to right (the first listed is the outermost). Unknown
// primitives are ignored. It never fails; a garbage string yields identity.
func parseTransform(s string) matrix {
	m := identity()
	for len(s) > 0 {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			break
		}
		name := strings.TrimSpace(s[:open])
		close := strings.IndexByte(s[open:], ')')
		if close < 0 {
			break
		}
		args := parseFloats(s[open+1 : open+close])
		s = s[open+close+1:]
		switch name {
		case "translate":
			tx := arg(args, 0, 0)
			ty := arg(args, 1, 0)
			m = m.mul(matrix{1, 0, 0, 1, tx, ty})
		case "scale":
			sx := arg(args, 0, 1)
			sy := arg(args, 1, sx)
			m = m.mul(matrix{sx, 0, 0, sy, 0, 0})
		case "matrix":
			if len(args) >= 6 {
				m = m.mul(matrix{args[0], args[1], args[2], args[3], args[4], args[5]})
			}
		}
	}
	return m
}

// arg returns args[i] or def if out of range.
func arg(args []float64, i int, def float64) float64 {
	if i < len(args) {
		return args[i]
	}
	return def
}

// parseFloats parses a comma/space separated list of floats, skipping tokens
// that do not parse.
func parseFloats(s string) []float64 {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]float64, 0, len(fields))
	for _, f := range fields {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// parseLen parses an SVG length. A trailing "%" is resolved against ref; a
// trailing "pt" (or "px") unit is stripped. Unparseable input yields 0.
func parseLen(s string, ref float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "%") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64); err == nil {
			return v / 100 * ref
		}
		return 0
	}
	s = strings.TrimSuffix(s, "pt")
	s = strings.TrimSuffix(s, "px")
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return 0
}

// resolveFill maps an SVG fill attribute value to a concrete colour plus a flag
// telling whether the shape should be painted at all. inherit* provide the value
// used when the attribute is absent, empty or unrecognised. ink is the colour for
// black/currentColor; paper is the colour for white.
func resolveFill(v string, inherit color.RGBA, inheritPaint bool, ink, paper color.RGBA) (color.RGBA, bool) {
	switch strings.TrimSpace(v) {
	case "":
		return inherit, inheritPaint
	case "none":
		return color.RGBA{}, false
	case "black", "currentColor":
		return ink, true
	case "white":
		return paper, true
	}
	if c, ok := parseHexColor(v); ok {
		return c, true
	}
	return inherit, inheritPaint
}

// parseHexColor parses #rgb and #rrggbb colours. It reports ok=false otherwise.
func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(s)
	if len(s) == 0 || s[0] != '#' {
		return color.RGBA{}, false
	}
	h := s[1:]
	switch len(h) {
	case 3:
		r, ok1 := hexNib(h[0])
		g, ok2 := hexNib(h[1])
		b, ok3 := hexNib(h[2])
		if ok1 && ok2 && ok3 {
			return color.RGBA{R: r * 17, G: g * 17, B: b * 17, A: 255}, true
		}
	case 6:
		r, ok1 := hexByte(h[0], h[1])
		g, ok2 := hexByte(h[2], h[3])
		b, ok3 := hexByte(h[4], h[5])
		if ok1 && ok2 && ok3 {
			return color.RGBA{R: r, G: g, B: b, A: 255}, true
		}
	}
	return color.RGBA{}, false
}

// hexNib parses a single hex digit.
func hexNib(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// hexByte parses two hex digits into a byte.
func hexByte(hi, lo byte) (byte, bool) {
	h, ok1 := hexNib(hi)
	l, ok2 := hexNib(lo)
	if !ok1 || !ok2 {
		return 0, false
	}
	return h<<4 | l, true
}
