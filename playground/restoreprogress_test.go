// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"
	"testing"
)

// Restoring a saved workspace used to show a single frozen caption ("Reopening
// your saved workspace") for its whole wait, so a reader could not tell what it
// was doing. It now rides the same progress line a clone/pull sideband does: the
// worker emits two coarse phases (reading the saved copy, then reopening the
// repository), and until the first lands the caption carries the elapsed clock.
func TestRestoreLoadingTextNamesThePhase(t *testing.T) {
	s := newTestState(t, false)
	s.git.beginOp("Restoring") // busy, op = "Restoring"

	// Before any phase: the generic caption, plus the elapsed seconds once it has
	// been running a moment — never a static line that reads as hung.
	s.git.opElapsed = 3
	msg, caption := s.sidebar.loadingText()
	if msg != "Restoring…" {
		t.Fatalf("restore msg = %q, want %q", msg, "Restoring…")
	}
	if !strings.Contains(caption, "Reopening your saved workspace") || !strings.Contains(caption, "3s") {
		t.Fatalf("early restore caption = %q, want the generic phase plus the elapsed clock", caption)
	}

	// Each emitted phase names the exact step in flight.
	for _, phase := range []string{"Reading saved copy", "Reopening the repository"} {
		s.SetGitProgress(phase, -1)
		if _, caption = s.sidebar.loadingText(); caption != phase {
			t.Fatalf("restore caption = %q, want the emitted phase %q", caption, phase)
		}
		if msg, _ = s.sidebar.loadingText(); msg != "Restoring…" {
			t.Fatalf("restore msg = %q during phase %q, want it to stay %q", msg, phase, "Restoring…")
		}
	}
}
