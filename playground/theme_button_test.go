// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// The in-app theme button cycles System -> Light -> Dark -> System, keeps its
// label in step, and drives the host apply-hook with the newly chosen mode so the
// page can put it on data-theme.
func TestThemeButtonCycles(t *testing.T) {
	s := newTestState(t, false)

	if s.ThemeMode() != "system" {
		t.Fatalf("initial theme mode = %q, want system", s.ThemeMode())
	}
	if got := s.themeBtn.Label().Get(); got != "System" {
		t.Fatalf("initial label = %q, want %q", got, "System")
	}

	var applied []string
	s.SetThemeApply(func(mode string) { applied = append(applied, mode) })

	wantSeq := []string{"light", "dark", "system"}
	for _, want := range wantSeq {
		s.cycleTheme()
		if s.ThemeMode() != want {
			t.Fatalf("after cycle, mode = %q, want %q", s.ThemeMode(), want)
		}
		if got := s.themeBtn.Label().Get(); got != themeBtnLabel(want) {
			t.Fatalf("label = %q, want %q", got, themeBtnLabel(want))
		}
	}
	// The apply-hook saw every step, in order.
	if len(applied) != 3 || applied[0] != "light" || applied[1] != "dark" || applied[2] != "system" {
		t.Fatalf("apply-hook saw %v, want [light dark system]", applied)
	}
}

// SetThemeMode seeds the button's selection (from the host's restored localStorage
// value) and clamps an unknown value to system.
func TestThemeButtonSetMode(t *testing.T) {
	s := newTestState(t, false)

	s.SetThemeMode("dark")
	if s.ThemeMode() != "dark" || s.themeBtn.Label().Get() != "Dark" {
		t.Fatalf("SetThemeMode(dark): mode=%q label=%q", s.ThemeMode(), s.themeBtn.Label().Get())
	}
	s.SetThemeMode("bogus")
	if s.ThemeMode() != "system" {
		t.Fatalf("SetThemeMode(bogus) = %q, want system (clamped)", s.ThemeMode())
	}
}
