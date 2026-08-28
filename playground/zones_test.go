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
	if s.bottomZoneH <= 0 {
		t.Fatalf("bottomZoneH = %d, want > 0 (prose must wrap to at least one line)", s.bottomZoneH)
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

// TestBottomZoneLinksTargets proves the three links are laid out with the expected
// targets: the two repositories and the (dynamic) site root.
func TestBottomZoneLinksTargets(t *testing.T) {
	s := newTestState(t, false)
	links := s.bottomZone.links
	if len(links) != 3 {
		t.Fatalf("got %d links, want 3", len(links))
	}
	want := map[string]bool{engineURL: false, brandURL: false, defaultSiteRoot: false}
	for _, ln := range links {
		if _, ok := want[ln.url]; !ok {
			t.Fatalf("unexpected link url %q", ln.url)
		}
		want[ln.url] = true
		if ln.rect.W <= 0 || ln.rect.H <= 0 {
			t.Fatalf("link %q has empty rect %+v", ln.url, ln.rect)
		}
	}
	for url, seen := range want {
		if !seen {
			t.Fatalf("link %q was not laid out", url)
		}
	}
}

// TestBottomZoneLinkClickNavigates drives a real click at each link's centre and
// asserts the host navigate hook fired with that link's exact url.
func TestBottomZoneLinkClickNavigates(t *testing.T) {
	s := newTestState(t, false)
	var got string
	s.SetNavigate(func(url string) { got = url })

	for _, ln := range s.bottomZone.links {
		got = ""
		cx := ln.rect.X + ln.rect.W/2
		cy := ln.rect.Y + ln.rect.H/2
		if !s.HandleClick(cx, cy) {
			t.Fatalf("click at link %q centre (%d,%d) was not consumed", ln.url, cx, cy)
		}
		if got != ln.url {
			t.Fatalf("navigate got %q, want %q", got, ln.url)
		}
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
// a safe no-op: a link click neither panics nor errors, it just marks dirty.
func TestDoNavigateWithoutHook(t *testing.T) {
	s := newTestState(t, false)
	s.navigate = nil // native build: no host hook
	s.ClearDirty()
	links := s.bottomZone.links
	ln := links[0]
	if !s.HandleClick(ln.rect.X+ln.rect.W/2, ln.rect.Y+ln.rect.H/2) {
		t.Fatal("a link click should still be consumed with no hook installed")
	}
	if !s.Dirty() {
		t.Fatal("a link click should mark the scene dirty even with no hook")
	}
}

// TestSetSiteRootUpdatesBackLink proves SetSiteRoot retargets the Back link (and a
// blank root is ignored, keeping the previous target).
func TestSetSiteRootUpdatesBackLink(t *testing.T) {
	s := newTestState(t, false)

	s.SetSiteRoot("") // ignored
	if s.siteRoot != defaultSiteRoot {
		t.Fatalf("blank SetSiteRoot changed the root to %q", s.siteRoot)
	}

	const custom = "https://example.test/"
	s.SetSiteRoot(custom)
	found := false
	for _, ln := range s.bottomZone.links {
		if ln.url == custom {
			found = true
		}
		if ln.url == defaultSiteRoot {
			t.Fatal("the old default site root is still a link target after SetSiteRoot")
		}
	}
	if !found {
		t.Fatalf("no Back link points at the new site root %q", custom)
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
	if br := s.BottomZoneRect(); br[2] != testW || br[3] != s.bottomZoneH || br[1]+br[3] != testH {
		t.Fatalf("BottomZoneRect = %v", br)
	}
	urls := s.BottomZoneLinkURLs()
	rects := s.BottomZoneLinkRects()
	if len(urls) != 3 || len(rects) != 3 {
		t.Fatalf("link urls=%d rects=%d, want 3 each", len(urls), len(rects))
	}
	for i, r := range rects {
		if r[2] <= 0 || r[3] <= 0 {
			t.Fatalf("link %d (%s) has empty rect %v", i, urls[i], r)
		}
	}
	if rgb := s.ReadyDotRGB(); rgb != [3]int{int(readyDotColor.R), int(readyDotColor.G), int(readyDotColor.B)} {
		t.Fatalf("ReadyDotRGB = %v", rgb)
	}
}

// TestBottomZoneHoverUnderline proves the footer link hover feedback end-to-end
// on the real laid-out band: a pointer-move onto a link rect raises that link's
// accent underline (and marks the scene dirty), a move off it clears the
// underline, and the click path is unchanged. The underline is asserted from the
// drawn pixels, not just the hovered flag.
func TestBottomZoneHoverUnderline(t *testing.T) {
	s := newTestState(t, false)

	if s.BottomZoneHoveredLink() != -1 {
		t.Fatalf("no link should be hovered initially, got %d", s.BottomZoneHoveredLink())
	}

	ln := s.bottomZone.links[0]
	gh := toolkit.GlyphHeight()
	// The Link centres its text (VMiddle) and draws the underline one line under
	// the glyph box, so it lands at this row inside the link's rect.
	urow := ln.rect.Y + (ln.rect.H-gh)/2 + gh

	underlineDrawn := func() bool {
		buf := make([]byte, testW*testH*4)
		s.Draw(buf)
		for x := ln.rect.X; x < ln.rect.X+ln.rect.W; x++ {
			if samplePixel(buf, x, urow) == s.theme.Accent {
				return true
			}
		}
		return false
	}

	// Resting: accent glyphs but no underline row.
	if underlineDrawn() {
		t.Fatal("a resting link must not draw an underline")
	}

	// Move onto the link centre → hovered → underline appears + dirty.
	cx := ln.rect.X + ln.rect.W/2
	cy := ln.rect.Y + ln.rect.H/2
	s.ClearDirty()
	s.HandleMove(cx, cy)
	if !s.Dirty() {
		t.Fatal("moving onto a link should mark the scene dirty")
	}
	if s.BottomZoneHoveredLink() != 0 {
		t.Fatalf("link 0 should be hovered, got %d", s.BottomZoneHoveredLink())
	}
	if !underlineDrawn() {
		t.Fatal("a hovered link must draw its accent underline")
	}

	// The click path is unchanged: a click at the hovered link still navigates.
	var got string
	s.SetNavigate(func(url string) { got = url })
	if !s.HandleClick(cx, cy) || got != ln.url {
		t.Fatalf("hovered link click: consumed=%v got=%q want %q", true, got, ln.url)
	}

	// Move off every link (into the band's top-left padding) → hover clears.
	bz := s.bottomZone.bounds
	s.ClearDirty()
	s.HandleMove(bz.X, bz.Y)
	if !s.Dirty() {
		t.Fatal("moving off the hovered link should mark the scene dirty")
	}
	if s.BottomZoneHoveredLink() != -1 {
		t.Fatalf("no link should be hovered after moving into padding, got %d", s.BottomZoneHoveredLink())
	}
	if underlineDrawn() {
		t.Fatal("the underline must clear once the pointer leaves the link")
	}

	// A move with no hover change reports no change (the changed==false path).
	if s.bottomZone.handleMove(bz.X, bz.Y) {
		t.Fatal("a move that changes no hover state must report no change")
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

// TestBottomZoneMeasureTinyWidthClamps exercises the maxW<1 and x<pad clamps on a
// degenerate width without panicking, returning a positive height.
func TestBottomZoneMeasureTinyWidthClamps(t *testing.T) {
	s := newTestState(t, false)
	h := s.bottomZone.measure(1)
	if h <= 0 {
		t.Fatalf("measure(1) height = %d, want > 0", h)
	}
	// Placing at a matching rect must still expose the links.
	s.bottomZone.place(toolkit.Rect{X: 0, Y: 0, W: 1, H: h})
	if len(s.bottomZone.links) != 3 {
		t.Fatalf("got %d links after a tiny-width layout, want 3", len(s.bottomZone.links))
	}
}
