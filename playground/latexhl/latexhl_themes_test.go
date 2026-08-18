// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package latexhl

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// commandColorOf highlights "\section" with h and returns the colour of
// the \section command span (rune 0).
func commandColorOf(h *Highlighter, theme *toolkit.Theme) toolkit.RGBA {
	got := h.Highlight("latex", []string{`\section`}, theme)[0]
	s, _ := spanAt(got, 0)
	return s.Color
}

func TestThemeNamesOrderAndUniqueness(t *testing.T) {
	names := ThemeNames()
	if len(names) == 0 || names[0] != "Default" {
		t.Fatalf("ThemeNames()[0] must be Default, got %#v", names)
	}
	want := []string{"Default", "Monokai", "Solarized", "GitHub", "Dracula"}
	if len(names) != len(want) {
		t.Fatalf("ThemeNames len=%d want %d (%#v)", len(names), len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ThemeNames[%d]=%q want %q", i, names[i], want[i])
		}
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("duplicate theme name %q", n)
		}
		seen[n] = true
	}
}

func TestPaletteByNameFixedSchemes(t *testing.T) {
	for _, name := range []string{"Monokai", "Solarized", "GitHub", "Dracula"} {
		p, derive := PaletteByName(name)
		if derive {
			t.Fatalf("%s: derive must be false", name)
		}
		cols := []toolkit.RGBA{p.Default, p.Comment, p.Command, p.Math, p.Delimiter, p.EnvName}
		for _, c := range cols {
			if c.A != 0xFF {
				t.Fatalf("%s: colour not opaque: %#v", name, c)
			}
		}
		if p.Comment == p.Default {
			t.Fatalf("%s: Comment must differ from Default", name)
		}
		if p.Command == p.Default {
			t.Fatalf("%s: Command must differ from Default", name)
		}
		if p.EnvName == p.Command {
			t.Fatalf("%s: EnvName must differ from Command", name)
		}
	}
}

func TestPaletteByNameDefaultAndUnknown(t *testing.T) {
	p, derive := PaletteByName("Default")
	if !derive {
		t.Fatalf("Default must be derive=true")
	}
	if p != (Palette{}) {
		t.Fatalf("Default palette must be the zero value, got %#v", p)
	}
	p, derive = PaletteByName("nope")
	if !derive {
		t.Fatalf("unknown name must fall back to derive=true")
	}
	if p != (Palette{}) {
		t.Fatalf("unknown name must return zero Palette, got %#v", p)
	}
}

func TestSetThemeMonokaiThenDefault(t *testing.T) {
	theme := toolkit.DefaultLight()
	h := New()

	if !h.SetTheme("Monokai") {
		t.Fatalf("SetTheme(Monokai) must return true")
	}
	if h.Palette == (Palette{}) {
		t.Fatalf("SetTheme(Monokai) must make Palette non-zero")
	}
	monokai, _ := PaletteByName("Monokai")
	if got := commandColorOf(h, theme); got != monokai.Command {
		t.Fatalf("Monokai Command colour: got %#v want %#v", got, monokai.Command)
	}

	if !h.SetTheme("Default") {
		t.Fatalf("SetTheme(Default) must return true")
	}
	if h.Palette != (Palette{}) {
		t.Fatalf("SetTheme(Default) must restore the zero Palette, got %#v", h.Palette)
	}
	derived := DefaultPalette(theme).Command
	got := commandColorOf(h, theme)
	if got != derived {
		t.Fatalf("after Default, Command must be theme-derived %#v, got %#v", derived, got)
	}
	if got == monokai.Command {
		t.Fatalf("after Default, Command must NOT be Monokai's %#v", monokai.Command)
	}
}

func TestSetThemeUnknownLeavesUsable(t *testing.T) {
	theme := toolkit.DefaultLight()
	h := New()
	if !h.SetTheme("Dracula") {
		t.Fatalf("SetTheme(Dracula) must return true")
	}
	dracula, _ := PaletteByName("Dracula")

	// Unknown name: returns false and leaves the palette unchanged.
	if h.SetTheme("bogus") {
		t.Fatalf("SetTheme(bogus) must return false")
	}
	if h.Palette != dracula {
		t.Fatalf("SetTheme(bogus) must leave the palette unchanged, got %#v", h.Palette)
	}
	// Still usable and still colours with the prior (Dracula) scheme.
	if got := commandColorOf(h, theme); got != dracula.Command {
		t.Fatalf("after bogus, Command must stay Dracula %#v, got %#v", dracula.Command, got)
	}
}

func TestFixedSchemeThemeIndependence(t *testing.T) {
	light := toolkit.DefaultLight()
	dark := toolkit.DefaultDark()
	h := New()

	// A fixed scheme colours identically regardless of toolkit theme.
	h.SetTheme("Monokai")
	if commandColorOf(h, light) != commandColorOf(h, dark) {
		t.Fatalf("Monokai Command must be identical under light and dark")
	}

	// The theme-derived Default differs between light and dark.
	h.SetTheme("Default")
	if commandColorOf(h, light) == commandColorOf(h, dark) {
		t.Fatalf("Default Command must differ between light and dark")
	}
}
