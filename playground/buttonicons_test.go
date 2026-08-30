// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// Every toolbar button carries a leading icon (set through the toolkit Button's
// MVVM LeadingIcon adornment), and the Find button also carries a shortcut hint
// the wasm shell fills in per platform.
func TestToolbarButtonsHaveIcons(t *testing.T) {
	s := newTestState(t, false)

	for name, get := range map[string]func() bool{
		"minimap":   func() bool { return s.minimapBtn.LeadingIcon().Get() != nil },
		"workspace": func() bool { return s.sidebarBtn.LeadingIcon().Get() != nil },
		"find":      func() bool { return s.findBtn.LeadingIcon().Get() != nil },
		"source":    func() bool { return s.wysiwygBtn.LeadingIcon().Get() != nil },
		"theme":     func() bool { return s.themeBtn.LeadingIcon().Get() != nil },
	} {
		if !get() {
			t.Errorf("%s button has no leading icon", name)
		}
	}

	// The Find shortcut hint is set through the button's MVVM Shortcut adornment.
	if s.findBtn.Shortcut().Get() != "" {
		t.Fatalf("find shortcut should start empty, got %q", s.findBtn.Shortcut().Get())
	}
	s.SetFindShortcut("⌘F")
	if got := s.findBtn.Shortcut().Get(); got != "⌘F" {
		t.Errorf("find shortcut = %q, want ⌘F", got)
	}
}

// The git panel buttons carry action glyphs too (created lazily by role).
func TestGitButtonsHaveIcons(t *testing.T) {
	s := newTestState(t, false)
	for _, role := range []gitRole{gitRoleClone, gitRolePull, gitRoleCommit, gitRolePush} {
		if s.git.btn(role, 0).LeadingIcon().Get() == nil {
			t.Errorf("git button role %d has no leading icon", role)
		}
	}
}
