// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// TestBuildInfoDefault proves an un-stamped (native/dev) build renders the honest
// "dev · unknown" placeholder in the last status-bar segment, and that a compile
// (which rewrites segments 0..3 through updateStatus) leaves the stamp untouched.
func TestBuildInfoDefault(t *testing.T) {
	s := newTestState(t, false)

	if got := s.status.Segments[buildInfoSegment]; got != defaultBuildInfo {
		t.Fatalf("default build-info segment = %q, want %q", got, defaultBuildInfo)
	}
	if got := s.buildInfo; got != defaultBuildInfo {
		t.Fatalf("default s.buildInfo = %q, want %q", got, defaultBuildInfo)
	}

	// A recompile refreshes the caret/encoding/page/issue segments; the build
	// stamp must survive it (updateStatus owns 0..3 only).
	s.Compile()
	if got := s.status.Segments[buildInfoSegment]; got != defaultBuildInfo {
		t.Fatalf("build-info segment after Compile = %q, want it preserved as %q", got, defaultBuildInfo)
	}
}

// TestSetBuildInfoInjected proves the wasm shell's set-once seam replaces the
// placeholder with the ldflags-injected SHA + UTC timestamp, marks the frame
// dirty, and (again) survives a later compile — the property that makes a SHA on
// screen correspond to the wasm actually running.
func TestSetBuildInfoInjected(t *testing.T) {
	s := newTestState(t, false)

	// Clear the dirty flag first (Draw clears it) so we can prove SetBuildInfo
	// re-sets it — the repaint that makes the new stamp appear.
	buf := make([]byte, 4*testW*testH)
	s.Draw(buf)
	if s.Dirty() {
		t.Fatalf("Draw should have cleared the dirty flag")
	}

	const sha, ts = "4d63d59", "2026-08-25 13:20 UTC"
	s.SetBuildInfo(sha, ts)

	want := sha + " · " + ts
	if got := s.status.Segments[buildInfoSegment]; got != want {
		t.Fatalf("injected build-info segment = %q, want %q", got, want)
	}
	if !s.Dirty() {
		t.Fatalf("SetBuildInfo should mark the frame dirty so the new stamp repaints")
	}

	// The injected stamp also survives a recompile.
	s.Compile()
	if got := s.status.Segments[buildInfoSegment]; got != want {
		t.Fatalf("injected build-info after Compile = %q, want %q", got, want)
	}

	// And it actually paints: rendering the scene must touch the status region.
	s.Draw(buf)
	if !nonBlank(buf, s.theme.Background) {
		t.Fatalf("Draw with build info produced a blank frame")
	}
}

// TestFormatBuildInfo covers the fully-specified case and both half-injected
// fallbacks (empty version, empty time), so a build that stamped only one field
// still reads sensibly instead of showing a dangling separator.
func TestFormatBuildInfo(t *testing.T) {
	cases := []struct {
		version, buildTime, want string
	}{
		{"4d63d59", "2026-08-25 13:20 UTC", "4d63d59 · 2026-08-25 13:20 UTC"},
		{"", "2026-08-25 13:20 UTC", "dev · 2026-08-25 13:20 UTC"},
		{"4d63d59", "", "4d63d59 · unknown"},
		{"", "", defaultBuildInfo},
	}
	for _, c := range cases {
		if got := formatBuildInfo(c.version, c.buildTime); got != c.want {
			t.Fatalf("formatBuildInfo(%q, %q) = %q, want %q", c.version, c.buildTime, got, c.want)
		}
	}
	// The dev fallback and the constant must agree (one honest placeholder).
	if !strings.Contains(defaultBuildInfo, "dev") || !strings.Contains(defaultBuildInfo, "unknown") {
		t.Fatalf("defaultBuildInfo = %q, want it to name dev + unknown", defaultBuildInfo)
	}
}
