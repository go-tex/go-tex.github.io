// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package latexhl

import "github.com/go-widgets/toolkit"

// namedTheme is one selectable highlight scheme. A scheme is either
// theme-derived (derive==true, empty Palette — follows the toolkit
// light/dark theme via DefaultPalette) or a fixed Palette independent of
// the toolkit theme (derive==false, all six colours opaque).
type namedTheme struct {
	name    string
	palette Palette
	derive  bool
}

// themeRegistry is the ordered set of selectable highlight schemes. The
// "Default" entry is first and is theme-derived (its Palette is the zero
// value so Highlight fills it from DefaultPalette). Every other entry is
// a fixed scheme with all six colours set opaque, so it colours the same
// way regardless of the toolkit's light/dark theme.
//
// The fixed hex values are the well-known approximate scheme colours;
// each field is annotated with the scheme role it stands for.
var themeRegistry = []namedTheme{
	{
		// Theme-derived sentinel: empty Palette, follows light/dark.
		name:   "Default",
		derive: true,
	},
	{
		name:   "Monokai",
		derive: false,
		palette: Palette{
			Default:   toolkit.RGB(0xF8, 0xF8, 0xF2), // foreground off-white
			Comment:   toolkit.RGB(0x75, 0x71, 0x5E), // muted brown-grey
			Command:   toolkit.RGB(0xF9, 0x26, 0x72), // keyword pink
			Math:      toolkit.RGB(0xE6, 0xDB, 0x74), // string yellow
			Delimiter: toolkit.RGB(0xFD, 0x97, 0x1F), // punctuation orange
			EnvName:   toolkit.RGB(0xA6, 0xE2, 0x2E), // identifier green
		},
	},
	{
		name:   "Solarized",
		derive: false,
		palette: Palette{
			Default:   toolkit.RGB(0x65, 0x7B, 0x83), // base00 body text
			Comment:   toolkit.RGB(0x93, 0xA1, 0xA1), // base1 comment
			Command:   toolkit.RGB(0x26, 0x8B, 0xD2), // blue keyword
			Math:      toolkit.RGB(0x2A, 0xA1, 0x98), // cyan
			Delimiter: toolkit.RGB(0x6C, 0x71, 0xC4), // violet
			EnvName:   toolkit.RGB(0x85, 0x99, 0x00), // green
		},
	},
	{
		name:   "GitHub",
		derive: false,
		palette: Palette{
			Default:   toolkit.RGB(0x24, 0x29, 0x2E), // near-black text
			Comment:   toolkit.RGB(0x6A, 0x73, 0x7D), // grey comment
			Command:   toolkit.RGB(0xD7, 0x3A, 0x49), // keyword red
			Math:      toolkit.RGB(0x03, 0x2F, 0x62), // string dark-blue
			Delimiter: toolkit.RGB(0x00, 0x5C, 0xC5), // constant blue
			EnvName:   toolkit.RGB(0x6F, 0x42, 0xC1), // entity purple
		},
	},
	{
		name:   "Dracula",
		derive: false,
		palette: Palette{
			Default:   toolkit.RGB(0xF8, 0xF8, 0xF2), // foreground off-white
			Comment:   toolkit.RGB(0x62, 0x72, 0xA4), // muted blue-grey
			Command:   toolkit.RGB(0xFF, 0x79, 0xC6), // keyword pink
			Math:      toolkit.RGB(0xF1, 0xFA, 0x8C), // string yellow
			Delimiter: toolkit.RGB(0xFF, 0xB8, 0x6C), // orange
			EnvName:   toolkit.RGB(0x50, 0xFA, 0x7B), // green
		},
	},
}

// ThemeNames returns the selectable highlight-theme names in display
// order, with "Default" (theme-derived) first.
func ThemeNames() []string {
	names := make([]string, len(themeRegistry))
	for i, t := range themeRegistry {
		names[i] = t.name
	}
	return names
}

// PaletteByName returns the fixed Palette for a named theme. The boolean
// "derive" is true for the "Default" theme, meaning the caller should use
// the theme-derived DefaultPalette(theme) instead of a fixed palette. An
// unknown name returns derive=true (fall back to the theme-derived
// palette) with a zero Palette.
func PaletteByName(name string) (p Palette, derive bool) {
	for _, t := range themeRegistry {
		if t.name == name {
			return t.palette, t.derive
		}
	}
	return Palette{}, true
}

// SetTheme sets h.Palette from a named theme. For "Default" it sets the
// zero-value Palette (theme-derived); for a named scheme it sets that
// fixed Palette verbatim, so Highlight colours independently of the
// toolkit light/dark theme. It returns false for an unknown name and
// leaves the highlighter unchanged (still usable with its current
// palette); it returns true otherwise.
func (h *Highlighter) SetTheme(name string) bool {
	for _, t := range themeRegistry {
		if t.name == name {
			if t.derive {
				h.Palette = Palette{}
			} else {
				h.Palette = t.palette
			}
			return true
		}
	}
	return false
}
