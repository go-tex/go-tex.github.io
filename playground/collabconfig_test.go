// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-crdt/collab"
	"github.com/go-crdt/crdt"
)

// A guest may not write as another site.
//
// The servers that use this config are js/wasm only, so this is the lane that
// can ask. What it checks is the wiring: collab tests the policy itself.
func TestARoomRefusesOperationsMadeByAnotherSite(t *testing.T) {
	cfg := roomConfig(collab.NewMemoryStore())
	if cfg.AuthorizeOperations == nil {
		t.Fatal("a room takes any site's operations from any peer")
	}
	made := func(site crdt.SiteID) []crdt.PartOps {
		t.Helper()
		c := crdt.NewComposite(site)
		body, err := c.Text("body")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := body.Insert(0, "typed by somebody"); err != nil {
			t.Fatal(err)
		}
		return c.OpsSince(nil)
	}
	if err := cfg.AuthorizeOperations(t.Context(), "room", 3, made(3)); err != nil {
		t.Errorf("a guest's own operations were refused: %v", err)
	}
	if err := cfg.AuthorizeOperations(t.Context(), "room", 3, made(4)); err == nil {
		t.Error("a guest wrote as another site")
	}
}
