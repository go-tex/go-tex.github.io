// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "github.com/go-widgets/toolkit"

// This file holds the small vector glyphs the toolbar buttons carry as their
// optional LeadingIcon (a go-widgets/toolkit Button MVVM adornment). They are
// FILL-based single-path SVGs on a 24×24 grid so toolkit.SVGIcon recolours them
// with the button's current ink (stroke-based icons do not recolour, and oksvg
// does not scale stroke width) — see [toolkit.SVGIcon].
//
// Each drawer is built once at startup and set on its button; the button paints
// it just before the caption.

// svgDoc wraps a 24×24 fill=currentColor path into a full SVG document.
func svgDoc(path string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + path + `</svg>`
}

var (
	// a magnifier with a see-through ring (even-odd) and a handle.
	iconFind = toolkit.SVGIcon(svgDoc(
		`<path fill-rule="evenodd" d="M10 3a7 7 0 1 0 4.2 12.6l4.1 4.1 1.4-1.4-4.1-4.1A7 7 0 0 0 10 3zm0 2a5 5 0 1 1 0 10 5 5 0 0 1 0-10z"/>`))
	// left-filled circle = light/dark contrast.
	iconTheme = toolkit.SVGIcon(svgDoc(
		`<path fill-rule="evenodd" d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zm0 2v14a7 7 0 0 1 0-14z"/>`))
	// two chevrons = source / code view.
	iconSource = toolkit.SVGIcon(svgDoc(
		`<path d="M9.4 7.4 4.8 12l4.6 4.6-1.4 1.4L2 12l6-6zM14.6 7.4 19.2 12l-4.6 4.6 1.4 1.4L22 12l-6-6z"/>`))
	// a panel with a left rail = workspace sidebar.
	iconWorkspace = toolkit.SVGIcon(svgDoc(
		`<path fill-rule="evenodd" d="M3 4h18v16H3V4zm2 2v12h3V6H5zm5 0v12h9V6h-9z"/>`))
	// stacked bars = the minimap.
	iconMinimap = toolkit.SVGIcon(svgDoc(
		`<path d="M4 5h16v2H4zM4 9h12v2H4zM4 13h16v2H4zM4 17h9v2H4z"/>`))
)
