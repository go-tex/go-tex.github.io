package playground

import "testing"

// ⛔ The boot clone is the FIRST request on the worker's single request goroutine,
// so a host that drives the panel itself must be able to decline it — otherwise a
// boot clone that hangs starves the clone the host actually asked for, and the
// symptom is an origin recording that not one request arrived while the worker
// looks perfectly alive (#212).
func TestDeclineBootCloneStopsTheBootClone(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}sample\end{document}`

	s.DeclineBootClone()
	var called bool
	s.BootClone(func(err error) {
		called = true
		if err != nil {
			t.Errorf("a declined boot clone must not report an error, got %v", err)
		}
	})

	// ⛔ It must still CALL BACK. The host's callback renders, and a boot path that
	// silently never answers would leave a page waiting on a frame that never comes
	// — replacing a network hang with a quieter one.
	if !called {
		t.Error("a declined boot clone never called its done callback")
	}
	if f.cloneCalls != 0 {
		t.Errorf("the boot clone ran %d time(s) although it was declined", f.cloneCalls)
	}
	if f.restoreCalls != 0 {
		t.Errorf("a declined boot clone still tried to restore %d time(s)", f.restoreCalls)
	}
	if s.BootNotice() != "" {
		t.Errorf("a declined boot clone put a notice on the page: %q", s.BootNotice())
	}
}

// And the default is unchanged: a host that says nothing still gets the samples.
// Without this, "declined" could become the behaviour for everyone and no test
// here would notice.
func TestBootCloneStillRunsWhenNotDeclined(t *testing.T) {
	s, f, _ := withGit(t)
	f.restoreFound = false
	f.files = []string{"article.tex"}
	f.fileData["article.tex"] = `\documentclass{article}\begin{document}sample\end{document}`

	s.BootClone(func(error) {})
	if f.cloneCalls == 0 {
		t.Error("the boot clone did not run although nothing declined it")
	}
}

// An explicit clone must still work after declining: the flag is about START-UP,
// not about disabling git. Confusing the two would turn the proofs' own subject
// off and leave them passing for the wrong reason.
func TestDecliningTheBootCloneLeavesExplicitClonesAlone(t *testing.T) {
	s, f, _ := withGit(t)
	s.DeclineBootClone()
	s.BootClone(func(error) {})
	if f.cloneCalls != 0 {
		t.Fatalf("boot clone ran despite being declined")
	}

	f.files = []string{"main.tex"}
	f.fileData["main.tex"] = `\documentclass{article}\begin{document}explicit\end{document}`
	s.GitClone(nil)
	if f.cloneCalls != 1 {
		t.Errorf("an explicit clone after declining the boot one ran %d time(s), want 1", f.cloneCalls)
	}
}
