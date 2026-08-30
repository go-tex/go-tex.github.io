// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// The right-pane Rendered/Log tabs carry leading glyphs via FolderTabs.Icons.
func TestRightPaneTabsHaveIcons(t *testing.T) {
	s := newTestState(t, false)
	icons := s.rightPane.tabs.Icons
	if len(icons) != 2 || icons[0] == nil || icons[1] == nil {
		t.Fatalf("right-pane tabs should have 2 non-nil icons, got %d", len(icons))
	}
}
