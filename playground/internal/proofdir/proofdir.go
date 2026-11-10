// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package proofdir says where a browser proof's screenshot may be written and,
// the part that matters, where it may not.
//
// ⛔ EVERY PROOF USED TO DEFAULT TO ".", the working tree, and eleven of the
// resulting PNGs were TRACKED. One `go test ./...` dirtied nine of them, so
// `git status` stopped being a usable signal; `gh pr create` warned about
// uncommitted changes on branches that had not touched them, which is how a
// screenshot gets committed into a dependency bump by accident; and every
// commit that carried one added a few hundred kilobytes to a history that never
// gives them back. Deterministic rendering is no defence — anti-aliasing, a
// font fallback or a Chrome version moves bytes without moving anything a
// reader cares about.
//
// The same four lines were copy-pasted into THIRTEEN test files, each with the
// same wrong default. This package is that decision, made once.
//
// It is a package rather than a test helper because the proofs live in TWO test
// packages — the browser ones in playground_test, two others in playground —
// and a rule enforced in one of them is a rule the other can forget.
package proofdir

import (
	"path/filepath"

	"github.com/go-appdirs/outdir"
)

// Env overrides where proofs are written. It is the name the thirteen copies
// already read, so an existing CI invocation keeps working unchanged.
const Env = "GOTEX_SCREENSHOT_DIR"

// spec is this repository's answer to "where may a screenshot go", handed to
// the package that owns the question.
//
// ⛔ go-appdirs/outdir rather than a local copy, and for a measured reason: the
// hand-rolled version of this in go-macos/screencapture resolved no symbolic
// links, so a directory reached through one — which on macOS is the ordinary
// case, /tmp being a link to /private/tmp — found no work tree and was
// accepted. outdir resolves the path first and refuses it. Adopting it there
// fixed a defect rather than tidying one away; writing a third copy here would
// re-introduce whatever this one has not met yet.
func spec(want string) outdir.Spec {
	return outdir.Spec{
		App:  "go-tex-playground",
		Env:  Env,
		Sub:  "proofs",
		Want: want,
	}
}

// Dir is the directory to write proofs into, created if needed.
//
// ⛔ Not a temporary directory: the artefact exists SO THAT A PERSON CAN LOOK
// AT IT after a failure, and one the test framework removes on exit would be
// gone before anyone could. The default is the user's own application-data
// directory, which survives the test and the machine restarting.
//
// ⛔ And not the working tree, even with a .gitignore. A .gitignore is a safety
// net, not a barrier: `git add -f`, a fresh clone, or any tool that does not
// consult it puts the file in history anyway. outdir refuses any directory
// inside a repository, so the rule holds where it cannot be forgotten.
func Dir() (string, error) { return outdir.Ensure(spec("")) }

// Choose reports where want would go, WITHOUT creating anything — the decision
// on its own, so a test can exercise the refusal without writing a screenshot
// somewhere to find out.
func Choose(want string) (string, error) { return outdir.Choose(spec(want)) }

// Path is Dir joined with name, absolute, for a driver that takes it as a
// command-line argument.
func Path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(dir, name))
}

// RepoRootOf reports the repository a path is inside, or "" when it is in none.
// It is outdir's, re-exported so a test can state the claim this package exists
// for without importing outdir itself.
func RepoRootOf(path string) string { return outdir.RepoRootOf(path) }
