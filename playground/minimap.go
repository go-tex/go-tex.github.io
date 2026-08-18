// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// minimap is a VS Code-style code overview: a scaled-down thumbnail of the
// actual buffer where each source line is a row of tiny coloured segments — one
// short segment per run of non-space characters, coloured by the syntax token
// under it (reusing the very spans the editor paints), with leading whitespace
// left as a blank gap so indentation reads. A translucent accent band marks the
// currently-visible line range. It is a scroll thumbnail: the host maps a
// click/drag on it back to a buffer line and scrolls the editor there (see
// State.minimapScrollTo).
//
// It is draw-only: State refreshes lines/spans/top/visible before each paint and
// owns the pointer mapping, so the widget stays a passive overview.
//
// Rows are a FIXED small height laid CONTIGUOUSLY from the top, exactly like the
// VS Code minimap: a short file sits as a compact block at the top with blank
// space below (it does not stretch to fill the column). Only when the buffer is
// taller than the widget (n*rowH > height) does it compress — sampling one line
// per drawn row — so the overview never spills past its bounds.
type minimap struct {
	toolkit.Base

	lines   []string             // reference to the editor buffer lines
	spans   [][]toolkit.TextSpan // per-line syntax spans (may be shorter/nil)
	top     int                  // first visible buffer line (editor ScrollLine)
	visible int                  // number of lines visible in the editor viewport
}

// update refreshes the overview inputs before a paint. spans is the per-line
// highlighter output (the editor's own colours); a nil or short spans slice
// falls back to a neutral ink for the uncovered rows.
func (m *minimap) update(lines []string, spans [][]toolkit.TextSpan, top, visible int) {
	m.lines, m.spans, m.top, m.visible = lines, spans, top, visible
}

// metrics returns the fixed row height and the number of rows actually drawn:
// one row per source line when the buffer fits, otherwise as many rows as fit
// the height (the buffer is then sampled, one line per row). displayRows is 0
// only when there is no area or no lines.
func (m *minimap) metrics() (rowH, displayRows int) {
	r := m.Bounds()
	n := len(m.lines)
	if r.H <= 0 || n == 0 {
		return 0, 0
	}
	rowH = m.rowH()
	maxRows := r.H / rowH
	if maxRows < 1 {
		maxRows = 1
	}
	displayRows = n
	if displayRows > maxRows {
		displayRows = maxRows
	}
	return rowH, displayRows
}

// lineForRow maps a drawn row index to its 0-based source line. It is the
// identity when the buffer fits (displayRows == n) and a proportional sample
// when it does not.
func lineForRow(row, displayRows, n int) int {
	line := row * n / displayRows
	if line >= n {
		line = n - 1
	}
	return line
}

// lineAtY maps a widget-local y (device pixels) to a 0-based buffer line, in the
// fixed-row-height geometry: rows above the drawn block clamp to line 0, rows in
// or below it clamp to the last drawn row's line.
func (m *minimap) lineAtY(y int) int {
	r := m.Bounds()
	n := len(m.lines)
	rowH, displayRows := m.metrics()
	if displayRows == 0 {
		return 0
	}
	rel := y - r.Y
	if rel < 0 {
		rel = 0
	}
	row := rel / rowH
	if row >= displayRows {
		row = displayRows - 1
	}
	return lineForRow(row, displayRows, n)
}

// charW is the device width of one source character cell in the overview, and
// rowH the device height of one line's row. Both are metric-scaled and floored
// at one device pixel so the overview never collapses to nothing.
func (m *minimap) charW() int { return atLeast1(toolkit.Scaled(1)) }
func (m *minimap) rowH() int  { return atLeast1(toolkit.Scaled(3)) }

// atLeast1 floors v at one device pixel.
func atLeast1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

// isSpace reports whether r is a horizontal-space rune (space or tab), which the
// overview renders as a gap so indentation and word structure survive.
func isSpace(r rune) bool { return r == ' ' || r == '\t' }

// spanColorAt returns the colour of the span covering rune index idx, or the
// fallback ink when no span does (a plain, unlexed run).
func spanColorAt(spans []toolkit.TextSpan, idx int, fallback toolkit.RGBA) toolkit.RGBA {
	for _, s := range spans {
		if idx >= s.Start && idx < s.End {
			return s.Color
		}
	}
	return fallback
}

// forEachSegment walks the whole overview and calls cb with the device rectangle
// and colour of every token segment it would paint, so Draw (which paints) and
// segmentCount (which tallies) share one geometry. It is a no-op when the widget
// has no area or no lines.
func (m *minimap) forEachSegment(neutral toolkit.RGBA, cb func(rect toolkit.Rect, c toolkit.RGBA)) {
	r := m.Bounds()
	n := len(m.lines)
	rowH, displayRows := m.metrics()
	if r.W <= 0 || displayRows == 0 {
		return
	}
	charW := m.charW()
	pad := toolkit.Scaled(2)
	usableW := r.W - 2*pad
	if usableW < 1 {
		usableW = 1
	}
	maxCols := usableW / charW
	if maxCols < 1 {
		maxCols = 1
	}
	segH := atLeast1(rowH - toolkit.Scaled(1)) // a 1px gap between rows
	for row := 0; row < displayRows; row++ {
		i := lineForRow(row, displayRows, n)
		y := r.Y + row*rowH

		runes := []rune(m.lines[i])
		cols := len(runes)
		if cols > maxCols {
			cols = maxCols
		}
		var spans []toolkit.TextSpan
		if i < len(m.spans) {
			spans = m.spans[i]
		}
		j := 0
		for j < cols {
			if isSpace(runes[j]) {
				j++ // gap (leading indentation and inter-word spaces alike)
				continue
			}
			start := j
			col := spanColorAt(spans, j, neutral)
			for j < cols && !isSpace(runes[j]) && spanColorAt(spans, j, neutral) == col {
				j++
			}
			seg := toolkit.Rect{X: r.X + pad + start*charW, Y: y, W: (j - start) * charW, H: segH}
			cb(seg, col)
		}
	}
}

// segmentCount is the number of token segments the overview paints for the
// current buffer — the introspection hook the headless harness reads to prove
// the minimap draws multi-token rows (not one solid bar per line).
func (m *minimap) segmentCount(neutral toolkit.RGBA) int {
	n := 0
	m.forEachSegment(neutral, func(toolkit.Rect, toolkit.RGBA) { n++ })
	return n
}

// Draw paints the overview and the viewport indicator.
func (m *minimap) Draw(p painter.Painter, theme *toolkit.Theme) {
	r := m.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	p.FillRect(r, theme.SurfaceAlt)
	p.FillRect(toolkit.Rect{X: r.X, Y: r.Y, W: toolkit.Scaled(1), H: r.H}, theme.Border) // left divider

	n := len(m.lines)
	if n == 0 {
		return
	}

	m.forEachSegment(theme.OnSurface, func(seg toolkit.Rect, c toolkit.RGBA) {
		p.FillRect(seg, c)
	})

	// Viewport indicator: a translucent accent band over the visible range,
	// mapped through the SAME fixed-row geometry so it lines up with the rows.
	if m.visible > 0 {
		rowH, displayRows := m.metrics()
		contentH := displayRows * rowH
		startRow := rowForLine(m.top, displayRows, n)
		end := m.top + m.visible
		if end > n {
			end = n
		}
		endRow := rowForLine(end, displayRows, n)
		vy := r.Y + startRow*rowH
		vh := (endRow - startRow) * rowH
		if vh < toolkit.Scaled(6) {
			vh = toolkit.Scaled(6)
		}
		if (vy-r.Y)+vh > contentH {
			vh = contentH - (vy - r.Y)
		}
		band := toolkit.Rect{X: r.X, Y: vy, W: r.W, H: vh}
		p.FillRect(band, toolkit.RGBA{R: theme.Accent.R, G: theme.Accent.G, B: theme.Accent.B, A: 0x33})
		p.StrokeRect(band, theme.Accent, toolkit.Scaled(1))
	}
}

// rowForLine maps a 0-based source line to the drawn row it appears on — the
// inverse of lineForRow, used to place the viewport indicator over the rows.
func rowForLine(line, displayRows, n int) int {
	return line * displayRows / n
}
