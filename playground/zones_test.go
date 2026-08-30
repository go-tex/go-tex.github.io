// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// TestZonesReserveHeightAndShrinkBody proves layout() reserves both bands' heights
// above the toolbar and below the status bar, shrinking the editor+render body to
// the remainder, and that the full surface is accounted for.
func TestZonesReserveHeightAndShrinkBody(t *testing.T) {
	s := newTestState(t, false)

	if s.topZoneH <= 0 {
		t.Fatalf("topZoneH = %d, want > 0", s.topZoneH)
	}
	// The footer band is empty now, so it reserves no height and the body claims
	// the space it used to take.
	if s.bottomZoneH != 0 {
		t.Fatalf("bottomZoneH = %d, want 0 (the footer band is empty)", s.bottomZoneH)
	}
	// The toolbar begins at the topZone height, and the body begins below the
	// toolbar (bodyTop).
	if got, want := s.bodyTop(), s.topZoneH+s.toolbarH; got != want {
		t.Fatalf("bodyTop() = %d, want %d", got, want)
	}
	// topZone band, toolbar, body, status bar and bottomZone tile the whole height.
	body := s.paned.Bounds()
	if body.Y != s.bodyTop() {
		t.Fatalf("body Y = %d, want bodyTop %d", body.Y, s.bodyTop())
	}
	sum := s.topZoneH + s.toolbarH + body.H + s.statusH + s.bottomZoneH
	if sum != testH {
		t.Fatalf("bands sum to %d, want the surface height %d", sum, testH)
	}
	// The topZone band sits at the very top and the bottomZone at the very bottom.
	if s.topZone.bounds.Y != 0 || s.topZone.bounds.H != s.topZoneH {
		t.Fatalf("topZone bounds = %+v, want Y0 H%d", s.topZone.bounds, s.topZoneH)
	}
	if bz := s.bottomZone.bounds; bz.Y+bz.H != testH {
		t.Fatalf("bottomZone bottom = %d, want %d", bz.Y+bz.H, testH)
	}
}

// TestBottomZoneIsEmpty proves the footer band carries no content now: no
// paragraphs, no links, and zero measured height.
func TestBottomZoneIsEmpty(t *testing.T) {
	s := newTestState(t, false)
	if got := len(s.bottomZone.paragraphs()); got != 0 {
		t.Fatalf("footer paragraphs = %d, want 0", got)
	}
	if got := len(s.bottomZone.links); got != 0 {
		t.Fatalf("footer links = %d, want 0", got)
	}
	if h := s.bottomZone.measure(testW); h != 0 {
		t.Fatalf("empty footer measure = %d, want 0", h)
	}
}

// TestBottomZoneNonLinkClickIsInert proves a click inside the band but off every
// link neither navigates nor is consumed by the band.
func TestBottomZoneNonLinkClickIsInert(t *testing.T) {
	s := newTestState(t, false)
	navigated := false
	s.SetNavigate(func(string) { navigated = true })

	// The band's very top-left corner is padding — no link sits there.
	bz := s.bottomZone.bounds
	if s.bottomZone.handleClick(bz.X, bz.Y) {
		t.Fatal("a click on empty band padding must not be consumed by handleClick")
	}
	if navigated {
		t.Fatal("a click off every link must not navigate")
	}
}

// TestDoNavigateWithoutHook proves the native path (no navigate hook installed) is
// a safe no-op: doNavigate neither panics nor errors, it just marks dirty.
func TestDoNavigateWithoutHook(t *testing.T) {
	s := newTestState(t, false)
	s.navigate = nil // native build: no host hook
	s.ClearDirty()
	s.doNavigate(engineURL)
	if !s.Dirty() {
		t.Fatal("doNavigate should mark the scene dirty even with no hook")
	}
}

// TestSetSiteRootUpdatesRoot proves SetSiteRoot updates the stored root and ignores
// a blank value.
func TestSetSiteRootUpdatesRoot(t *testing.T) {
	s := newTestState(t, false)

	s.SetSiteRoot("") // ignored
	if s.siteRoot != defaultSiteRoot {
		t.Fatalf("blank SetSiteRoot changed the root to %q", s.siteRoot)
	}

	const custom = "https://example.test/"
	s.SetSiteRoot(custom)
	if s.siteRoot != custom {
		t.Fatalf("SetSiteRoot did not update the root; got %q, want %q", s.siteRoot, custom)
	}
}

// TestTopZoneStatusLinePaints proves the topZone status band paints its brand
// lockup (left) and its ready dot (right of the brand) over the surface.
func TestTopZoneStatusLinePaints(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	// The brand tile's indigo must appear near the left edge of the band.
	sawBrand := false
	for y := 0; y < s.topZoneH; y++ {
		for x := 0; x < 40; x++ {
			if samplePixel(buf, x, y) == brandIndigo {
				sawBrand = true
			}
		}
	}
	if !sawBrand {
		t.Fatal("the topZone brand tile did not paint")
	}
	// The ready dot's green must appear in the band, to the right of the brand.
	sawDot := false
	for y := 0; y < s.topZoneH; y++ {
		for x := 0; x < testW; x++ {
			if samplePixel(buf, x, y) == readyDotColor {
				sawDot = true
			}
		}
	}
	if !sawDot {
		t.Fatal("the topZone ready dot did not paint")
	}
	// The band must not be entirely the page background (the status label + ground
	// paint over it).
	band := make([]byte, 0)
	for y := 0; y < s.topZoneH; y++ {
		for x := 0; x < testW; x++ {
			band = append(band, buf[(y*testW+x)*4:(y*testW+x)*4+4]...)
		}
	}
	if !nonBlank(band, s.theme.Background) {
		t.Fatal("the topZone band is entirely the page background")
	}
}

// TestZonesDrawZeroBoundsAreNoops covers the early-return guards when a band has no
// area (a degenerate surface): draw must not touch the painter or panic.
func TestZonesDrawZeroBoundsAreNoops(t *testing.T) {
	s := newTestState(t, false)
	buf := make([]byte, testW*testH*4)
	p := painter.NewPixelPainter(buf, testW, testH)

	s.topZone.setBounds(toolkit.Rect{})
	s.topZone.draw(p, s.theme)
	s.bottomZone.bounds = toolkit.Rect{}
	s.bottomZone.draw(p, s.theme)

	for _, b := range buf {
		if b != 0 {
			t.Fatal("a zero-bounds band draw painted pixels")
		}
	}
}

// TestZoneIntrospectionAccessors covers the host/harness accessors the gotexZones
// hook surfaces: the topZone status + rect, the bottomZone rect, the parallel link
// url/rect slices and the ready-dot colour.
func TestZoneIntrospectionAccessors(t *testing.T) {
	s := newTestState(t, false)

	if s.TopZoneStatusText() != topZoneStatus {
		t.Fatalf("TopZoneStatusText = %q, want %q", s.TopZoneStatusText(), topZoneStatus)
	}
	if tr := s.TopZoneRect(); tr != [4]int{0, 0, testW, s.topZoneH} {
		t.Fatalf("TopZoneRect = %v", tr)
	}
	// The footer band is empty and collapsed: zero height, no links.
	if br := s.BottomZoneRect(); br[3] != s.bottomZoneH || s.bottomZoneH != 0 {
		t.Fatalf("BottomZoneRect = %v, bottomZoneH = %d (want a zero-height band)", br, s.bottomZoneH)
	}
	if urls, rects := s.BottomZoneLinkURLs(), s.BottomZoneLinkRects(); len(urls) != 0 || len(rects) != 0 {
		t.Fatalf("link urls=%d rects=%d, want 0 each", len(urls), len(rects))
	}
	if rgb := s.ReadyDotRGB(); rgb != [3]int{int(readyDotColor.R), int(readyDotColor.G), int(readyDotColor.B)} {
		t.Fatalf("ReadyDotRGB = %v", rgb)
	}
}

// TestBottomZoneHandleMoveBeforeLayout covers the guard in handleMove for a band
// whose link-widget pool has not been grown yet (links present, no widgets): the
// move is a safe no-op reporting no change.
func TestBottomZoneHandleMoveBeforeLayout(t *testing.T) {
	z := &bottomZone{links: []zoneLink{{rect: toolkit.Rect{W: 10, H: 10}}}}
	if z.handleMove(1, 1) {
		t.Fatal("handleMove with no link widgets yet must report no change")
	}
}

// TestWrapTokensWordWiderThanMax proves a token wider than the line budget still
// gets its own line unbroken, and that ordinary tokens pack greedily.
func TestWrapTokensWordWiderThanMax(t *testing.T) {
	SetupText(1)
	toks := []zoneToken{{text: "aaaaaaaaaaaaaaaaaaaa"}, {text: "b"}, {text: "c"}}
	// A budget smaller than the first word forces it onto its own line; the next
	// two words then pack onto the following line.
	lines := wrapTokens(toks, 1, toolkit.TextWidth(" "))
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want the over-wide word broken onto its own line", len(lines))
	}
	if len(lines[0]) != 1 || lines[0][0].text != "aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("first line = %+v, want the lone over-wide word", lines[0])
	}
}

// TestBottomZoneMeasureEmptyCollapses proves an empty footer measures to zero
// height at any width and places no links.
func TestBottomZoneMeasureEmptyCollapses(t *testing.T) {
	s := newTestState(t, false)
	if h := s.bottomZone.measure(1); h != 0 {
		t.Fatalf("empty measure(1) = %d, want 0", h)
	}
	s.bottomZone.place(toolkit.Rect{X: 0, Y: 0, W: 1, H: 0})
	if len(s.bottomZone.links) != 0 {
		t.Fatalf("got %d links from an empty footer, want 0", len(s.bottomZone.links))
	}
}
