// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"strings"
	"testing"
)

// A cache key is a stored string that outlives the session, and a remote URL can
// carry a credential. These pin that the secret cannot reach the store, and that
// the store cannot grow without bound.

func TestAWorkspaceKeyCannotCarryACredential(t *testing.T) {
	// Deliberately NOT shaped like a real token: this file is read by tools and
	// printed by test runners, and a realistic-looking secret in a fixture is
	// one copy-paste away from being treated as a real one.
	const secret = "CREDENTIAL-PLACEHOLDER"
	key := workspacePath("https://user:"+secret+"@forge.example/o/r.git", "main")
	if strings.Contains(key, secret) {
		t.Fatalf("the key carries the credential verbatim: %q", key)
	}
	if strings.Contains(key, "user") || strings.Contains(key, "forge.example") {
		t.Fatalf("the key embeds the remote rather than hashing it: %q", key)
	}
	if !strings.HasPrefix(key, "/__gotex-workspace/") {
		t.Fatalf("key = %q, want it under the workspace path", key)
	}
}

func TestWorkspaceKeysSeparateRepositoriesAndBranches(t *testing.T) {
	a := workspacePath("https://forge.example/o/r.git", "main")
	b := workspacePath("https://forge.example/o/r.git", "draft")
	c := workspacePath("https://forge.example/o/other.git", "main")
	if a == b {
		t.Fatal("two branches of one repository share a key; one would overwrite the other")
	}
	if a == c {
		t.Fatal("two repositories share a key")
	}
	if a != workspacePath("https://forge.example/o/r.git", "main") {
		t.Fatal("the key is not stable across calls, so nothing could ever be found again")
	}
}

func TestPruneCountKeepsTheMostRecent(t *testing.T) {
	for _, tc := range []struct {
		total, keep, want int
	}{
		{0, 4, 0},
		{4, 4, 0}, // exactly at the bound: nothing to do
		{5, 4, 1}, // one over
		{9, 4, 5},
		{3, 4, 0},  // under the bound
		{3, 0, 3},  // keep nothing
		{3, -1, 3}, // a negative bound must not delete a negative number
	} {
		if got := pruneCount(tc.total, tc.keep); got != tc.want {
			t.Errorf("pruneCount(%d, %d) = %d, want %d", tc.total, tc.keep, got, tc.want)
		}
	}
}
