// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground_test

import (
	"os"
	"strings"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/proofdir"
)

// shotPath is where a browser proof writes its screenshot: somewhere durable,
// and never inside a repository. See internal/proofdir for why.
func shotPath(t testing.TB, name string) string {
	t.Helper()
	p, err := proofdir.Path(name)
	if err != nil {
		t.Fatalf("screenshot path: %v", err)
	}
	// Logged, because a proof nobody can find is a proof nobody reads.
	t.Logf("screenshot: %s", p)
	return p
}

// ⛔ THE REFUSAL IS EXERCISED, not just the acceptance. A guard whose failing
// branch never runs is not known to work — and this one exists to stop a
// screenshot reaching a public repository, which is not a failure anybody gets
// to see before it has happened.
func TestProofDirRefusesADirectoryInsideARepository(t *testing.T) {
	// This package's own directory is inside a work tree, and is exactly the
	// default ("." ) that the thirteen copied snippets used.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if root := proofdir.RepoRootOf(wd); root == "" {
		t.Skipf("%s is not inside a repository, so there is nothing to refuse", wd)
	}
	if got, err := proofdir.Choose(wd); err == nil {
		t.Fatalf("a directory inside this repository was accepted for screenshots: %s", got)
	}
}

// And what it DOES choose is outside every repository, which is the other half
// of the same claim: a guard that refuses everything would also pass the test
// above.
func TestProofDirIsOutsideAnyRepositoryAndSurvivesTheRun(t *testing.T) {
	dir, err := proofdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if root := proofdir.RepoRootOf(dir); root != "" {
		t.Fatalf("proofs would be written inside the repository at %s (dir %s)", root, dir)
	}
	// Not a temporary directory either: these are written to be looked at after
	// the run, and one the framework removes on exit would be gone first.
	if tmp := os.TempDir(); tmp != "" && strings.HasPrefix(dir, tmp) {
		t.Errorf("proofs go to a temporary directory (%s)", dir)
	}
}
