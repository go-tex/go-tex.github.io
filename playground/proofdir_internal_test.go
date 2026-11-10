// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/proofdir"
)

// shotDirFor is the in-package half of the same rule the external proofs use:
// screenshots go somewhere durable and never inside a repository. The decision
// itself lives in internal/proofdir, once, because these tests and the browser
// proofs are in two different packages and a rule enforced in one is a rule the
// other can forget.
func shotDirFor(t testing.TB) string {
	t.Helper()
	dir, err := proofdir.Dir()
	if err != nil {
		t.Fatalf("screenshot directory: %v", err)
	}
	t.Logf("screenshots go to %s", dir)
	return dir
}
