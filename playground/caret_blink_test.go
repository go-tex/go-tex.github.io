// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// SetEditorCaretVisible toggles the editor caret (the seam the host blinks on a
// timer) and only marks the scene dirty on an actual change, so an idle blink is
// one cheap redraw per phase — not two.
func TestEditorCaretBlink(t *testing.T) {
	s := newTestState(t, false)

	if !s.EditorCaretVisible() {
		t.Fatal("caret should start visible")
	}

	s.ClearDirty()
	s.SetEditorCaretVisible(false)
	if s.EditorCaretVisible() {
		t.Fatal("caret should be hidden after SetEditorCaretVisible(false)")
	}
	if !s.Dirty() {
		t.Fatal("hiding the caret should mark the scene dirty")
	}

	// Toggling to the SAME value is a no-op: no needless repaint.
	s.ClearDirty()
	s.SetEditorCaretVisible(false)
	if s.Dirty() {
		t.Fatal("re-hiding an already-hidden caret must not mark dirty")
	}

	s.ClearDirty()
	s.SetEditorCaretVisible(true)
	if !s.EditorCaretVisible() || !s.Dirty() {
		t.Fatalf("showing the caret again: visible=%v dirty=%v", s.EditorCaretVisible(), s.Dirty())
	}
}
