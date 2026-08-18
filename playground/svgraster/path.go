// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package svgraster

import (
	"strconv"

	"github.com/go-gfx/gfx/vector"
)

// pathScanner tokenises an SVG path "d" string into command letters and numbers.
type pathScanner struct {
	s   string
	i   int
	bad bool
}

// skipSep advances over whitespace and commas.
func (p *pathScanner) skipSep() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r', ',':
			p.i++
		default:
			return
		}
	}
}

// isCmd reports whether c is a path command letter.
func isCmd(c byte) bool {
	switch c {
	case 'M', 'm', 'L', 'l', 'H', 'h', 'V', 'v', 'C', 'c',
		'S', 's', 'Q', 'q', 'T', 't', 'Z', 'z', 'A', 'a':
		return true
	}
	return false
}

// nextCmd returns the next command letter, or 0 at end of input. A non-command,
// non-numeric byte sets bad.
func (p *pathScanner) nextCmd() byte {
	p.skipSep()
	if p.i >= len(p.s) {
		return 0
	}
	c := p.s[p.i]
	if !isCmd(c) {
		p.bad = true
		return 0
	}
	p.i++
	return c
}

// number parses the next float. On failure it sets bad and returns 0.
func (p *pathScanner) number() float64 {
	p.skipSep()
	start := p.i
	if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		p.i++
	}
	seenDigit := false
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c >= '0' && c <= '9' {
			seenDigit = true
			p.i++
			continue
		}
		if c == '.' {
			p.i++
			continue
		}
		if (c == 'e' || c == 'E') && seenDigit {
			p.i++
			if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
				p.i++
			}
			continue
		}
		break
	}
	if !seenDigit {
		p.bad = true
		return 0
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		p.bad = true
		return 0
	}
	return v
}

// more reports whether the next non-separator byte can begin a number, marking a
// repeated implicit coordinate set for the current command.
func (p *pathScanner) more() bool {
	p.skipSep()
	if p.i >= len(p.s) {
		return false
	}
	c := p.s[p.i]
	return c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9')
}

// up upper-cases a command letter.
func up(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

// buildPath parses a "d" string and emits a device-space vector.Path with the
// affine m applied to every coordinate. It returns ok=false when the path uses
// an unsupported command (arcs), starts a drawing command with no move, or is
// otherwise malformed, so the caller can skip it without failing the page.
func buildPath(d string, m matrix) (*vector.Path, bool) {
	sc := &pathScanner{s: d}
	path := vector.NewPath()

	var (
		curX, curY     float64 // current point, user space
		startX, startY float64 // subpath start, user space
		prevCX, prevCY float64 // last cubic control, user space
		prevQX, prevQY float64 // last quad control, user space
		hasStart       bool
		prevCmd        byte
		emitted        bool
	)

	moveTo := func(x, y float64) {
		dx, dy := m.apply(x, y)
		path.MoveTo(dx, dy)
	}
	lineTo := func(x, y float64) {
		dx, dy := m.apply(x, y)
		path.LineTo(dx, dy)
		emitted = true
	}
	quadTo := func(cx, cy, x, y float64) {
		dcx, dcy := m.apply(cx, cy)
		dx, dy := m.apply(x, y)
		path.QuadTo(dcx, dcy, dx, dy)
		emitted = true
	}
	cubicTo := func(c1x, c1y, c2x, c2y, x, y float64) {
		d1x, d1y := m.apply(c1x, c1y)
		d2x, d2y := m.apply(c2x, c2y)
		dx, dy := m.apply(x, y)
		path.CubicTo(d1x, d1y, d2x, d2y, dx, dy)
		emitted = true
	}

	for {
		cmd := sc.nextCmd()
		if sc.bad {
			return nil, false
		}
		if cmd == 0 {
			break
		}
		if cmd == 'A' || cmd == 'a' {
			return nil, false // arcs unsupported
		}
		abs := cmd >= 'A' && cmd <= 'Z'

		switch up(cmd) {
		case 'M':
			first := true
			for {
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs && hasStart {
					x += curX
					y += curY
				}
				curX, curY = x, y
				if first {
					hasStart = true
					startX, startY = curX, curY
					moveTo(curX, curY)
					first = false
				} else {
					lineTo(curX, curY)
				}
				if !sc.more() {
					break
				}
			}
		case 'L':
			if !hasStart {
				return nil, false
			}
			for {
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					x += curX
					y += curY
				}
				curX, curY = x, y
				lineTo(curX, curY)
				if !sc.more() {
					break
				}
			}
		case 'H':
			if !hasStart {
				return nil, false
			}
			for {
				x := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					x += curX
				}
				curX = x
				lineTo(curX, curY)
				if !sc.more() {
					break
				}
			}
		case 'V':
			if !hasStart {
				return nil, false
			}
			for {
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					y += curY
				}
				curY = y
				lineTo(curX, curY)
				if !sc.more() {
					break
				}
			}
		case 'C':
			if !hasStart {
				return nil, false
			}
			for {
				c1x := sc.number()
				c1y := sc.number()
				c2x := sc.number()
				c2y := sc.number()
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					c1x += curX
					c1y += curY
					c2x += curX
					c2y += curY
					x += curX
					y += curY
				}
				cubicTo(c1x, c1y, c2x, c2y, x, y)
				prevCX, prevCY = c2x, c2y
				curX, curY = x, y
				if !sc.more() {
					break
				}
			}
		case 'S':
			if !hasStart {
				return nil, false
			}
			for {
				c2x := sc.number()
				c2y := sc.number()
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					c2x += curX
					c2y += curY
					x += curX
					y += curY
				}
				c1x, c1y := curX, curY
				if prevCmd == 'C' || prevCmd == 'S' {
					c1x = 2*curX - prevCX
					c1y = 2*curY - prevCY
				}
				cubicTo(c1x, c1y, c2x, c2y, x, y)
				prevCX, prevCY = c2x, c2y
				curX, curY = x, y
				prevCmd = 'S'
				if !sc.more() {
					break
				}
			}
		case 'Q':
			if !hasStart {
				return nil, false
			}
			for {
				cx := sc.number()
				cy := sc.number()
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					cx += curX
					cy += curY
					x += curX
					y += curY
				}
				quadTo(cx, cy, x, y)
				prevQX, prevQY = cx, cy
				curX, curY = x, y
				if !sc.more() {
					break
				}
			}
		case 'T':
			if !hasStart {
				return nil, false
			}
			for {
				x := sc.number()
				y := sc.number()
				if sc.bad {
					return nil, false
				}
				if !abs {
					x += curX
					y += curY
				}
				cx, cy := curX, curY
				if prevCmd == 'Q' || prevCmd == 'T' {
					cx = 2*curX - prevQX
					cy = 2*curY - prevQY
				}
				quadTo(cx, cy, x, y)
				prevQX, prevQY = cx, cy
				curX, curY = x, y
				prevCmd = 'T'
				if !sc.more() {
					break
				}
			}
		case 'Z':
			path.Close()
			curX, curY = startX, startY
		}

		// Record the command family for the next smooth-curve reflection. S and
		// T set prevCmd themselves inside their loops.
		switch up(cmd) {
		case 'S', 'T':
			// already recorded
		default:
			prevCmd = up(cmd)
		}
	}

	if !emitted {
		return nil, false
	}
	return path, true
}
